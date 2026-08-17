// Package render 把 Markdown 与 agent 生成的 HTML 渲染成阅读端直接可用的
// HTML 片段，同时产出纯文本、目录与块 ID。
//
// 渲染放在服务端而不是浏览器，理由不是性能而是稳定性：产物一次生成、多端复用，
// DOM 结构逐字节一致。放在客户端渲染的话，库版本、字体、屏幕宽度的差异都会让
// DOM 产生细微变化，P3 的批注锚点就会在手机和电脑之间漂移。
package render

import (
	"path/filepath"
	"strings"

	"previeweverywhere/internal/anchor"
)

type Options struct {
	Kind          string // markdown | html
	FallbackTitle string // 通常是文件名
	// AssetResolver 把文档里的相对资源引用换成平台内的 URL。
	// 由采集管线提供，因为只有它知道源文件在磁盘上的位置。
	AssetResolver func(ref string) (string, bool)
}

type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	Blk   string `json:"blk"`
}

type Result struct {
	Title      string
	Summary    string
	Project    string // 来自 front-matter，采集管线用它决定归属
	Run        string
	Tags       []string
	Kind       string
	RenderMode string // reader | raw

	HTML  string // 渲染产物；raw 模式下为空，由前端 iframe 直接取原文
	Plain string
	TOC   []Heading
	Chars int
	// Blocks 是块 ID 到纯文本偏移的索引，批注重定位靠它在
	// 「块 ID」和「全文偏移」之间换算。
	Blocks []anchor.Block

	MissingAssets []string
}

// KindForPath 按扩展名判断文档类型。
func KindForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		return "html"
	default:
		return "markdown"
	}
}

func Render(src []byte, opt Options) (*Result, error) {
	if opt.Kind == "html" {
		return renderHTMLDoc(src, opt)
	}
	return renderMarkdownDoc(src, opt)
}

func renderMarkdownDoc(src []byte, opt Options) (*Result, error) {
	fm, body := SplitFrontMatter(src)

	res := &Result{
		Kind:       "markdown",
		RenderMode: "reader",
		Title:      strings.TrimSpace(fm.Title),
		Summary:    strings.TrimSpace(fm.Summary),
		Project:    strings.TrimSpace(fm.Project),
		Run:        strings.TrimSpace(fm.Run),
		Tags:       fm.Tags,
		TOC:        []Heading{},
		Blocks:     []anchor.Block{},
	}

	rendered, err := renderMarkdown(body)
	if err != nil {
		return nil, err
	}
	clean := Policy().SanitizeBytes(rendered)

	out, err := postProcess(string(clean), opt, res)
	if err != nil {
		return nil, err
	}
	res.HTML = out
	finalize(res, opt)
	return res, nil
}

func finalize(res *Result, opt Options) {
	if res.Title == "" {
		res.Title = opt.FallbackTitle
	}
	if res.Title == "" {
		res.Title = "无标题文档"
	}
	if res.Summary == "" {
		res.Summary = excerpt(res)
	}
	if res.Tags == nil {
		res.Tags = []string{}
	}
}

// excerpt 取正文第一段作为列表页的摘要行——一行就能判断值不值得点开。
// 跳过开头的标题块，否则摘要会跟标题重复。
func excerpt(res *Result) string {
	blocks := strings.Split(res.Plain, "\n\n")
	headings := map[string]bool{}
	for _, h := range res.TOC {
		headings[h.Text] = true
	}
	for _, b := range blocks {
		b = strings.TrimSpace(b)
		if b == "" || headings[b] {
			continue
		}
		r := []rune(b)
		if len(r) > 100 {
			return string(r[:100]) + "…"
		}
		return b
	}
	return ""
}
