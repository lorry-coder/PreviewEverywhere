package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FeedbackFileName 是数据目录下那份「打开就能看」的反馈清单。
const FeedbackFileName = "feedback.md"

// WriteFeedbackFile 把当前所有反馈全量重写成一份 Markdown。
//
// 为什么是全量重写而不是追加：这份文件是**投影**，不是第二份数据。
// 程序只写不读，所以它永远不可能和数据库漂移——增量维护才会。
// 抬头写明「改它不生效」，免得有人在文件里标了已修复却发现界面没变。
//
// 写入走「临时文件 + rename」：改状态和重写文件是同一个动作的两半，
// 中途断电时宁可看到旧的完整文件，也不要一份写了一半的。
func (s *Store) WriteFeedbackFile(dir string) error {
	list, err := s.ListFeedback("")
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# 问题反馈\n\n")
	b.WriteString("> 本文件由 PreviewEverywhere 自动生成，每次反馈变动后全量重写。\n")
	b.WriteString("> **直接改这个文件不会生效**——改状态请用 `pe feedback` 或界面上的反馈页。\n\n")
	fmt.Fprintf(&b, "生成时间：%s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	for _, g := range []string{FeedbackOpen, FeedbackFixed, FeedbackWontFix} {
		var part []Feedback
		for _, f := range list {
			if f.Status == g {
				part = append(part, f)
			}
		}
		fmt.Fprintf(&b, "## %s（%d）\n\n", FeedbackStatusLabel[g], len(part))
		if len(part) == 0 {
			b.WriteString("（无）\n\n")
			continue
		}
		for _, f := range part {
			writeFeedbackEntry(&b, f)
		}
	}

	path := filepath.Join(dir, FeedbackFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("写 %s 失败: %w", FeedbackFileName, err)
	}
	return os.Rename(tmp, path)
}

func writeFeedbackEntry(b *strings.Builder, f Feedback) {
	fmt.Fprintf(b, "### #%d　%s\n\n", f.ID, firstLine(f.Body))
	if strings.Contains(strings.TrimSpace(f.Body), "\n") {
		fmt.Fprintf(b, "%s\n\n", strings.TrimSpace(f.Body))
	}
	fmt.Fprintf(b, "- 提交时间：%s\n", time.Unix(f.CreatedAt, 0).Format("2006-01-02 15:04"))
	if f.DocTitle != "" {
		fmt.Fprintf(b, "- 当时在读：%s\n", f.DocTitle)
	}
	if f.Route != "" {
		fmt.Fprintf(b, "- 当时页面：`%s`\n", f.Route)
	}
	if f.Resolution != "" {
		fmt.Fprintf(b, "- 处理说明：%s\n", f.Resolution)
	}
	if env := formatEnv(f.Env); env != "" {
		fmt.Fprintf(b, "- 环境：%s\n", env)
	}
	b.WriteString("\n")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	// 标题里放太长的一行会把清单撑得没法扫读。
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// formatEnv 把环境快照压成一行。原始 JSON 也在库里，
// `pe feedback show` 会完整打出来；清单上只要一眼能认出设备就够了。
func formatEnv(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"build", "viewport", "pointer", "displayMode", "ua"} {
		if v, ok := m[k]; ok && v != nil && v != "" {
			s := fmt.Sprint(v)
			if k == "ua" && len([]rune(s)) > 60 {
				s = string([]rune(s)[:60]) + "…"
			}
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}
