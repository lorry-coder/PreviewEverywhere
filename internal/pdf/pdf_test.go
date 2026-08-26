package pdf

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/carlos7ags/folio/font"
)

// 一张 16×16 的合法 PNG。刻意用真实编码而不是手抄的 base64——
// 验证时踩过一次：手写的 base64 校验和不对，浏览器容忍而 Go 的
// image/png 严格拒绝，表现是图片静默变成 alt 文本。
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAFklEQV" +
	"R4nGPwyl9KEmIY1TCqYfhqAAC3aV4Qn9+FQwAAAABJRU5ErkJggg=="

func pageCount(pdf []byte) int {
	return len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(pdf, -1))
}

func TestRenderBasics(t *testing.T) {
	out, err := Render(`<!doctype html><html><head><meta charset="utf-8">
<title>迁移风险评估</title></head><body>
<h1>迁移风险评估</h1><p>正文里的中文必须排得出来，混排 English 与数字 123 也要对。</p>
</body></html>`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Error("产物不是 PDF")
	}
	if pageCount(out) < 1 {
		t.Error("一页都没有")
	}
	// 有中文就必须内嵌字体，否则页面上是一片豆腐块。
	// PDF-14 标准字体不需要 FontFile，出现它才说明字体真的嵌进去了。
	if !bytes.Contains(out, []byte("FontFile")) {
		t.Error("没有内嵌字体，中文会变成豆腐块")
	}
}

// 内嵌字体必须保留原始字形编号，否则页面上一个中文都不显示。
//
// 这条是踩出来的，而且踩得很深：子集化字体时，fontTools 默认会把保留下来的
// 字形重新编号（压到 0..N）。字体本身仍然自洽——查 cmap 拿到的编号、
// 取到的字宽都对——但 folio 嵌入 CFF 时按**原始编号**取字形，于是取到的
// 全是空的。表现极其迷惑：pdftotext 能把中文完整抽出来，页面上却一片空白。
//
// 「嵌了字体吗」「字体里有这个字吗」「字宽对吗」这三个问题的答案全是「是」，
// 所以那几种检查一个都抓不住。真正的判据只有一个：编号有没有被重排。
// 原始 Noto Sans CJK 有 65535 个字形，'中' 在 9544；重排之后总数会掉到
// 一万出头，'中' 会跑到几百。生成子集时必须开 retain_gids。
func TestEmbeddedFontRetainsGlyphIDs(t *testing.T) {
	data, err := fontFS.ReadFile(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	face, err := font.ParseFont(data)
	if err != nil {
		t.Fatalf("内嵌字体解析失败: %v", err)
	}

	// 常用汉字在原始 Noto Sans CJK 里的字形编号都在数千以上。
	// 重排过的子集会把它们压到几百以内。
	const minGID = 2000
	for _, r := range []rune{'中', '文', '风', '险', '评', '估'} {
		gid := face.GlyphIndex(r)
		if gid == 0 {
			t.Errorf("字体里没有 %q 的字形", r)
			continue
		}
		if gid < minGID {
			t.Errorf("%q 的字形编号是 %d，太小了——字形编号被重排过，"+
				"PDF 里会一个中文都不显示。生成子集时要开 retain_gids", r, gid)
		}
		if w := face.GlyphAdvance(gid); w <= 0 {
			t.Errorf("%q 的字形宽度是 %d", r, w)
		}
	}

	// 拉丁字母同样要在，正文里中英混排是常态。
	for _, r := range []rune{'A', 'z', '0'} {
		if face.GlyphIndex(r) == 0 {
			t.Errorf("字体里没有 %q 的字形", r)
		}
	}
}

// 空文档不该 panic，也不该产出一个坏文件。
func TestRenderDegenerate(t *testing.T) {
	for _, src := range []string{"", "<html></html>", "   ", "<p></p>"} {
		out, err := Render(src, Options{})
		if err != nil {
			continue // 明确报错也是可接受的结果
		}
		if len(out) > 0 && !bytes.HasPrefix(out, []byte("%PDF-")) {
			t.Errorf("输入 %q 产出了非 PDF 内容", src)
		}
	}
}

// data: URI 的图片必须被真的画进去。
func TestRenderInlineImage(t *testing.T) {
	src := `<!doctype html><html><head><meta charset="utf-8"></head><body>
<p>下面是内联图片</p><img src="data:image/png;base64,` + tinyPNG + `" alt="图">
</body></html>`
	out, err := Render(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 画进去的图会成为 XObject 类型的图像资源。
	if !bytes.Contains(out, []byte("/Image")) {
		t.Error("内联图片没有进入 PDF")
	}
}

// 不许联网。服务端替用户去拉外部地址，等于开了一个服务端请求伪造的口子；
// 而且导出本来就该是离线动作。
func TestRenderDoesNotFetchRemote(t *testing.T) {
	src := `<!doctype html><html><head><meta charset="utf-8"></head><body>
<img src="http://127.0.0.1:1/should-not-be-fetched.png" alt="远程图">
</body></html>`
	// 真去连的话会因为连不上而卡住或报错；这里只要求它安静地跳过。
	out, err := Render(src, Options{})
	if err != nil {
		t.Fatalf("远程图片不该让整次导出失败: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Error("产物不是 PDF")
	}
}

// 长文档要能分页。这条曾经在打印样式上翻过车（只印出第一页）。
func TestRenderPaginates(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"></head><body>`)
	for i := 0; i < 120; i++ {
		b.WriteString("<p>这是第 ")
		b.WriteString(strings.Repeat("很长的一段正文内容，用来把文档撑到多页。", 2))
		b.WriteString("</p>")
	}
	b.WriteString(`</body></html>`)

	out, err := Render(b.String(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := pageCount(out); n < 2 {
		t.Errorf("长文档只产出了 %d 页，应当分页", n)
	}
}

// 页面尺寸可配，零值退回 A4。
func TestRenderPageSize(t *testing.T) {
	small, err := Render(`<p>甲</p>`, Options{PageWidth: 300, PageHeight: 400})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(small, []byte("300")) {
		t.Error("自定义页面尺寸没有生效")
	}
	if _, err := Render(`<p>甲</p>`, Options{PageWidth: -1, PageHeight: 0}); err != nil {
		t.Errorf("非法尺寸应当退回 A4 而不是报错: %v", err)
	}
}

func TestFontIsEmbeddedInBinary(t *testing.T) {
	data, err := fontFS.ReadFile(fontPath)
	if err != nil {
		t.Fatalf("字体没被打进二进制: %v", err)
	}
	// 3 MB 上下。太小说明子集化过头，太大说明没裁。
	if len(data) < 1<<20 || len(data) > 8<<20 {
		t.Errorf("字体大小异常: %.1f MB", float64(len(data))/1024/1024)
	}
	_ = base64.StdEncoding
}
