package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"previeweverywhere/internal/anchor"
)

// blockTags 是会被分配块 ID 的元素。只有「不含其它块级后代」的块才算叶子块，
// 这样 <li><p>x</p></li> 只有 p 拿到 ID，纯文本不会被重复统计。
var blockTags = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "blockquote": true, "pre": true, "td": true, "th": true,
	"dd": true, "dt": true, "figcaption": true, "summary": true,
}

var headingLevel = map[string]int{"h1": 1, "h2": 2, "h3": 3, "h4": 4, "h5": 5, "h6": 6}

type walkState struct {
	res   *Result
	opt   Options
	plain strings.Builder
	// plainRunes 是已写入纯文本的字符数。块索引里的偏移必须按字符算
	// 而不是字节——批注锚定全程用字符偏移，混了单位就会错位。
	plainRunes int
	seen       map[string]int
}

// postProcess 是 Markdown 与 agent HTML 共用的唯一一次树遍历，一并完成：
// 代码高亮与 mermaid 提取、图片引用改写、块 ID 注入、纯文本与目录抽取。
// 之所以合成一次遍历，是因为这些结果必须严格一致——纯文本的顺序要和块 ID
// 的顺序对得上，P3 的批注锚定才有地基。
func postProcess(fragment string, opt Options, res *Result) (string, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), ctx)
	if err != nil {
		return "", fmt.Errorf("解析渲染结果失败: %w", err)
	}

	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, n := range nodes {
		root.AppendChild(n)
	}

	// 标题已经显示在页头，正文里再来一个同名 H1 是重复的。
	if res.Title == "" {
		if t := takeLeadingH1(root); t != "" {
			res.Title = t
		}
	}

	transformCode(root)

	w := &walkState{res: res, opt: opt, seen: map[string]int{}}
	w.annotate(root)

	res.Plain = strings.TrimRight(w.plain.String(), "\n")
	res.Chars = len([]rune(res.Plain))

	var buf bytes.Buffer
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&buf, c); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

// takeLeadingH1 摘掉正文开头的 H1 并返回它的文本，作为文档标题。
func takeLeadingH1(root *html.Node) string {
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if c.Type == html.ElementNode && c.Data == "h1" {
			text := normalizeSpace(textContent(c))
			root.RemoveChild(c)
			return text
		}
		return "" // 第一个实体元素不是 H1，就不动它
	}
	return ""
}

// ── 块 ID 与纯文本 ────────────────────────────────────────────────

func (w *walkState) annotate(n *html.Node) bool {
	hasBlockChild := false
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if w.annotate(c) {
			hasBlockChild = true
		}
	}
	if n.Type != html.ElementNode {
		return hasBlockChild
	}
	if n.Data == "img" {
		w.rewriteImage(n)
	}
	if !blockTags[n.Data] {
		return hasBlockChild
	}
	if hasBlockChild {
		return true // 不是叶子块，ID 留给内层
	}

	text := normalizeSpace(textContent(n))
	key := text
	if key == "" {
		// 纯图片段落也要可寻址，否则 P3 里没法给它加批注。
		if src := firstImageSrc(n); src != "" {
			key = "img:" + src
		} else {
			return true
		}
	}

	id := w.blockID(key)
	setAttr(n, "data-blk", id)
	if text != "" {
		w.res.Blocks = append(w.res.Blocks, anchor.Block{
			Blk: id, Off: w.plainRunes, Len: len([]rune(text)),
		})
		w.plain.WriteString(text)
		w.plain.WriteString("\n\n")
		w.plainRunes += len([]rune(text)) + 2
	}
	if lvl, ok := headingLevel[n.Data]; ok {
		w.res.TOC = append(w.res.TOC, Heading{Level: lvl, Text: text, Blk: id})
	}
	return true
}

// blockID 取规范化文本的 sha256 前 40 位，base32 编码成 8 个字符。
// 关键性质是「内容不变则 ID 不变」——agent 重写文档时未改动的段落 ID 一致，
// 挂在上面的批注可以零成本命中。
func (w *walkState) blockID(text string) string {
	sum := sha256.Sum256([]byte(text))
	id := strings.ToLower(base32.StdEncoding.EncodeToString(sum[:]))[:8]
	w.seen[id]++
	if n := w.seen[id]; n > 1 {
		return fmt.Sprintf("%s-%d", id, n)
	}
	return id
}

