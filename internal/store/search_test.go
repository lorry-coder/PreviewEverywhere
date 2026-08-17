package store

import (
	"strings"
	"testing"

	"previeweverywhere/internal/search"
)

// seed 塞几篇内容各异的文档，模拟真实的 agent 产出。
func seed(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	auth, err := s.EnsureProject("auth-refactor", "auth-refactor", "")
	if err != nil {
		t.Fatal(err)
	}
	ci, err := s.EnsureProject("ci-pipeline", "ci-pipeline", "")
	if err != nil {
		t.Fatal(err)
	}

	save := func(pid int64, key, title, plain string, tags []string) int64 {
		res, err := s.SaveDoc(SaveDocInput{
			ProjectID: pid, SourceKey: key, Title: title, Kind: "markdown",
			ContentSha: key + title, RawBlob: "x", HTML: "<p>x</p>", Plain: plain,
			TOC: "[]", Tags: tags,
		})
		if err != nil {
			t.Fatalf("写入 %s 失败: %v", key, err)
		}
		return res.DocID
	}

	save(auth, "docs/risk.md", "迁移风险评估",
		"切换期间订单写入同时落到旧表与快照表。双写窗口期若超过 30 分钟，两边会出现不可收敛的偏差。",
		[]string{"风险", "待复核"})
	save(auth, "docs/plan.md", "重构方案",
		"分三步走：先建影子表，再开双写，最后切流量。",
		[]string{"结论"})
	save(ci, "docs/build.md", "构建失败分析",
		"第三次重试后仍然失败，怀疑是缓存键没把 Go 版本算进去。",
		[]string{"风险", "已解决"})
	return s
}

func titles(hits []SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Title
	}
	return out
}

func mustSearch(t *testing.T, s *Store, q string) []SearchHit {
	t.Helper()
	hits, err := s.Search(search.Parse(q), 20)
	if err != nil {
		t.Fatalf("检索 %q 出错: %v", q, err)
	}
	return hits
}

// P2 的验收目标：「上周那份提到双写的评估」十秒内翻出来。
func TestChineseFullText(t *testing.T) {
	s := seed(t)

	hits := mustSearch(t, s, "不可收敛")
	if len(hits) != 1 || hits[0].Title != "迁移风险评估" {
		t.Fatalf("三字以上中文词应走 FTS 命中，实得 %v", titles(hits))
	}
	if !strings.Contains(hits[0].Snippet, "<mark>不可收敛</mark>") {
		t.Errorf("片段应高亮命中词，实得 %q", hits[0].Snippet)
	}
}

// trigram 分词器索引不了两字词，必须有 LIKE 回落，否则「双写」这种最常用的
// 中文二字词一个都搜不到——那样整个检索功能就是废的。
func TestTwoCharChineseFallsBackToLike(t *testing.T) {
	s := seed(t)
	hits := mustSearch(t, s, "双写")
	got := titles(hits)
	if len(got) != 2 {
		t.Fatalf("「双写」应命中 2 篇，实得 %v", got)
	}
}

func TestTitleMatchRanksFirst(t *testing.T) {
	s := seed(t)
	hits := mustSearch(t, s, "构建")
	if len(hits) == 0 || hits[0].Title != "构建失败分析" {
		t.Errorf("标题命中应排最前，实得 %v", titles(hits))
	}
}

func TestQuerySyntaxCombination(t *testing.T) {
	s := seed(t)

	if got := titles(mustSearch(t, s, "tag:待复核")); len(got) != 1 || got[0] != "迁移风险评估" {
		t.Errorf("标签过滤失败: %v", got)
	}
	if got := titles(mustSearch(t, s, "tag:风险 -tag:已解决")); len(got) != 1 || got[0] != "迁移风险评估" {
		t.Errorf("标签排除失败: %v", got)
	}
	if got := titles(mustSearch(t, s, "project:ci-pipeline 缓存键")); len(got) != 1 {
		t.Errorf("项目内检索失败: %v", got)
	}
	if got := titles(mustSearch(t, s, "project:auth-refactor 缓存键")); len(got) != 0 {
		t.Errorf("跨项目不该命中: %v", got)
	}
	if got := titles(mustSearch(t, s, "is:unread")); len(got) != 3 {
		t.Errorf("新入库文档都应是未读，实得 %v", got)
	}
}

