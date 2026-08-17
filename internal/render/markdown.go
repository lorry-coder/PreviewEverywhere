package render

import (
	"bytes"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

var (
	mdOnce sync.Once
	md     goldmark.Markdown
)

// markdownEngine 刻意不挂 goldmark-highlighting：代码高亮统一放到块处理阶段
// 用 chroma 直接做。那里能同时拿到语言和原始源码，才能把 ```mermaid 单独拎出来
// 交给前端渲染，而不是被当成普通代码块高亮掉。
func markdownEngine() goldmark.Markdown {
	mdOnce.Do(func() {
		md = goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,      // 表格、删除线、任务列表、自动链接
				extension.Footnote, // agent 写报告常用脚注
				extension.DefinitionList,
				// 刻意不启用 Typographer：它会把正文里的 --flag 变成 –flag，
				// 而 agent 的运行记录里满是没加反引号的命令行参数。
			),
			goldmark.WithParserOptions(
				parser.WithAutoHeadingID(),
				parser.WithAttribute(),
			),
			goldmark.WithRendererOptions(
				ghtml.WithUnsafe(), // 允许内嵌 HTML（<details> 等），随后由 bluemonday 净化
			),
		)
	})
	return md
}

func renderMarkdown(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := markdownEngine().Convert(src, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
