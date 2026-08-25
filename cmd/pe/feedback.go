package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/store"
)

// pe feedback —— 在终端里过一遍待办。
//
// 界面上也能改状态，但修 bug 这件事本来就发生在终端里：
// 改完代码顺手 `pe feedback fix 3`，比切回手机点一下顺手得多。
func cmdFeedback(args []string) error {
	if len(args) == 0 {
		return feedbackList([]string{})
	}
	switch args[0] {
	case "list", "ls":
		return feedbackList(args[1:])
	case "show":
		return feedbackShow(args[1:])
	case "fix":
		return feedbackSet(args[1:], store.FeedbackFixed)
	case "wontfix":
		return feedbackSet(args[1:], store.FeedbackWontFix)
	case "reopen":
		return feedbackSet(args[1:], store.FeedbackOpen)
	case "rm", "remove":
		return feedbackRemove(args[1:])
	case "export":
		return feedbackExport(args[1:])
	default:
		return fmt.Errorf("未知子命令 %q，可用: list / show / fix / wontfix / reopen / rm / export", args[0])
	}
}

// openFeedbackStore 打开数据库。
//
// 注意这会和正在跑的 `pe serve` 共用同一个文件。SQLite 开了 WAL，
// 并发读写是安全的；但服务端在启动时把配置读进了内存，所以这里改的是
// 数据，不是配置——状态改完界面刷新一下就能看到，不用重启。
func openFeedbackStore(dataDir string) (*store.Store, string, error) {
	st, err := store.Open(dataDir)
	if err != nil {
		return nil, "", err
	}
	return st, dataDir, nil
}

func feedbackList(args []string) error {
	fs := flag.NewFlagSet("feedback list", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	status := fs.String("status", "open", "open | fixed | wontfix | all")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	st, _, err := openFeedbackStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	filter := *status
	if filter == "all" {
		filter = ""
	} else if !store.ValidFeedbackStatus(filter) {
		return fmt.Errorf("未知状态 %q，可用: open / fixed / wontfix / all", filter)
	}

	list, err := st.ListFeedback(filter)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		if filter == store.FeedbackOpen {
			fmt.Println("没有待修复的问题。")
		} else {
			fmt.Println("没有符合条件的反馈。")
		}
		return nil
	}

	counts, _ := st.FeedbackCounts()
	for _, f := range list {
		fmt.Printf("#%-3d [%s] %s\n", f.ID, store.FeedbackStatusLabel[f.Status], firstLine(f.Body))
		var meta []string
		meta = append(meta, time.Unix(f.CreatedAt, 0).Format("01-02 15:04"))
		if f.DocTitle != "" {
			meta = append(meta, f.DocTitle)
		}
		if f.Route != "" {
			meta = append(meta, f.Route)
		}
		fmt.Printf("     %s\n", strings.Join(meta, " · "))
		if f.Resolution != "" {
			fmt.Printf("     处理: %s\n", f.Resolution)
		}
	}
	fmt.Printf("\n待修复 %d · 已修复 %d · 无需修复 %d\n",
		counts[store.FeedbackOpen], counts[store.FeedbackFixed], counts[store.FeedbackWontFix])
	return nil
}

func feedbackShow(args []string) error {
	fs := flag.NewFlagSet("feedback show", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("用法: pe feedback show <编号>")
	}
	id, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		return fmt.Errorf("编号得是数字: %s", positional[0])
	}

	st, _, err := openFeedbackStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	f, err := st.Feedback(id)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("没有编号为 %d 的反馈", id)
	}
	if err != nil {
		return err
	}

	fmt.Printf("#%d　%s\n\n", f.ID, store.FeedbackStatusLabel[f.Status])
	fmt.Printf("%s\n\n", strings.TrimSpace(f.Body))
	fmt.Printf("提交时间: %s\n", time.Unix(f.CreatedAt, 0).Format("2006-01-02 15:04:05"))
	if f.DocTitle != "" {
		fmt.Printf("当时在读: %s", f.DocTitle)
		if f.DocID > 0 {
			fmt.Printf("（文档 #%d）", f.DocID)
		}
		fmt.Println()
	}
	if f.Route != "" {
		fmt.Printf("当时页面: %s\n", f.Route)
	}
	if f.Resolution != "" {
		fmt.Printf("处理说明: %s\n", f.Resolution)
	}
	// 环境快照完整打出来——它正是为了省掉「你那边是什么环境」这一轮问答而存在的。
	if strings.TrimSpace(f.Env) != "" {
		fmt.Println("\n环境快照:")
		var m map[string]any
		if json.Unmarshal([]byte(f.Env), &m) == nil {
			for _, k := range sortedKeys(m) {
				fmt.Printf("  %-16s %v\n", k, m[k])
			}
		} else {
			fmt.Printf("  %s\n", f.Env)
		}
	}
	return nil
}

func feedbackSet(args []string, status string) error {
	fs := flag.NewFlagSet("feedback", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	note := fs.String("note", "", "处理说明")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("用法: pe feedback %s <编号> [--note \"…\"]", statusVerb(status))
	}
	id, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		return fmt.Errorf("编号得是数字: %s", positional[0])
	}

	st, dir, err := openFeedbackStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	f, err := st.SetFeedbackStatus(id, status, *note)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("没有编号为 %d 的反馈", id)
	}
	if err != nil {
		return err
	}
	// 投影文件要跟着变，否则「打开文件就能看」会看到过期的状态。
	if err := st.WriteFeedbackFile(dir); err != nil {
		fmt.Fprintf(os.Stderr, "提示: 重写 %s 失败: %v\n", store.FeedbackFileName, err)
	}
	fmt.Printf("#%d 已标记为「%s」：%s\n", f.ID, store.FeedbackStatusLabel[f.Status], firstLine(f.Body))
	return nil
}

func feedbackRemove(args []string) error {
	fs := flag.NewFlagSet("feedback rm", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("用法: pe feedback rm <编号>")
	}
	id, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		return fmt.Errorf("编号得是数字: %s", positional[0])
	}

	st, dir, err := openFeedbackStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.DeleteFeedback(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("没有编号为 %d 的反馈", id)
		}
		return err
	}
	if err := st.WriteFeedbackFile(dir); err != nil {
		fmt.Fprintf(os.Stderr, "提示: 重写 %s 失败: %v\n", store.FeedbackFileName, err)
	}
	fmt.Printf("已删除 #%d\n", id)
	return nil
}

func feedbackExport(args []string) error {
	fs := flag.NewFlagSet("feedback export", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	format := fs.String("format", "md", "md | jsonl")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	st, dir, err := openFeedbackStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	switch *format {
	case "md":
		// 重写投影文件并告诉用户它在哪——这就是「直接读文件」那条路。
		if err := st.WriteFeedbackFile(dir); err != nil {
			return err
		}
		fmt.Printf("已写入 %s/%s\n", dir, store.FeedbackFileName)
		return nil
	case "jsonl":
		list, err := st.ListFeedback("")
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		for _, f := range list {
			if err := enc.Encode(f); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("未知格式 %q，可用: md / jsonl", *format)
	}
}

func statusVerb(status string) string {
	switch status {
	case store.FeedbackFixed:
		return "fix"
	case store.FeedbackWontFix:
		return "wontfix"
	default:
		return "reopen"
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " …"
	}
	if r := []rune(s); len(r) > 46 {
		return string(r[:46]) + "…"
	}
	return s
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// 简单排序，字段不多
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
