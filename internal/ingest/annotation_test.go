package ingest

import (
	"path/filepath"
	"strings"
	"testing"

	"previeweverywhere/internal/store"
)

// annotate 在文档的某个块上按引文打一条批注。
// 这一步模拟前端：它只知道「哪个块 + 块内起止偏移 + 选中的文字」。
func annotate(t *testing.T, st *store.Store, docID int64, kind, quote, body string) *store.Annotation {
	t.Helper()
	detail, err := st.GetDoc(docID)
	if err != nil {
		t.Fatal(err)
	}

	blk, start := findQuote(t, st, docID, quote)
	ann, err := st.CreateAnnotation(store.NewAnnotation{
		DocID: docID, Kind: kind, Body: body,
		Blk: blk, StartOff: start, EndOff: start + len([]rune(quote)),
		Exact: quote,
	})
	if err != nil {
		t.Fatalf("在 %q 上创建批注失败: %v（文档 %q）", quote, err, detail.Title)
	}
	return ann
}

// findQuote 在文档当前版本里定位引文所属的块与块内偏移。
func findQuote(t *testing.T, st *store.Store, docID int64, quote string) (blk string, startOff int) {
	t.Helper()
	blocks, plain, err := st.DebugHeadBlocks(docID)
	if err != nil {
		t.Fatal(err)
	}
	text := []rune(plain)
	for _, b := range blocks {
		body := string(text[b.Off : b.Off+b.Len])
		if i := strings.Index(body, quote); i >= 0 {
			return b.Blk, len([]rune(body[:i]))
		}
	}
	t.Fatalf("在文档里找不到引文 %q", quote)
	return "", 0
}

const docV1 = `# 迁移风险评估

## 背景

订单表从 MySQL 5.7 迁到 8.0，期间需要双写。本文评估切换窗口内的风险。

## 风险清单

切换期间订单写入同时落到旧表与快照表。双写窗口期若超过 30 分钟，两边会出现不可收敛的偏差，此时只能停写重放。

## 回滚方案

停写、重放 binlog、切回旧表。整个过程约 8 分钟，其中重放占 6 分钟。
`

// agent 重跑：背景段一字未改，风险段被改写，回滚段被整个换掉。
const docV2 = `# 迁移风险评估

## 背景

订单表从 MySQL 5.7 迁到 8.0，期间需要双写。本文评估切换窗口内的风险。

## 风险清单

切换期间订单写入会同时落到旧表与快照表两处。双写窗口期一旦超过 15 分钟，两边就会出现不可收敛的偏差，那时只能停写重放。

## 回滚方案

改用蓝绿部署，不再需要回滚脚本。
`

// 这是 P3 的验收标准：让 agent 重写一份已批注的文档，
// 绝大多数批注自动存活，改写的能自动迁移，删掉的进失联面板。
func TestAnnotationsSurviveRewrite(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "auth-refactor")
	doc := filepath.Join(root, "docs", "risk.md")
	write(t, doc, docV1)

	res, err := p.Ingest(Source{Path: doc})
	if err != nil {
		t.Fatal(err)
	}

	unchanged := annotate(t, st, res.DocID, "highlight", "期间需要双写", "")
	rewritten := annotate(t, st, res.DocID, "note", "不可收敛的偏差", "这条要跟 DBA 确认")
	vanished := annotate(t, st, res.DocID, "todo", "重放占 6 分钟", "确认这个数字")

	// agent 重跑一遍
	write(t, doc, docV2)
	if _, err := p.Ingest(Source{Path: doc}); err != nil {
		t.Fatal(err)
	}

	detail, err := st.GetDoc(res.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Seq != 2 {
		t.Fatalf("文档应更新到 v2，实得 v%d", detail.Seq)
	}

	byID := map[int64]store.Annotation{}
	for _, a := range detail.Annotations {
		byID[a.ID] = a
	}
	if len(byID) != 3 {
		t.Fatalf("三条批注都该留着（失联的也不删），实得 %d 条", len(byID))
	}

	// ① 未改动的段落：零成本命中，正文照旧。
	if got := byID[unchanged.ID]; got.State != "ok" {
		t.Errorf("未改动段落上的批注应为 ok，实得 %s", got.State)
	} else if got.Quote != "期间需要双写" {
		t.Errorf("引文不该变: %q", got.Quote)
	}

	// ② 被改写的段落：引文模糊匹配迁移过去，且偏移要真的指向那句话。
	moved := byID[rewritten.ID]
	if moved.State != "moved" {
		t.Errorf("被改写段落上的批注应为 moved，实得 %s", moved.State)
	}
	if moved.Body != "这条要跟 DBA 确认" {
		t.Errorf("笔记正文丢了: %q", moved.Body)
	}
	assertAnchorLandsOn(t, st, res.DocID, moved, "不可收敛的偏差")

	// ③ 原文消失：判失联、留快照、不删除。
	orphan := byID[vanished.ID]
	if orphan.State != "orphan" {
		t.Errorf("原文消失的批注应为 orphan，实得 %s", orphan.State)
	}
	if orphan.Quote != "重放占 6 分钟" {
		t.Errorf("失联批注应保留当初的原文快照，实得 %q", orphan.Quote)
	}
	if !strings.Contains(orphan.OrphanNote, "v2") {
		t.Errorf("应记下在哪个版本失联的，实得 %q", orphan.OrphanNote)
	}
	if orphan.Body != "确认这个数字" {
		t.Errorf("失联批注的正文也不能丢: %q", orphan.Body)
	}
}

