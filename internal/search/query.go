// Package search 解析搜索框里输入的查询语法。
//
// 语法刻意做得像 GitHub / Gmail，因为那是大多数人已经会的：
//
//	迁移风险                    全文
//	tag:待复核                  标签
//	project:auth 双写           项目内全文
//	tag:风险 -tag:已解决 回滚    组合与排除
//	is:unread                   状态
//	"双写窗口期"                 短语（引号内的空格不拆词）
package search

import (
	"strings"
	"unicode"
)

type Query struct {
	Raw     string
	Terms   []string // 自由词与短语
	Tags    []string
	NotTags []string
	Project string // slug 或名称
	Kind    string // markdown | html
	Unread  bool
	Read    bool
	Later   bool
}

// Empty 表示这个查询什么也没限定，调用方应当直接返回空结果而不是全量。
func (q Query) Empty() bool {
	return len(q.Terms) == 0 && len(q.Tags) == 0 && len(q.NotTags) == 0 &&
		q.Project == "" && q.Kind == "" && !q.Unread && !q.Read && !q.Later
}

func Parse(input string) Query {
	q := Query{Raw: strings.TrimSpace(input)}

	for _, tok := range tokenize(input) {
		negated := false
		text := tok.text
		if !tok.quoted && strings.HasPrefix(text, "-") && len(text) > 1 {
			negated = true
			text = text[1:]
		}

		key, value, isField := splitField(text, tok.quoted)
		if !isField {
			if text != "" {
				q.Terms = append(q.Terms, text)
			}
			continue
		}

		switch key {
		case "tag", "标签":
			if negated {
				q.NotTags = append(q.NotTags, value)
			} else {
				q.Tags = append(q.Tags, value)
			}
		case "project", "proj", "项目":
			q.Project = value
		case "kind", "type":
			q.Kind = strings.ToLower(value)
		case "is", "状态":
			switch strings.ToLower(value) {
			case "unread", "未读":
				q.Unread = true
			case "read", "已读":
				q.Read = true
			case "later", "稍后读":
				q.Later = true
			}
		default:
			// 不认识的前缀原样当关键词，别让用户输入的冒号变成静默丢弃。
			q.Terms = append(q.Terms, text)
		}
	}
	return q
}

// splitField 判断 token 是不是 `key:value` 形式。
// 引号包起来的整体永远当关键词，这样搜 "http://example.com" 才不会被误解析。
func splitField(text string, quoted bool) (key, value string, ok bool) {
	if quoted {
		return "", "", false
	}
	i := strings.Index(text, ":")
	if i <= 0 || i == len(text)-1 {
		return "", "", false
	}
	key = strings.ToLower(text[:i])
	value = strings.Trim(text[i+1:], `"`)
	// key 必须是纯字母或中文，否则像「12:30」这种时间会被误当字段。
	for _, r := range key {
		if !unicode.IsLetter(r) {
			return "", "", false
		}
	}
	return key, value, value != ""
}

type token struct {
	text   string
	quoted bool
}

func tokenize(s string) []token {
	var out []token
	var buf strings.Builder
	inQuote := false
	quotedTok := false

	flush := func() {
		if buf.Len() > 0 {
			out = append(out, token{text: buf.String(), quoted: quotedTok})
			buf.Reset()
		}
		quotedTok = false
	}

	for _, r := range s {
		switch {
		case r == '"' || r == '“' || r == '”':
			if inQuote {
				inQuote = false
				// 形如 tag:"待 复核" 时引号只是给字段值加的，
				// 整体仍要当字段解析，不能标成纯短语。
				if !strings.HasSuffix(buf.String(), ":") {
					quotedTok = !strings.Contains(buf.String(), ":")
				}
				flush()
			} else {
				// 紧跟在 key: 后面的引号是字段值的引号，不切分 token。
				if !strings.HasSuffix(buf.String(), ":") {
					flush()
				}
				inQuote = true
			}
		case unicode.IsSpace(r) && !inQuote:
			flush()
		default:
			buf.WriteRune(r)
		}
	}
	if inQuote {
		quotedTok = true
	}
	flush()
	return out
}
