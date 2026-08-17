package search

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Query
	}{
		{"迁移风险", Query{Terms: []string{"迁移风险"}}},
		{"tag:待复核", Query{Tags: []string{"待复核"}}},
		{"project:auth 双写", Query{Project: "auth", Terms: []string{"双写"}}},
		{"tag:风险 -tag:已解决 回滚", Query{Tags: []string{"风险"}, NotTags: []string{"已解决"}, Terms: []string{"回滚"}}},
		{"is:unread", Query{Unread: true}},
		{"is:later kind:html", Query{Later: true, Kind: "html"}},
		{`"双写窗口期"`, Query{Terms: []string{"双写窗口期"}}},
		{`tag:"待 复核" 回滚`, Query{Tags: []string{"待 复核"}, Terms: []string{"回滚"}}},
		// 中文字段名也认，省得切输入法。
		{"标签:风险", Query{Tags: []string{"风险"}}},
		// 时间里的冒号不该被当成字段。
		{"12:30 的构建", Query{Terms: []string{"12:30", "的构建"}}},
		// 不认识的前缀原样当关键词，不能静默丢弃。
		{"foo:bar", Query{Terms: []string{"foo:bar"}}},
	}

	for _, c := range cases {
		got := Parse(c.in)
		got.Raw = ""
		c.want.Raw = ""
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q)\n  实得 %+v\n  期望 %+v", c.in, got, c.want)
		}
	}
}

func TestEmpty(t *testing.T) {
	if !Parse("   ").Empty() {
		t.Error("空白输入应视为空查询")
	}
	if Parse("is:unread").Empty() {
		t.Error("只有状态过滤也不算空查询")
	}
}
