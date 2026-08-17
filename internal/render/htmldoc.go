package render

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"

	"previeweverywhere/internal/anchor"
)

// renderHTMLDoc 处理 agent 直接生成的 HTML。这类文档差别很大——有的是纯文本
// 报告，有的带 ECharts 和交互——所以给两条路：
//
//	reader：净化后套平台排版。可批注、可检索、移动端排版正常，代价是丢掉原有样式。
//	raw   ：整页交给前端的 sandbox iframe。保留图表与交互，代价是不可批注。
//
// 两种模式都会跑一遍 reader 管线来取纯文本与目录，所以 raw 模式的文档一样能被搜到。
func renderHTMLDoc(src []byte, opt Options) (*Result, error) {
	doc, err := html.Parse(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("解析 HTML 失败: %w", err)
	}

	res := &Result{
		Kind:       "html",
		RenderMode: detectRenderMode(doc),
		TOC:        []Heading{},
		Blocks:     []anchor.Block{},
	}

	body := findNode(doc, "body")
	if body == nil {
		body = doc
	}
	clean := Policy().Sanitize(renderChildren(body))

	out, err := postProcess(clean, opt, res)
	if err != nil {
		return nil, err
	}

	if res.RenderMode == "reader" {
		res.HTML = out
	} else {
		// raw 模式下前端走 /api/v1/raw/<versionId> 取原文，这里不留渲染产物。
		res.HTML = ""
	}

	// 正文里的 H1 优先于 <title>：agent 生成的 <title> 常常是 "Report" 这种泛称。
	if res.Title == "" {
		res.Title = normalizeSpace(textContent(findNode(doc, "title")))
	}
	finalize(res, opt)
	return res, nil
}

// detectRenderMode 判断这份 HTML 是否必须原样渲染才有意义。
// 阈值刻意偏保守：能走 reader 就走 reader，因为 reader 模式才能批注和检索。
func detectRenderMode(doc *html.Node) string {
	var script, canvas, iframe, svg int
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script":
				script++
			case "canvas":
				canvas++
			case "iframe", "object", "embed":
				iframe++
			case "svg":
				svg++
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if script > 0 || canvas > 0 || iframe > 0 || svg > 5 {
		return "raw"
	}
	return "reader"
}

func findNode(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findNode(c, tag); found != nil {
			return found
		}
	}
	return nil
}

func renderChildren(n *html.Node) string {
	var buf bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&buf, c); err != nil {
			return ""
		}
	}
	return strings.TrimSpace(buf.String())
}
