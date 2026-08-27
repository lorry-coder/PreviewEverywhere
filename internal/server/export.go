package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"previeweverywhere/internal/store"
)

// 「下载原始文件」这条路。
//
// 一句话说清它为什么不是「把 raw_blob 发出去」那么简单：文档里写的是
// ![](./img/arch.png)，而图片入库时按内容哈希存成了 blob，渲染时 src 就被
// 换成了 /api/v1/asset/<sha>。只发原文，对方打开就是一堆裂图。
//
// 所以带图的文档打包成 zip：md 一字未改，图片按原始相对路径放回去。

// exportedDoc 是打包一篇文档所需的一切。
type exportedDoc struct {
	name   string // 不含扩展名的文件名
	ext    string // 原文的扩展名，如 .md
	raw    []byte
	assets []store.AssetRef
	// paired 表示图片映射是不是可靠。老文档没有记录映射，
	// 只能按次序配对；配不上时这里是 false，zip 里会附一份说明。
	paired bool
	// refCount 是文档里一共引用了多少张本地图片。
	refCount int
}

func (s *Server) handleDownloadDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	detail, err := s.st.GetDoc(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "文档不存在")
		return
	}
	if len(detail.Versions) == 0 {
		writeError(w, http.StatusNotFound, "这篇文档没有可下载的版本")
		return
	}
	versionID := detail.Versions[0].ID

	ex, err := s.collectExport(detail, versionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 没有图片就直接给原文——为一个纯文本文档套个 zip 是给人添麻烦。
	if ex.refCount == 0 {
		serveDownload(w, ex.name+ex.ext, mimeForExt(ex.ext), ex.raw, wantsInline(r))
		return
	}

	buf, err := s.buildZip(ex)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	serveDownload(w, ex.name+".zip", "application/zip", buf, wantsInline(r))
}

func (s *Server) collectExport(detail *store.DocDetail, versionID int64) (*exportedDoc, error) {
	raw, mimeType, err := s.st.VersionRaw(versionID)
	if err != nil {
		return nil, err
	}
	ext := ".md"
	if strings.Contains(mimeType, "html") {
		ext = ".html"
	}

	ex := &exportedDoc{name: safeFileName(detail.Title), ext: ext, raw: raw}

	refs, err := s.st.AssetRefs(versionID)
	if err != nil {
		return nil, err
	}
	if len(refs) > 0 {
		ex.assets = refs
		ex.paired = true
		ex.refCount = len(refs)
		return ex, nil
	}

	// 没有记录：可能是真没图，也可能是这一版入库时还没有 asset_ref 表。
	// 用渲染结果里的图片数来分辨，并尝试按次序配对。
	html, err := s.st.VersionHTML(versionID)
	if err != nil {
		return nil, err
	}
	shas := assetShasInOrder(html)
	ex.refCount = len(shas)
	if len(shas) == 0 {
		return ex, nil
	}

	localRefs := localImageRefs(raw, ext)
	// 硬校验：两边数量必须相等才配对。不等就不猜——一张配错的图
	// 比一张缺失的图更糟，因为它看起来是对的。
	if len(localRefs) == len(shas) {
		for i := range shas {
			ex.assets = append(ex.assets, store.AssetRef{Ord: i, Ref: localRefs[i], Sha: shas[i]})
		}
		ex.paired = true
	}
	return ex, nil
}

func (s *Server) buildZip(ex *exportedDoc) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	docEntry, err := zw.Create(ex.name + ex.ext)
	if err != nil {
		return nil, err
	}
	if _, err := docEntry.Write(ex.raw); err != nil {
		return nil, err
	}

	var notes []string
	if !ex.paired {
		notes = append(notes,
			fmt.Sprintf("这篇文档引用了 %d 张图片，但这一版入库时还没有记录", ex.refCount),
			"「文档里的路径 → 库里的图片」这层对应关系，所以无法可靠地把图片放回原位。",
			"",
			"没有把图片胡乱塞进来，是因为放错位置的图比缺失的图更糟——它看起来是对的。",
			"想要带图的完整版本，用阅读页的「导出单文件 HTML」。",
			"文档重新采集一次之后，这条限制就不存在了。")
	}
	return s.finishZip(zw, &buf, ex, notes)
}