func (w *walkState) rewriteImage(n *html.Node) {
	src := getAttr(n, "src")
	if src == "" || isAbsoluteURL(src) || strings.HasPrefix(src, "data:") {
		return
	}
	// 按出现次序记下原始引用。解析出的次序就是文档里的次序，
	// 老文档没有这份记录时，兜底的顺序配对靠的正是同一个次序。
	w.res.ImageRefs = append(w.res.ImageRefs, src)

	if w.opt.AssetResolver != nil {
		if url, ok := w.opt.AssetResolver(src); ok {
			setAttr(n, "src", url)
			return
		}
	}
	// 找不到（或压根没法找）的图片保留原引用并打标，
	// 前端显示成占位块而不是一张无声的坏图。
	w.res.MissingAssets = append(w.res.MissingAssets, src)
	setAttr(n, "data-pe-missing", "1")
}

// ── 代码块：mermaid 与语法高亮 ────────────────────────────────────

var chromaFormatter = chromahtml.New(
	chromahtml.WithClasses(true),           // 用 class 而非内联样式，才能跟随明暗主题
	chromahtml.PreventSurroundingPre(true), // pre/code 由我们自己写
)

func transformCode(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		transformCode(c)
	}
	if n.Type != html.ElementNode || n.Data != "pre" {
		return
	}
	code := findChild(n, "code")
	if code == nil {
		return
	}
	lang := langFromClass(getAttr(code, "class"))
	source := textContent(code)

	if lang == "mermaid" {
		// 只有 JS 有 mermaid 实现，服务端画不了。原样留给前端。
		setAttr(n, "class", "mermaid")
		removeChildren(n)
		n.AppendChild(&html.Node{Type: html.TextNode, Data: source})
		return
	}

	highlighted, ok := highlight(source, lang)
	if !ok {
		return // 语言未知就保持纯文本，不去猜
	}
	frag, err := html.ParseFragment(strings.NewReader(highlighted),
		&html.Node{Type: html.ElementNode, Data: "code", DataAtom: atom.Code})
	if err != nil {
		return
	}
	removeChildren(code)
	for _, child := range frag {
		code.AppendChild(child)
	}
	setAttr(n, "class", strings.TrimSpace("chroma language-"+lang))
}

func highlight(source, lang string) (string, bool) {
	if lang == "" {
		return "", false
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return "", false
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, source)
	if err != nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := chromaFormatter.Format(&buf, styles.Fallback, iterator); err != nil {
		return "", false
	}
	return buf.String(), true
}

func langFromClass(class string) string {
	for _, c := range strings.Fields(class) {
		if strings.HasPrefix(c, "language-") {
			return strings.ToLower(strings.TrimPrefix(c, "language-"))
		}
	}
	return ""
}

// ── DOM 小工具 ────────────────────────────────────────────────────

// textContent 收集节点下的全部文本。
// 允许传 nil：findNode 找不到目标时返回的就是 nil，
// 调用方写成 textContent(findNode(doc, "title")) 是很自然的。
func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// normalizeSpace 折叠所有空白为单个空格并剔除零宽字符。
// 块 ID 依赖它：agent 重新生成时常常只有缩进或换行位置变了，
// 规范化之后这些差异不会让 ID 漂移。
//
// 中文有一条额外规则：两个汉字之间的换行不产生空格。Markdown 的软换行会把
// 「一段话，<换行>中间有内容。」渲染成带换行的文本，若一律折叠成空格，agent 换一次
// 折行宽度就会让整段 ID 全变——而重新折行恰恰是最常见的无意义 diff。
func normalizeSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	var last rune
	for _, r := range s {
		switch {
		case r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff':
			continue // 零宽字符：agent 输出里偶尔混入，会让块 ID 无谓地漂移
		case unicode.IsSpace(r):
			space = true
		default:
			if space && b.Len() > 0 && !(isCJK(last) && isCJK(r)) {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			last = r
		}
	}
	return b.String()
}

// isCJK 覆盖汉字、假名、谚文，以及中日韩标点与全角形式。
func isCJK(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x303F, // CJK 标点
		r >= 0xFF00 && r <= 0xFFEF: // 全角形式
		return true
	}
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func findChild(n *html.Node, tag string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			return c
		}
	}
	return nil
}

func removeChildren(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
}

func firstImageSrc(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "img" {
		return getAttr(n, "src")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if src := firstImageSrc(c); src != "" {
			return src
		}
	}
	return ""
}

func isAbsoluteURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/")
}
