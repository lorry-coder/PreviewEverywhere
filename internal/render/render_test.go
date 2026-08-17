package render

import (
	"regexp"
	"strings"
	"testing"
)

func mustRender(t *testing.T, src string, opt Options) *Result {
	t.Helper()
	res, err := Render([]byte(src), opt)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	return res
}

func TestFrontMatterAndTitle(t *testing.T) {
	res := mustRender(t, `---
title: 迁移风险评估
project: auth-refactor
tags: [风险, 待复核]
summary: 双写窗口期是主要风险来源
---

## 风险清单

切换期间订单写入同时落到旧表与快照表。
`, Options{FallbackTitle: "risk.md"})

	if res.Title != "迁移风险评估" {
		t.Errorf("标题应取自 front-matter，实得 %q", res.Title)
	}
	if res.Project != "auth-refactor" {
		t.Errorf("项目应取自 front-matter，实得 %q", res.Project)
	}
	if len(res.Tags) != 2 || res.Tags[0] != "风险" || res.Tags[1] != "待复核" {
		t.Errorf("标签解析错误: %v", res.Tags)
	}
	if res.Summary != "双写窗口期是主要风险来源" {
		t.Errorf("摘要应取自 front-matter，实得 %q", res.Summary)
	}
	if strings.Contains(res.HTML, "front-matter") || strings.Contains(res.HTML, "auth-refactor") {
		t.Error("front-matter 不应出现在渲染产物里")
	}
	if len(res.TOC) != 1 || res.TOC[0].Level != 2 || res.TOC[0].Text != "风险清单" {
		t.Errorf("目录抽取错误: %+v", res.TOC)
	}
}

// tags 写成裸字符串也要能解析——agent 两种写法都会用。
func TestFrontMatterScalarTags(t *testing.T) {
	res := mustRender(t, "---\ntags: 风险, 待复核\n---\n\n正文\n", Options{})
	if len(res.Tags) != 2 {
		t.Fatalf("标量形式的 tags 应被拆开，实得 %v", res.Tags)
	}
}

// front-matter 写坏了不能让整篇文档进不来。
func TestBrokenFrontMatterFallsBackToBody(t *testing.T) {
	res := mustRender(t, "---\ntitle: [坏掉的\n  缩进\n---\n\n正文段落\n", Options{FallbackTitle: "x.md"})
	if !strings.Contains(res.Plain, "正文段落") {
		t.Errorf("解析失败时应保留正文，实得 %q", res.Plain)
	}
}

func TestTitleFromLeadingH1IsRemovedFromBody(t *testing.T) {
	res := mustRender(t, "# 构建失败分析\n\n第一段正文。\n", Options{FallbackTitle: "build.md"})
	if res.Title != "构建失败分析" {
		t.Errorf("标题应取自首个 H1，实得 %q", res.Title)
	}
	if strings.Contains(res.HTML, "<h1") {
		t.Error("作为标题的 H1 应从正文中移除，避免与页头重复")
	}
	if strings.Contains(res.Plain, "构建失败分析") {
		t.Error("被摘走的 H1 不应留在纯文本里")
	}
}

var blkRe = regexp.MustCompile(`data-blk="([^"]+)"`)

