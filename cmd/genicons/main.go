// genicons 生成 PWA 需要的图标。
//
//	go run ./cmd/genicons
//
// 图案就是产品本身：一张纸，中间一行被荧光笔划过。
// 深色底 + 赭石色高亮，和界面的强调色是同一套——
// 那个颜色本来就取自「划重点」这个动作。
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var (
	ink       = color.NRGBA{0x1a, 0x1c, 0x1b, 0xff} // 底色
	paper     = color.NRGBA{0xf3, 0xf3, 0xf0, 0xff} // 纸
	line      = color.NRGBA{0xc8, 0xca, 0xc4, 0xff} // 文字行
	highlight = color.NRGBA{0xc0, 0x8a, 0x22, 0xff} // 荧光笔
)

func main() {
	out := filepath.Join("web", "public")
	if err := os.MkdirAll(out, 0o755); err != nil {
		fail(err)
	}
	for _, size := range []int{192, 512} {
		write(filepath.Join(out, fmt.Sprintf("icon-%d.png", size)), icon(size, false))
	}
	// iOS 的图标会被系统自己套圆角，画满即可。
	write(filepath.Join(out, "apple-touch-icon.png"), icon(180, true))
	writeSVG(filepath.Join(out, "favicon.svg"))
	fmt.Println("图标已生成到", out)
}

func icon(size int, square bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)

	radius := s * 0.22
	if square {
		radius = 0
	}
	fillRounded(img, 0, 0, s, s, radius, ink)

	// 纸：竖着的长方形，留出四周的底色边。
	pw, ph := s*0.56, s*0.66
	px, py := (s-pw)/2, (s-ph)/2
	fillRounded(img, px, py, pw, ph, s*0.04, paper)

	// 四行「文字」，第二行被荧光笔划过。
	lineH := ph * 0.075
	gap := ph * 0.145
	top := py + ph*0.18
	widths := []float64{0.78, 0.86, 0.62, 0.72}
	for i, w := range widths {
		y := top + float64(i)*gap
		x := px + pw*0.11
		width := pw * 0.78 * w
		if i == 1 {
			// 高亮块比字行高一些、宽一些，看着才像被笔扫过；
			// 这一行的「字」要用深色，否则浅灰压在赭石上对比不足，
			// 整块会读成一个描边框而不是被划重点的文字。
			fillRect(img, x-pw*0.04, y-lineH*0.55, width+pw*0.08, lineH*2.1, highlight)
			fillRect(img, x, y, width, lineH, ink)
			continue
		}
		fillRect(img, x, y, width, lineH, line)
	}
	return img
}

// fillRect 填一个矩形，坐标用浮点方便按比例算。
func fillRect(img *image.NRGBA, x, y, w, h float64, c color.NRGBA) {
	r := image.Rect(int(math.Round(x)), int(math.Round(y)),
		int(math.Round(x+w)), int(math.Round(y+h)))
	draw.Draw(img, r.Intersect(img.Bounds()), &image.Uniform{c}, image.Point{}, draw.Over)
}

// fillRounded 画圆角矩形，边缘做一次简单的超采样抗锯齿。
func fillRounded(img *image.NRGBA, x, y, w, h, radius float64, c color.NRGBA) {
	const samples = 3
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	x1, y1 := int(math.Ceil(x+w)), int(math.Ceil(y+h))

	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			if !(image.Point{px, py}).In(img.Bounds()) {
				continue
			}
			hits := 0
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					fx := float64(px) + (float64(sx)+0.5)/samples
					fy := float64(py) + (float64(sy)+0.5)/samples
					if insideRounded(fx, fy, x, y, w, h, radius) {
						hits++
					}
				}
			}
			if hits == 0 {
				continue
			}
			alpha := float64(hits) / float64(samples*samples)
			shade := c
			shade.A = uint8(float64(c.A) * alpha)
			draw.Draw(img, image.Rect(px, py, px+1, py+1), &image.Uniform{shade}, image.Point{}, draw.Over)
		}
	}
}

func insideRounded(fx, fy, x, y, w, h, r float64) bool {
	if fx < x || fy < y || fx > x+w || fy > y+h {
		return false
	}
	if r <= 0 {
		return true
	}
	// 只有四个角需要判圆
	cx, cy := fx, fy
	switch {
	case fx < x+r && fy < y+r:
		cx, cy = x+r, y+r
	case fx > x+w-r && fy < y+r:
		cx, cy = x+w-r, y+r
	case fx < x+r && fy > y+h-r:
		cx, cy = x+r, y+h-r
	case fx > x+w-r && fy > y+h-r:
		cx, cy = x+w-r, y+h-r
	default:
		return true
	}
	dx, dy := fx-cx, fy-cy
	return dx*dx+dy*dy <= r*r
}

func writeSVG(path string) {
	const tmpl = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <rect width="64" height="64" rx="14" fill="#1a1c1b"/>
  <rect x="14" y="11" width="36" height="42" rx="3" fill="#f3f3f0"/>
  <rect x="17.5" y="22.9" width="29" height="6.2" fill="#c08a22"/>
  <rect x="19.5" y="18.6" width="21.4" height="3.2" fill="#c8cac4"/>
  <rect x="19.5" y="24.7" width="23.6" height="3.2" fill="#1a1c1b"/>
  <rect x="19.5" y="30.8" width="17" height="3.2" fill="#c8cac4"/>
  <rect x="19.5" y="36.9" width="19.8" height="3.2" fill="#c8cac4"/>
</svg>
`
	if err := os.WriteFile(path, []byte(tmpl), 0o644); err != nil {
		fail(err)
	}
}

func write(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