// assertAnchorLandsOn 校验批注的块内偏移确实指向期望的文字。
// 状态对但偏移错的话，页面上高亮的是另一句话，比直接失联更糟。
func assertAnchorLandsOn(t *testing.T, st *store.Store, docID int64, a store.Annotation, want string) {
	t.Helper()
	blocks, plain, err := st.DebugHeadBlocks(docID)
	if err != nil {
		t.Fatal(err)
	}
	text := []rune(plain)
	for _, b := range blocks {
		if b.Blk != a.Blk {
			continue
		}
		body := []rune(string(text[b.Off : b.Off+b.Len]))
		if a.EndOff > len(body) {
			t.Fatalf("偏移越界: [%d,%d) 超出块长度 %d", a.StartOff, a.EndOff, len(body))
		}
		if landed := string(body[a.StartOff:a.EndOff]); landed != want {
			t.Errorf("批注落在了 %q，期望 %q", landed, want)
		}
		return
	}
	t.Fatalf("批注指向的块 %s 不存在", a.Blk)
}

// 失联批注可以手动重挂回去。这是「原文真的没了时把判断权交回给人」的落点。
func TestRebindOrphan(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "proj")
	doc := filepath.Join(root, "docs", "a.md")
	write(t, doc, docV1)

	res, _ := p.Ingest(Source{Path: doc})
	orphaned := annotate(t, st, res.DocID, "todo", "重放占 6 分钟", "确认这个数字")

	write(t, doc, docV2)
	if _, err := p.Ingest(Source{Path: doc}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetAnnotation(orphaned.ID); got.State != "orphan" {
		t.Fatalf("前置条件不成立，状态是 %s", got.State)
	}

	// 用户在新版本里手动指到「蓝绿部署」这四个字上
	blk, start := findQuote(t, st, res.DocID, "蓝绿部署")
	rebound, err := st.Rebind(orphaned.ID, blk, start, start+4, "蓝绿部署")
	if err != nil {
		t.Fatalf("重挂失败: %v", err)
	}
	if rebound.State != "ok" {
		t.Errorf("重挂后应恢复为 ok，实得 %s", rebound.State)
	}
	if rebound.Quote != "蓝绿部署" {
		t.Errorf("引文应更新为新选区，实得 %q", rebound.Quote)
	}
	if rebound.OrphanNote != "" {
		t.Errorf("重挂后失联备注应清空，实得 %q", rebound.OrphanNote)
	}
	if rebound.Body != "确认这个数字" {
		t.Errorf("笔记正文必须保留: %q", rebound.Body)
	}
}

// 待办与疑问汇总成一张跨文档的清单——这是「在通勤路上读文档」的产出物。
func TestActionableList(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "proj")

	docA := filepath.Join(root, "docs", "a.md")
	write(t, docA, docV1)
	resA, _ := p.Ingest(Source{Path: docA})
	annotate(t, st, resA.DocID, "todo", "重放占 6 分钟", "确认这个数字")
	annotate(t, st, resA.DocID, "highlight", "期间需要双写", "")

	docB := filepath.Join(root, "docs", "b.md")
	write(t, docB, "# 另一篇\n\n这里有一个需要确认的地方，涉及分片策略。\n")
	resB, _ := p.Ingest(Source{Path: docB})
	annotate(t, st, resB.DocID, "question", "分片策略", "按用户 ID 还是订单 ID？")

	list, err := st.ListActionable(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("应只汇总 todo 与 question 共 2 条，实得 %d", len(list))
	}
	for _, a := range list {
		if a.DocTitle == "" || a.ProjectName == "" {
			t.Errorf("汇总项要带上出处，实得 %+v", a)
		}
	}
}

// 前端算出的偏移和服务端的规范化文本之间只要有一点漂移，批注就会错位。
// 服务端拿前端自述的引文校正一次，把这种漂移挡在入口。
func TestOffsetDriftIsCorrected(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "proj")
	doc := filepath.Join(root, "docs", "a.md")
	write(t, doc, docV1)
	res, _ := p.Ingest(Source{Path: doc})

	blk, start := findQuote(t, st, res.DocID, "不可收敛的偏差")
	// 故意把偏移报错三个字符
	ann, err := st.CreateAnnotation(store.NewAnnotation{
		DocID: res.DocID, Kind: "highlight",
		Blk: blk, StartOff: start + 3, EndOff: start + 3 + 7,
		Exact: "不可收敛的偏差",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ann.Quote != "不可收敛的偏差" {
		t.Errorf("偏移漂移应被引文校正回来，实得 %q", ann.Quote)
	}
	assertAnchorLandsOn(t, st, res.DocID, *ann, "不可收敛的偏差")
}