func blockIDs(html string) []string {
	matches := blkRe.FindAllStringSubmatch(html, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// 这是整个平台最要紧的一条性质：内容不变则块 ID 不变。
// P3 的批注靠它零成本存活——agent 重写文档时没动过的段落必须命中原 ID。
func TestBlockIDStableAcrossRewrite(t *testing.T) {
	v1 := `## 风险清单

切换期间订单写入同时落到旧表与快照表。

双写窗口期若超过 30 分钟，两边会出现不可收敛的偏差。

## 回滚方案

停写、重放、切回旧表。
`
	// 模拟 agent 重跑：改掉中间一段，其余原样。
	v2 := `## 风险清单

切换期间订单写入同时落到旧表与快照表。

双写窗口期若超过 15 分钟就应当告警，超过 30 分钟则不可收敛。

## 回滚方案

停写、重放、切回旧表。
`
	a := blockIDs(mustRender(t, v1, Options{}).HTML)
	b := blockIDs(mustRender(t, v2, Options{}).HTML)

	if len(a) != 5 || len(b) != 5 {
		t.Fatalf("应各有 5 个块，实得 %d / %d", len(a), len(b))
	}
	for _, i := range []int{0, 1, 3, 4} {
		if a[i] != b[i] {
			t.Errorf("第 %d 块未改动，ID 却变了：%s → %s", i, a[i], b[i])
		}
	}
	if a[2] == b[2] {
		t.Error("第 2 块内容已改，ID 应当变化")
	}
}

// 空白与换行位置的变化不该让 ID 漂移。
func TestBlockIDIgnoresWhitespaceNoise(t *testing.T) {
	a := blockIDs(mustRender(t, "一段话，中间有内容。\n", Options{}).HTML)
	b := blockIDs(mustRender(t, "一段话，中间有内容。\n\n", Options{}).HTML)
	c := blockIDs(mustRender(t, "一段话，\n中间有内容。\n", Options{}).HTML)
	if a[0] != b[0] || a[0] != c[0] {
		t.Errorf("空白差异不应影响块 ID: %s / %s / %s", a[0], b[0], c[0])
	}
}

// 嵌套结构里只有叶子块拿 ID，否则纯文本会被重复统计。
func TestOnlyLeafBlocksGetIDs(t *testing.T) {
	res := mustRender(t, "- 第一项\n- 第二项\n\n> 引用段落\n", Options{})
	if n := strings.Count(res.HTML, "data-blk"); n != 3 {
		t.Errorf("应为 2 个 li + 1 个引用内的 p 共 3 个块，实得 %d\n%s", n, res.HTML)
	}
	if strings.Count(res.Plain, "第一项") != 1 {
		t.Errorf("纯文本重复统计了嵌套块: %q", res.Plain)
	}
}

func TestMermaidLeftForClient(t *testing.T) {
	res := mustRender(t, "```mermaid\ngraph TD;\n  A-->B;\n```\n", Options{})
	if !strings.Contains(res.HTML, `class="mermaid"`) {
		t.Errorf("mermaid 代码块应转成 <pre class=\"mermaid\">，实得:\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, "graph TD") {
		t.Error("mermaid 源码应原样保留给前端渲染")
	}
	if strings.Contains(res.HTML, "<span") {
		t.Error("mermaid 不应被当成普通代码块做语法高亮")
	}
}

func TestCodeHighlighting(t *testing.T) {
	res := mustRender(t, "```go\nfunc main() {}\n```\n", Options{})
	if !strings.Contains(res.HTML, "chroma") {
		t.Errorf("Go 代码块应被高亮，实得:\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, "<span") {
		t.Error("高亮应产生 span 标记")
	}
	// 未知语言保持纯文本，不去猜。
	res2 := mustRender(t, "```zzz-unknown\nsome text\n```\n", Options{})
	if strings.Contains(res2.HTML, "chroma") {
		t.Error("未知语言不应被强行高亮")
	}
}

func TestRelativeImageRewrite(t *testing.T) {
	resolved := map[string]string{"./img/arch.png": "/api/v1/asset/abc123"}
	res := mustRender(t, "![架构图](./img/arch.png)\n\n![缺失的](./img/gone.png)\n", Options{
		AssetResolver: func(ref string) (string, bool) {
			u, ok := resolved[ref]
			return u, ok
		},
	})
	if !strings.Contains(res.HTML, "/api/v1/asset/abc123") {
		t.Errorf("相对图片应被改写成平台内 URL，实得:\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, "data-pe-missing") {
		t.Error("找不到的图片应被打标，前端才能显示占位块而不是裂图")
	}
	if len(res.MissingAssets) != 1 {
		t.Errorf("应记录 1 个缺失资源，实得 %v", res.MissingAssets)
	}
}

func TestScriptIsStripped(t *testing.T) {
	res := mustRender(t, "正文\n\n<script>alert(1)</script>\n\n<p onclick=\"evil()\">点我</p>\n", Options{})
	if strings.Contains(res.HTML, "<script") || strings.Contains(res.HTML, "alert") {
		t.Errorf("script 应被净化掉:\n%s", res.HTML)
	}
	if strings.Contains(res.HTML, "onclick") {
		t.Errorf("事件属性应被净化掉:\n%s", res.HTML)
	}
}

func TestHTMLDocReaderMode(t *testing.T) {
	res := mustRender(t, `<!doctype html><html><head><title>Report</title></head>
<body><h1>测试结果</h1><p>全部用例通过。</p></body></html>`,
		Options{Kind: "html", FallbackTitle: "result.html"})

	if res.RenderMode != "reader" {
		t.Errorf("纯文本 HTML 应走 reader 模式，实得 %q", res.RenderMode)
	}
	if res.Title != "测试结果" {
		t.Errorf("正文 H1 应优先于 <title>，实得 %q", res.Title)
	}
	if !strings.Contains(res.HTML, "data-blk") {
		t.Error("reader 模式的 HTML 也应注入块 ID")
	}
	if !strings.Contains(res.Plain, "全部用例通过") {
		t.Errorf("纯文本抽取失败: %q", res.Plain)
	}
}

func TestHTMLDocRawMode(t *testing.T) {
	res := mustRender(t, `<!doctype html><html><body>
<h1>构建趋势</h1><p>近七天平均构建耗时下降了 18%。</p>
<canvas id="c"></canvas><script>draw()</script></body></html>`,
		Options{Kind: "html", FallbackTitle: "trend.html"})

	if res.RenderMode != "raw" {
		t.Errorf("带脚本与 canvas 的 HTML 应走 raw 模式，实得 %q", res.RenderMode)
	}
	if res.HTML != "" {
		t.Error("raw 模式不应留渲染产物，前端直接取原文")
	}
	if res.Title != "构建趋势" {
		t.Errorf("raw 模式也要抽出标题，实得 %q", res.Title)
	}
	// 关键：raw 模式虽然不可批注，但仍然要能被搜到——
	// 所以即便丢弃渲染产物，也照样跑一遍 reader 管线取纯文本。
	if !strings.Contains(res.Plain, "平均构建耗时") {
		t.Errorf("raw 模式仍应抽出纯文本供检索，实得 %q", res.Plain)
	}
}

func TestSummaryFallsBackToFirstParagraph(t *testing.T) {
	res := mustRender(t, "# 标题\n\n## 小节\n\n这是第一段正文，应该成为摘要。\n", Options{})
	if res.Summary != "这是第一段正文，应该成为摘要。" {
		t.Errorf("摘要应取第一段非标题正文，实得 %q", res.Summary)
	}
}

func TestGFMTable(t *testing.T) {
	res := mustRender(t, "| 项 | 值 |\n| --- | --- |\n| 延迟 | 30ms |\n", Options{})
	if !strings.Contains(res.HTML, "<table") || !strings.Contains(res.HTML, "30ms") {
		t.Errorf("GFM 表格未渲染:\n%s", res.HTML)
	}
}

// 既没有 <title> 也没有开头 H1 的 HTML 曾经导致空指针崩溃：
// renderHTMLDoc 拿 findNode(doc, "title") 的返回值直接进 textContent，
// 而找不到时它返回 nil。有 H1 的文档先在 H1 分支设好标题、根本不走这一支，
// 所以原有的 HTMLDoc 测试全都盖不到。现实中这种文档并不罕见——
// 构建产物里的许可证页就是。
func TestHTMLWithoutTitleOrH1(t *testing.T) {
	res, err := Render([]byte(`<html><body><p>纯正文，没有 title 也没有 h1</p></body></html>`),
		Options{Kind: "html", FallbackTitle: "无题.html"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Title == "" {
		t.Error("应当退回到文件名，而不是留空")
	}
}

// 空文档、只有注释的文档同样不能崩。采集是在后台扫目录时跑的，
// 一篇畸形文档不该把整个服务带走。
func TestDegenerateInputsDoNotPanic(t *testing.T) {
	inputs := []string{"", "   ", "<!-- 只有注释 -->", "<html></html>", "<title></title>", "\x00\x01"}
	for _, kind := range []string{"markdown", "html"} {
		for _, in := range inputs {
			if _, err := Render([]byte(in), Options{Kind: kind}); err != nil {
				t.Errorf("kind=%s input=%q: %v", kind, in, err)
			}
		}
	}
}
