package render

import (
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

var (
	policyOnce sync.Once
	policy     *bluemonday.Policy
)

// Policy 是 Markdown 与 agent HTML 共用的净化策略。
//
// 净化发生在块 ID 注入之前：先把不可信内容洗干净，再往上加我们自己的属性，
// 这样 data-blk 不会被策略吃掉，也不必给策略开 data-* 的口子。
func Policy() *bluemonday.Policy {
	policyOnce.Do(func() {
		p := bluemonday.UGCPolicy()

		// 代码高亮与 mermaid 都靠 class 识别，必须放行。
		p.AllowAttrs("class").OnElements(
			"pre", "code", "span", "div", "p", "table", "thead", "tbody", "tr",
			"td", "th", "ul", "ol", "li", "section", "sup", "sub", "blockquote",
			"h1", "h2", "h3", "h4", "h5", "h6", "img", "a")
		// 标题锚点与脚注互链依赖 id。
		p.AllowAttrs("id").Globally()
		p.AllowAttrs("align").OnElements("td", "th", "table")
		p.AllowAttrs("colspan", "rowspan").OnElements("td", "th")
		p.AllowAttrs("checked", "disabled", "type").OnElements("input") // GFM 任务列表
		p.AllowElements("input")
		p.AllowElements("details", "summary", "figure", "figcaption",
			"mark", "kbd", "abbr", "del", "ins", "sub", "sup", "hr", "br", "dl", "dt", "dd")

		// 相对路径必须活到块处理阶段，图片引用要在那里被改写成 /api/v1/asset/<sha>。
		p.AllowRelativeURLs(true)
		p.RequireNoFollowOnLinks(false)
		p.AddTargetBlankToFullyQualifiedLinks(true)

		policy = p
	})
	return policy
}
