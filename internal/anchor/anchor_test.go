package anchor

import (
	"strings"
	"testing"
)

// buildDoc 把若干段落拼成「纯文本 + 块索引」，模拟渲染管线的产物：
// 块之间用两个换行分隔，块 ID 就是内容的哈希（这里用内容本身代替，
// 只要满足「内容不变则 ID 不变」这一条性质即可）。
func buildDoc(paras ...string) (string, []Block) {
	var sb strings.Builder
	var blocks []Block
	off := 0
	for i, p := range paras {
		if i > 0 {
			sb.WriteString("\n\n")
			off += 2
		}
		sb.WriteString(p)
		blocks = append(blocks, Block{Blk: blkID(p), Off: off, Len: len([]rune(p))})
		off += len([]rune(p))
	}
	return sb.String(), blocks
}

// blkID 模拟「块 ID = 内容哈希」。
func blkID(s string) string {
	var h uint32 = 2166136261
	for _, r := range s {
		h ^= uint32(r)
		h *= 16777619
	}
	const digits = "abcdefghijklmnopqrstuvwxyz234567"
	out := make([]byte, 8)
	for i := range out {
		out[i] = digits[h&31]
		h >>= 3
	}
	return string(out)
}

func anchorIn(plain string, blocks []Block, para, quote string) Anchor {
	return anchorAt(plain, blocks, para, quote, 0)
}

// anchorAt 在第 nth 次出现处构造锚点。
// 前后文必须取自同一处，否则上下文自相矛盾，消歧当然失效。
func anchorAt(plain string, blocks []Block, para, quote string, nth int) Anchor {
	text := []rune(plain)
	byteAt, from := -1, 0
	for i := 0; i <= nth; i++ {
		rel := strings.Index(plain[from:], quote)
		if rel < 0 {
			panic("找不到第 " + string(rune('0'+nth)) + " 处引文")
		}
		byteAt = from + rel
		from = byteAt + len(quote)
	}
	docOff := len([]rune(plain[:byteAt]))
	blk := blkID(para)
	var blockOff int
	for _, b := range blocks {
		if b.Blk == blk {
			blockOff = b.Off
		}
	}
	return Anchor{
		Blk:      blk,
		StartOff: docOff - blockOff,
		EndOff:   docOff - blockOff + len([]rune(quote)),
		Exact:    quote,
		Prefix:   contextBefore(text, docOff),
		Suffix:   contextAfter(text, docOff+len([]rune(quote))),
		DocOff:   docOff,
	}
}

const (
	p1 = "订单表从 MySQL 5.7 迁到 8.0，期间需要双写。本文评估切换窗口内的风险。"
	p2 = "切换期间订单写入同时落到旧表与快照表。双写窗口期若超过 30 分钟，两边会出现不可收敛的偏差，此时只能停写重放。"
	p3 = "停写、重放 binlog、切回旧表。整个过程约 8 分钟。"
)

// 最常见的情形：agent 重跑一遍，大部分段落一字未改。
// 这些批注必须零成本命中，否则每次重跑都要人工复核一遍。
func TestUnchangedBlockHitsDirectly(t *testing.T) {
	plain, blocks := buildDoc(p1, p2, p3)
	a := anchorIn(plain, blocks, p2, "不可收敛")

	// 只改了第一段，第二段原样。
	newPlain, newBlocks := buildDoc("订单表迁移，本文评估风险。", p2, p3)
	got := Relocate(a, newPlain, newBlocks)

	if got.State != StateOK {
		t.Fatalf("未改动的块应直接命中，实得 %s", got.State)
	}
	if got.Anchor.Blk != a.Blk {
		t.Errorf("块 ID 不该变: %s → %s", a.Blk, got.Anchor.Blk)
	}
	// 前面的段落变短了，全文偏移应当跟着更新。
	newText := []rune(newPlain)
	at := got.Anchor.DocOff
	if string(newText[at:at+4]) != "不可收敛" {
		t.Errorf("全文偏移没有跟着更新，指向了 %q", string(newText[at:at+6]))
	}
}

