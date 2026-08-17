// gencss 把 chroma 的高亮配色导出成 CSS，供前端样式表内联。
//
// 代码高亮在服务端做，但用的是 class 而不是内联样式——只有这样，
// 同一份渲染产物才能在明暗两套主题下都好看。这个小工具生成那两套 class 定义，
// 换配色时重跑一次即可：
//
//	go run ./cmd/gencss > web/src/chroma.css
//
// 输出结构对齐前端的三态主题：裸选择器是浅色；系统深色下用
// html:not([data-theme="light"]) 兜住（让手动选浅色能压过系统）；
// html[data-theme="dark"] 让手动选深色也生效。
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
)

// chroma 每条规则形如：/* Keyword */ .chroma .k { color: #abc }
var leadingComment = regexp.MustCompile(`^/\*.*?\*/\s*`)

func main() {
	light, err := css("github")
	if err != nil {
		fail(err)
	}
	dark, err := css("github-dark")
	if err != nil {
		fail(err)
	}

	fmt.Println("/* 由 `go run ./cmd/gencss` 生成，勿手改。 */")
	fmt.Println(strings.Join(rules(light, ""), "\n"))
	fmt.Println()
	fmt.Println("@media (prefers-color-scheme: dark) {")
	for _, r := range rules(dark, "html:not([data-theme=\"light\"])") {
		fmt.Println("  " + r)
	}
	fmt.Println("}")
	fmt.Println()
	fmt.Println(strings.Join(rules(dark, "html[data-theme=\"dark\"]"), "\n"))
}

func css(style string) (string, error) {
	var b strings.Builder
	err := chromahtml.New(chromahtml.WithClasses(true)).WriteCSS(&b, styles.Get(style))
	return b.String(), err
}

// rules 去掉 chroma 自带的行首注释，并按需给每条规则加上主题前缀。
func rules(raw, prefix string) []string {
	out := []string{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(leadingComment.ReplaceAllString(strings.TrimSpace(line), ""))
		if line == "" {
			continue
		}
		// 背景色由平台自己的 token 决定，不跟着高亮主题走。
		if strings.HasPrefix(line, ".bg ") || strings.HasPrefix(line, ".chroma {") {
			continue
		}
		if prefix != "" {
			line = prefix + " " + line
		}
		out = append(out, line)
	}
	return out
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
