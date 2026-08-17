package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdp/qrterminal/v3"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/ingest"
)

func cmdWatch(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: pe watch add|list|rm …")
	}
	switch args[0] {
	case "add":
		return watchAdd(args[1:])
	case "list":
		return watchList(args[1:])
	case "rm", "remove":
		return watchRemove(args[1:])
	default:
		return fmt.Errorf("未知子命令 %q，可用: add / list / rm", args[0])
	}
}

func watchAdd(args []string) error {
	fs := flag.NewFlagSet("watch add", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	project := fs.String("project", "", "固定项目名；留空则自动判定（.pe.toml → .git → 目录名）")
	var include stringList
	fs.Var(&include, "include", "文件名 glob，可重复；默认 *.md *.markdown *.html *.htm")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("用法: pe watch add <目录> [--project 名称] [--include '*.md']")
	}

	target := positional[0]
	// glob 原样保留（运行时展开，新出现的匹配目录才能被纳入），
	// 非 glob 转成绝对路径，避免配置依赖当前工作目录。
	if !strings.ContainsAny(target, "*?[") {
		abs, err := filepath.Abs(expandHome(target))
		if err != nil {
			return err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return fmt.Errorf("%s 不是一个存在的目录", target)
		}
		target = abs
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}
	cfg.AddWatch(config.Watch{Path: target, Project: *project, Include: include})
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Printf("已添加监听: %s\n", target)
	fmt.Println("重启 pe serve 后生效。")
	// 与其笼统地劝「别监听仓库根」，不如当场数一遍再说。
	// 现代机器的 inotify 配额通常远比想象中宽裕，
	// 真正该拦的是那种把构建产物也算进来的目录。
	if n, ok := ingest.CountWatchDirs(target, cfg.Ignore); ok {
		budget := ingest.WatchBudget()
		fmt.Printf("需监听约 %d 个子目录（已扣除忽略规则），本机预算 %d。\n", n, budget)
		if n > budget {
			fmt.Println()
			fmt.Println("⚠ 超出预算，部分子目录不会被监听，表现是「新文档有时候不进来」且没有报错。")
			fmt.Println("  办法二选一：把体积大的子目录名加进 pe.toml 的 ignore；")
			fmt.Println("  或提高内核上限 sudo sysctl -w fs.inotify.max_user_watches=524288")
		}
	}
	return nil
}

func watchList(args []string) error {
	fs := flag.NewFlagSet("watch list", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}
	if len(cfg.Watch) == 0 {
		fmt.Println("还没有配置监听目录。用 `pe watch add <目录>` 添加。")
		return nil
	}
	for _, w := range cfg.Watch {
		line := w.Path
		if w.Project != "" {
			line += "  → 项目 " + w.Project
		}
		if len(w.Include) > 0 {
			line += "  [" + strings.Join(w.Include, " ") + "]"
		}
		fmt.Println(line)
	}
	return nil
}

func watchRemove(args []string) error {
	fs := flag.NewFlagSet("watch rm", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("用法: pe watch rm <目录>")
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}

	target := positional[0]
	if abs, err := filepath.Abs(target); err == nil && !strings.ContainsAny(target, "*?[") {
		target = abs
	}
	kept := cfg.Watch[:0]
	removed := false
	for _, w := range cfg.Watch {
		if w.Path == target {
			removed = true
			continue
		}
		kept = append(kept, w)
	}
	cfg.Watch = kept
	if !removed {
		return fmt.Errorf("配置里没有 %s，用 `pe watch list` 看看现有的", target)
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("已移除监听: %s\n", target)
	return nil
}

func cmdToken(args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	port := fs.String("port", "", "二维码里使用的端口，默认取配置")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}
	token, err := cfg.NewToken()
	if err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	p := *port
	if p == "" {
		p = cfg.Bind
		if i := strings.LastIndex(p, ":"); i >= 0 {
			p = p[i+1:]
		}
	}

	fmt.Println()
	fmt.Println("  新的访问口令已生成。扫码登录：")
	fmt.Println()
	qrterminal.GenerateHalfBlock(
		fmt.Sprintf("http://%s:%s/#t=%s", firstLANIP(), p, token),
		qrterminal.L, os.Stdout)
	fmt.Println()
	fmt.Printf("  口令: %s\n\n", token)
	// 服务端启动时把口令哈希读进了内存，不会重读配置文件。
	// 不说清楚的话，人会拿着新口令怎么试都是 401。
	fmt.Println("  注意: pe serve 若正在运行，需要重启后新口令才生效（旧 Cookie 同时失效）。")
	return nil
}

// expandHome 展开开头的 ~。加引号的路径 shell 不会替我们展开，
// 而带引号恰恰是写 glob 时的必需写法，两种写法的行为应当一致。
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