// 段落被改写，但划的那句话还在（周围变了）。这是重定位真正要解决的情况。
func TestRewrittenBlockRelocatesByQuote(t *testing.T) {
	plain, blocks := buildDoc(p1, p2, p3)
	a := anchorIn(plain, blocks, p2, "不可收敛的偏差")

	rewritten := "切换期间订单写入会同时落到旧表与快照表两处。双写窗口期一旦超过 15 分钟，" +
		"两边就会出现不可收敛的偏差，那时只能停写重放。"
	newPlain, newBlocks := buildDoc(p1, rewritten, p3)

	got := Relocate(a, newPlain, newBlocks)
	if got.State != StateMoved {
		t.Fatalf("改写过的块应走引文匹配，实得 %s", got.State)
	}
	if got.Anchor.Blk != blkID(rewritten) {
		t.Errorf("应迁移到改写后的块，实得 %s", got.Anchor.Blk)
	}

	// 关键：新的块内偏移必须真的指向那句话。
	text, ok := BlockText(newPlain, newBlocks, got.Anchor.Blk)
	if !ok {
		t.Fatal("取不到目标块的文本")
	}
	r := []rune(text)
	if got.Anchor.EndOff > len(r) {
		t.Fatalf("偏移越界: %d > %d", got.Anchor.EndOff, len(r))
	}
	if landed := string(r[got.Anchor.StartOff:got.Anchor.EndOff]); landed != "不可收敛的偏差" {
		t.Errorf("重定位落在了 %q，而不是原来那句话", landed)
	}
}

// 引文本身被轻微改动（15% 以内）也要能找回来。
func TestQuoteSurvivesSmallEdits(t *testing.T) {
	plain, blocks := buildDoc(p1, p2, p3)
	a := anchorIn(plain, blocks, p2, "双写窗口期若超过 30 分钟")

	rewritten := "切换期间订单写入同时落到旧表与快照表。双写窗口期若超过 45 分钟，两边会出现不可收敛的偏差。"
	newPlain, newBlocks := buildDoc(p1, rewritten, p3)

	got := Relocate(a, newPlain, newBlocks)
	if got.State != StateMoved {
		t.Fatalf("轻微改动的引文应当仍能找回，实得 %s（score=%.2f）", got.State, got.Score)
	}
	if !strings.Contains(got.Anchor.Exact, "双写窗口期若超过") {
		t.Errorf("匹配到的引文不对: %q", got.Anchor.Exact)
	}
}

// 原文真的没了就老实说失联，不要瞎猜一个位置。猜错比找不到更糟。
func TestVanishedTextBecomesOrphan(t *testing.T) {
	plain, blocks := buildDoc(p1, p2, p3)
	a := anchorIn(plain, blocks, p2, "不可收敛的偏差")

	newPlain, newBlocks := buildDoc(p1, "这一段被整个换成了完全无关的内容，讲的是别的事情。", p3)
	got := Relocate(a, newPlain, newBlocks)
	if got.State != StateOrphan {
		t.Fatalf("原文消失时应判为失联，实得 %s（落在 %q）", got.State, got.Anchor.Exact)
	}
}

// 同一句话在文档里出现多次时，靠上下文选对那一处。
func TestContextDisambiguatesRepeatedText(t *testing.T) {
	repeated := "需要人工确认。"
	first := "关于索引的部分，" + repeated
	second := "关于分片的部分，" + repeated
	plain, blocks := buildDoc(first, "中间隔着一段无关内容。", second)

	// 批注打在第二处
	a := anchorAt(plain, blocks, second, repeated, 1)

	// 第二段被改写，块 ID 变了，必须靠上下文找回第二处而不是第一处。
	rewritten := "关于分片的部分，仍然" + repeated
	newPlain, newBlocks := buildDoc(first, "中间隔着一段无关内容。", rewritten)

	got := Relocate(a, newPlain, newBlocks)
	if got.State != StateMoved {
		t.Fatalf("应能重定位，实得 %s", got.State)
	}
	if got.Anchor.Blk != blkID(rewritten) {
		t.Errorf("上下文没能消歧，落到了第一处而不是第二处")
	}
}

// 很短的引文没有容错余地，宁可失联也不能乱配。
func TestShortQuoteDoesNotFuzzyMatch(t *testing.T) {
	plain, blocks := buildDoc("这里写的是甲方案。", "另一段内容。")
	a := anchorIn(plain, blocks, "这里写的是甲方案。", "甲方案")

	newPlain, newBlocks := buildDoc("这里写的是乙方案。", "另一段内容。")
	got := Relocate(a, newPlain, newBlocks)
	if got.State == StateMoved && strings.Contains(got.Anchor.Exact, "乙") {
		t.Errorf("短引文不该被模糊配到别的词上：%q", got.Anchor.Exact)
	}
}

func TestApproxSearchCollapsesRuns(t *testing.T) {
	text := []rune("aaabbbcccbbbaaa")
	got := approxSearch(text, []rune("bbb"), 1)
	// 两片 bbb，中间隔着 ccc，应当只报两个候选而不是十几个。
	if len(got) > 4 {
		t.Errorf("同一处匹配产生了过多候选: %d 个 %+v", len(got), got)
	}
	if len(got) < 2 {
		t.Errorf("两处 bbb 都应被找到，实得 %+v", got)
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "", 3},
		{"双写窗口期", "双写窗口", 1},
		{"30 分钟", "45 分钟", 2},
	}
	for _, c := range cases {
		if got := editDistance([]rune(c.a), []rune(c.b)); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}