func (s *Server) finishZip(zw *zip.Writer, buf *bytes.Buffer, ex *exportedDoc, notes []string) ([]byte, error) {
	// 图片按原始相对路径放回去，这样 md 一字未改也能正常打开。
	for _, a := range ex.assets {
		name, safe := zipPathFor(a.Ref, a.Sha)
		if !safe {
			notes = append(notes, fmt.Sprintf("图片 %s 的路径无法安全还原，已放在 %s", a.Ref, name))
		}
		f, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		data, err := s.st.ReadBlob(a.Sha)
		if err != nil {
			notes = append(notes, fmt.Sprintf("图片 %s 已经不在库里了", a.Ref))
			continue
		}
		if _, err := f.Write(data); err != nil {
			return nil, err
		}
	}

	if len(notes) > 0 {
		f, err := zw.Create("图片说明.txt")
		if err != nil {
			return nil, err
		}
		if _, err := f.Write([]byte(strings.Join(notes, "\n") + "\n")); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// zipPathFor 把原始引用变成 zip 里的安全路径。
//
// 绝对路径、盘符、以及用 ../ 逃出根目录的引用都不能原样放进压缩包——
// 解压工具照做的话会把文件写到包外面去。这类落到 _assets/ 下并在说明里注明。
func zipPathFor(ref, sha string) (string, bool) {
	clean := strings.TrimPrefix(strings.TrimPrefix(ref, "./"), "/")
	clean = path.Clean(filepath.ToSlash(clean))

	unsafe := clean == "." || clean == "/" ||
		strings.HasPrefix(clean, "../") || clean == ".." ||
		strings.HasPrefix(ref, "/") ||
		regexp.MustCompile(`^[A-Za-z]:`).MatchString(ref)
	if unsafe {
		return "_assets/" + sha + path.Ext(clean), false
	}
	return clean, true
}

var assetSrcRe = regexp.MustCompile(`/api/v1/asset/([0-9a-f]{8,64})`)

// assetShasInOrder 从渲染后的 HTML 里按出现次序取出图片的 sha。
func assetShasInOrder(html string) []string {
	m := assetSrcRe.FindAllStringSubmatch(html, -1)
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	return out
}

var mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\(\s*<?([^)\s>]+)`)
var htmlImgRe = regexp.MustCompile(`(?i)<img[^>]+src\s*=\s*["']([^"']+)["']`)

// localImageRefs 按出现次序取出文档里的本地图片引用。
//
// 只用于老文档的兜底配对——新文档在采集时就把映射记下来了。
// 这里同时认 Markdown 的 ![]() 和内嵌的 <img>，因为渲染时两者都会
// 变成 <img>，次序也是合并计数的。
func localImageRefs(raw []byte, ext string) []string {
	text := string(raw)
	type hit struct {
		pos int
		ref string
	}
	var hits []hit
	if ext != ".html" {
		for _, m := range mdImageRe.FindAllStringSubmatchIndex(text, -1) {
			hits = append(hits, hit{m[0], text[m[2]:m[3]]})
		}
	}
	for _, m := range htmlImgRe.FindAllStringSubmatchIndex(text, -1) {
		hits = append(hits, hit{m[0], text[m[2]:m[3]]})
	}
	// 按在文中的位置排序，与渲染时的遍历次序一致。
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].pos < hits[j-1].pos; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}

	out := []string{}
	for _, h := range hits {
		// 绝对地址和 data URI 不是本地图片，渲染时也不会被改写，
		// 两边都要排除，否则次序对不上。
		if isRemoteRef(h.ref) {
			continue
		}
		out = append(out, h.ref)
	}
	return out
}

func isRemoteRef(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") ||
		strings.HasPrefix(ref, "//") || strings.HasPrefix(ref, "/") ||
		strings.HasPrefix(ref, "data:")
}

func mimeForExt(ext string) string {
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

var unsafeName = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]`)

// safeFileName 把标题变成能落盘的文件名。
func safeFileName(title string) string {
	name := unsafeName.ReplaceAllString(strings.TrimSpace(title), "_")
	name = strings.Trim(name, " .")
	if name == "" {
		name = "文档"
	}
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	return name
}

// serveDownload 把响应交给浏览器。
//
// disposition 只有两种取值，而这个区别在 iOS 上是决定性的：
//
//	attachment  浏览器里正常下载到「文件」。桌面和 Safari 标签页都用它。
//	inline      让浏览器就地显示。**主屏 App 模式下必须用它**——
//	            那里带 attachment 的响应会得到一个占满屏幕的文件图标和
//	            「在……中打开」，既下载不了，也回不到 App；
//	            换成 inline，PDF 会正常渲染出来，系统分享按钮才有东西可分享。
//	            这是 WebKit 在 standalone 下的已知缺口，绕不过去，只能让开。
//
// filename* 用 RFC 5987 编码，中文标题才不会在下载时变成乱码或被截断。
func serveDownload(w http.ResponseWriter, filename, contentType string, data []byte, inline bool) {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`,
			kind, asciiFallback(filename), urlEncode(filename)))
	w.Write(data)
}

// wantsInline 判断请求方要不要就地显示。主屏 App 会带上 inline=1。
func wantsInline(r *http.Request) bool {
	return r.URL.Query().Get("inline") == "1"
}

func asciiFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 128 && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func urlEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
