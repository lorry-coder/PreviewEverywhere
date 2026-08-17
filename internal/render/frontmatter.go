package render

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

// FrontMatter 是文档头部可选的 YAML 元数据。让 agent 多写三行，
// 就免掉了后续所有手动归类工作——这是「推送方便」最廉价的一环。
type FrontMatter struct {
	Title   string     `yaml:"title"`
	Project string     `yaml:"project"`
	Tags    StringList `yaml:"tags"`
	Summary string     `yaml:"summary"`
	Run     string     `yaml:"run"`
}

// StringList 同时接受 `tags: [a, b]` 和 `tags: a, b` 两种写法。
// agent 两种都会写，宽容解析比事后纠正划算。
type StringList []string

func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	var list []string
	if err := value.Decode(&list); err == nil {
		*s = trimAll(list)
		return nil
	}
	var single string
	if err := value.Decode(&single); err != nil {
		return err
	}
	*s = trimAll(strings.Split(single, ","))
	return nil
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

var fmDelim = []byte("---")

// SplitFrontMatter 剥离开头的 YAML front-matter，返回元数据与剩余正文。
// 解析失败时不报错，把整个文件当正文处理——宁可少一点元数据，
// 也不能因为 agent 写错一个缩进就让整篇文档进不来。
func SplitFrontMatter(src []byte) (FrontMatter, []byte) {
	var fm FrontMatter

	rest := bytes.TrimLeft(src, "\ufeff \t\r\n")
	if !bytes.HasPrefix(rest, fmDelim) {
		return fm, src
	}
	// 第一行必须正好是 ---
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 || strings.TrimSpace(string(rest[:nl])) != "---" {
		return fm, src
	}
	body := rest[nl+1:]

	end := findClosingDelim(body)
	if end == nil {
		return fm, src
	}
	if err := yaml.Unmarshal(body[:end.start], &fm); err != nil {
		return FrontMatter{}, src
	}
	return fm, body[end.next:]
}

type delimPos struct{ start, next int }

func findClosingDelim(body []byte) *delimPos {
	offset := 0
	for offset < len(body) {
		lineEnd := bytes.IndexByte(body[offset:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = body[offset:]
			lineEnd = len(body) - offset - 1
		} else {
			line = body[offset : offset+lineEnd]
		}
		if trimmed := strings.TrimSpace(string(line)); trimmed == "---" || trimmed == "..." {
			return &delimPos{start: offset, next: min(offset+lineEnd+1, len(body))}
		}
		offset += lineEnd + 1
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