func TestEmptyQueryReturnsNothing(t *testing.T) {
	s := seed(t)
	if hits := mustSearch(t, s, "   "); len(hits) != 0 {
		t.Errorf("空查询应返回空，而不是全量：%v", titles(hits))
	}
}

// 片段是直接塞进 DOM 的，除了自己加的 <mark> 之外必须全部转义。
func TestSnippetEscapesHTML(t *testing.T) {
	got := Snippet("这里有 <script>alert(1)</script> 和关键词在后面", []string{"关键词"}, 30)
	if strings.Contains(got, "<script") {
		t.Errorf("片段没有转义原文里的标签: %q", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("期望看到转义后的标签: %q", got)
	}
	if !strings.Contains(got, "<mark>关键词</mark>") {
		t.Errorf("命中词应被高亮: %q", got)
	}
}

// 窗口按字符取，不能从半个汉字中间切开。
func TestSnippetWindowIsRuneBased(t *testing.T) {
	long := strings.Repeat("前文", 200) + "命中词" + strings.Repeat("后文", 200)
	got := Snippet(long, []string{"命中词"}, 20)
	if !strings.Contains(got, "<mark>命中词</mark>") {
		t.Fatalf("没截到命中词: %q", got)
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Errorf("两端截断应有省略号: %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Errorf("出现了替换字符，说明从字节中间切开了: %q", got)
	}
}

func TestSetDocTags(t *testing.T) {
	s := seed(t)
	docs, err := s.ListDocs(DocFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var risk Doc
	for _, d := range docs {
		if d.Title == "迁移风险评估" {
			risk = d
		}
	}

	if _, err := s.SetDocTags(risk.ID, []string{"风险", "已排期"}); err != nil {
		t.Fatalf("设置标签失败: %v", err)
	}
	detail, err := s.GetDoc(risk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(detail.Tags, ",") != "已排期,风险" {
		t.Errorf("标签应为「已排期,风险」，实得 %v", detail.Tags)
	}
	// 删掉的标签不该还能检索到。
	if got := titles(mustSearch(t, s, "tag:待复核")); len(got) != 0 {
		t.Errorf("已删除的标签仍能检索到: %v", got)
	}
	// 新加的能检索到。
	if got := titles(mustSearch(t, s, "tag:已排期")); len(got) != 1 {
		t.Errorf("新加的标签检索不到: %v", got)
	}
}

func TestTimelineGroupsByDayAndProject(t *testing.T) {
	s := seed(t)
	groups, err := s.Timeline(50)
	if err != nil {
		t.Fatalf("时间线失败: %v", err)
	}
	// 三篇文档、两个项目、同一天 → 应该分成两组。
	if len(groups) != 2 {
		t.Fatalf("期望 2 组（按日期+项目降级分组），实得 %d 组", len(groups))
	}
	total := 0
	for _, g := range groups {
		total += len(g.Docs)
		if g.Unread != len(g.Docs) {
			t.Errorf("组 %s 的未读数不对: %d / %d", g.ProjectName, g.Unread, len(g.Docs))
		}
		if g.ProjectName == "" {
			t.Error("组里应带项目名")
		}
	}
	if total != 3 {
		t.Errorf("文档总数应为 3，实得 %d", total)
	}
}

func TestTimelineGroupsByRun(t *testing.T) {
	s := seed(t)
	pid, _ := s.EnsureProject("infra", "infra", "")
	runID, err := s.EnsureRun(pid, "sess-abc123def456", "夜间巡检")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a.md", "b.md"} {
		if _, err := s.SaveDoc(SaveDocInput{
			ProjectID: pid, RunID: runID, SourceKey: key, Title: key, Kind: "markdown",
			ContentSha: key, RawBlob: "x", HTML: "<p>x</p>", Plain: "内容", TOC: "[]",
		}); err != nil {
			t.Fatal(err)
		}
	}

	groups, err := s.Timeline(50)
	if err != nil {
		t.Fatal(err)
	}
	if groups[0].RunID != runID {
		t.Fatalf("最新一组应是那次 run，实得 %+v", groups[0])
	}
	if len(groups[0].Docs) != 2 {
		t.Errorf("同一次 run 的两篇应聚在一组，实得 %d", len(groups[0].Docs))
	}
	if groups[0].RunLabel != "夜间巡检" {
		t.Errorf("应显示 run 的标签，实得 %q", groups[0].RunLabel)
	}
}
