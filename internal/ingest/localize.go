package ingest

import (
	"bytes"
	"encoding/base64"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// agent 生成的 HTML 常从 CDN 引 mermaid / echarts / chart.js。
// 你在地铁上没网时，那些图表全是空白——而带图表恰恰是这类文档
// 走「原样模式」的唯一理由。
//
// 所以入库时把外部依赖收进文档自身。做法是内联而不是改成本地 URL：
// 原样模式的 HTML 跑在 sandbox="allow-scripts" 的 iframe 里，
// 那是个独立的不透明源，它发出的子请求带不上 Cookie，
// 指向平台自己的 /api/v1/asset/ 只会拿到 401。内联则一个子请求都不需要。

// cdnHosts 是允许抓取的主机白名单。
//
// 这里的输入是文档内容，也就是不可信输入——不能让一句 <script src>
// 就把服务器变成任意 URL 的抓取器。白名单是唯一稳妥的做法。
var cdnHosts = map[string]bool{
	"cdn.jsdelivr.net":       true,
	"unpkg.com":              true,
	"cdnjs.cloudflare.com":   true,
	"esm.sh":                 true,
	"cdn.staticfile.org":     true,
	"registry.npmmirror.com": true,
	"lib.baomitu.com":        true,
	"fastly.jsdelivr.net":    true,
}

const (
	maxCDNBytes    = 8 << 20
	maxInlineImage = 256 << 10
	cdnTimeout     = 10 * time.Second
)

// localizeHTML 把 HTML 里的外部依赖内联进来，返回改写后的文档与内联数量。
// 任何一项失败都只是跳过它，不影响文档入库。
func (p *Pipeline) localizeHTML(raw []byte, baseDir, projectRoot string) ([]byte, int) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return raw, 0
	}

	client := p.cdnClient
	if client == nil {
		client = &http.Client{Timeout: cdnTimeout}
	}
	count := 0

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type != html.ElementNode {
			return
		}
		switch n.Data {
		case "script":
			if inlineScript(client, n) {
				count++
			}
		case "link":
			if inlineStylesheet(client, n) {
				count++
			}
		case "img":
			if p.inlineImage(n, baseDir, projectRoot) {
				count++
			}
		}
	}
	walk(doc)

	if count == 0 {
		return raw, 0
	}
	var out bytes.Buffer
	if err := html.Render(&out, doc); err != nil {
		return raw, 0
	}
	return out.Bytes(), count
}

func inlineScript(client *http.Client, n *html.Node) bool {
	src := getAttrNode(n, "src")
	body, ok := fetchCDN(client, src)
	if !ok {
		return false
	}
	delAttrNode(n, "src")
	// 保留 type=module 之类的属性，内联之后语义不变。
	replaceChildrenWithText(n, string(body))
	return true
}

func inlineStylesheet(client *http.Client, n *html.Node) bool {
	if !strings.Contains(strings.ToLower(getAttrNode(n, "rel")), "stylesheet") {
		return false
	}
	body, ok := fetchCDN(client, getAttrNode(n, "href"))
	if !ok {
		return false
	}
	// <link> 是空元素，得换成 <style>
	n.Data = "style"
	n.DataAtom = 0
	n.Attr = nil
	replaceChildrenWithText(n, string(body))
	return true
}

// inlineImage 把文档旁边的小图片转成 data: URI。
// 原样模式下相对路径解析不到平台里的资源，内联是唯一能让图片显示出来的办法。
func (p *Pipeline) inlineImage(n *html.Node, baseDir, projectRoot string) bool {
	src := getAttrNode(n, "src")
	if src == "" || isAbsoluteURL(src) || strings.HasPrefix(src, "data:") || baseDir == "" {
		return false
	}
	clean := src
	if i := strings.IndexAny(clean, "?#"); i >= 0 {
		clean = clean[:i]
	}
	if unescaped, err := url.PathUnescape(clean); err == nil {
		clean = unescaped
	}
	if !assetExts[strings.ToLower(path.Ext(clean))] {
		return false
	}

	abs := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(clean)))
	if !withinRoot(abs, projectRoot, baseDir) {
		return false
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() > maxInlineImage {
		return false
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return false
	}
	mimeType := mime.TypeByExtension(strings.ToLower(path.Ext(clean)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	setAttrNode(n, "src", "data:"+mimeType+";base64,"+base64.StdEncoding.EncodeToString(data))
	return true
}

func fetchCDN(client *http.Client, rawURL string) ([]byte, bool) {
	if rawURL == "" {
		return nil, false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !cdnHosts[strings.ToLower(u.Host)] {
		return nil, false
	}

	resp, err := client.Get(u.String())
	if err != nil {
		log.Printf("本地化 %s 失败: %v", u.Host+u.Path, err)
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCDNBytes+1))
	if err != nil || len(body) > maxCDNBytes {
		return nil, false
	}
	// 内联进 <script> 之后，正文里出现 </script> 会提前闭合标签。
	// x/net/html 渲染 script 的子节点时不会转义，所以必须自己挡掉。
	if bytes.Contains(bytes.ToLower(body), []byte("</script")) {
		return nil, false
	}
	return body, true
}

// ── DOM 小工具 ────────────────────────────────────────────────────

func getAttrNode(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttrNode(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func delAttrNode(n *html.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != key {
			out = append(out, a)
		}
	}
	n.Attr = out
}

func replaceChildrenWithText(n *html.Node, text string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: text})
}

func isAbsoluteURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/")
}
