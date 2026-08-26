// Package pdf 把一份自包含的 HTML 转成 PDF。
//
// 为什么不用系统打印：这个平台的主场是手机，而手册引导用户「加到主屏」——
// 那就是 standalone 模式，Safari 在这个模式下不实现 window.print()，
// 点了没有任何反应。所以「存为 PDF」必须由我们自己产出文件。
//
// 为什么不在服务端从 Markdown 直接生成：mermaid 图表和数学公式是在浏览器里
// 渲染的，服务端手上只有一份没执行过 JS 的 HTML，直接转出来图表只剩一段代码。
// 所以走的是「浏览器把渲染完的样子做成自包含 HTML → 服务端转 PDF」这条路，
// 和「导出单文件 HTML」共用同一套管线。
package pdf

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"

	"github.com/carlos7ags/folio/document"
	"github.com/carlos7ags/folio/html"
)

// 中文字体随二进制分发。PDF 内置的 14 种标准字体全是拉丁字体，
// 不带一份中文字体的话正文会整片变成豆腐块。
//
// 这是 Noto Sans CJK SC 裁到 GB2312 常用字的子集，3.5 MB，
// 由 scripts/make-font.py 生成，SIL OFL 1.1 许可。
//
//go:embed fonts/cjk.otf
var fontFS embed.FS

const fontPath = "fonts/cjk.otf"

// Options 控制页面尺寸。零值表示 A4。
type Options struct {
	// PageWidth/PageHeight 以点为单位（1 点 = 1/72 英寸）。
	PageWidth  float64
	PageHeight float64
}

// A4 的尺寸，以点计。
const (
	a4Width  = 595.28
	a4Height = 841.89
)

// Render 把自包含的 HTML 转成 PDF 字节。
//
// 传进来的 HTML 必须已经把资源内联成 data: URI——转换器不联网、也不读
// 本地文件（BaseFS 只提供那份内嵌字体），这既是安全边界，也是「导出件
// 必须自包含」这条原则的自然结果。
func Render(htmlSrc string, opt Options) ([]byte, error) {
	w, h := opt.PageWidth, opt.PageHeight
	if w <= 0 || h <= 0 {
		w, h = a4Width, a4Height
	}

	res, err := html.ConvertFull(htmlSrc, &html.Options{
		// BaseFS 只装着字体。文档里的图片必须是 data: URI，
		// 任何指向本地路径的引用都会在这里失败——这正是我们要的。
		BaseFS:           fontSubFS(),
		FallbackFontPath: fontPath,
		PageWidth:        w,
		PageHeight:       h,
		// 不联网。导出是离线动作，而且服务端替用户去拉外部地址
		// 等于开了一个服务端请求伪造的口子。
		AllowRemoteFetch: false,
		// 缺一张图不该让整次导出失败，警告并继续即可。
		StrictAssets: false,
	})
	if err != nil {
		return nil, fmt.Errorf("解析文档失败: %w", err)
	}

	doc := document.NewDocument(document.PageSize{Width: w, Height: h})
	// 自动书签：正文里的标题会变成 PDF 的目录，长文档翻起来方便得多。
	doc.SetAutoBookmarks(true)
	if err := doc.AddConvertResult(res); err != nil {
		return nil, fmt.Errorf("排版失败: %w", err)
	}

	var buf bytes.Buffer
	if _, err := doc.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("生成 PDF 失败: %w", err)
	}
	return buf.Bytes(), nil
}

func fontSubFS() fs.FS { return fontFS }
