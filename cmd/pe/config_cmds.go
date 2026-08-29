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
	"previeweverywhere/internal/store"
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
	// 运行中的服务会自己发现这次改动（它每两秒看一眼 pe.toml），
	// 所以这里不再让人去重启。要立刻确认生效就 `pe reload`。
	fmt.Println("运行中的服务几秒内自动生效，不用重启。")
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

// cmdToken 换主口令。
//
// 有了 `pe pair` 之后，这条命令从「日常操作」降级成了应急操作：
// 加一台设备用配对码，只有「我怀疑主口令泄露了」才需要换它。
// 所以子命令名叫 rotate，让这层意思写在命令本身上而不是藏在文档里。
func cmdToken(args []string) error {
	// `pe token rotate` 与老写法 `pe token` 等价。老写法保留：
	// 它出现在已有的文档和别人的笔记里，让它突然报错没有任何好处。
	if len(args) > 0 && args[0] == "rotate" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	port := fs.String("port", "", "二维码里使用的端口，默认取配置")
	revokeAll := fs.Bool("revoke-devices", false, "顺带撤掉所有配对过的设备")
	if _, err := parseFlags(fs, args); err != nil {
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

	// 换口令的连带后果要写在紧跟着的地方，而不是让人去手册里找。
	// 而且现在的后果比以前小：配对过的设备各有各的会话，不受影响。
	fmt.Println("  运行中的服务几秒内自动切到新口令，不用重启。")
	fmt.Println("  失效的是：pe push / hook / MCP（重配一下：pe client set），")
	fmt.Println("  以及配对机制之前留下的旧浏览器登录。")

	if *revokeAll {
		st, err := store.Open(*dataDir)
		if err != nil {
			return err
		}
		defer st.Close()
		n, err := st.RevokeAllDevices()
		if err != nil {
			return err
		}
		fmt.Printf("  另外按你的要求撤掉了 %d 台配对过的设备。\n", n)
		return nil
	}

	if st, err := store.Open(*dataDir); err == nil {
		defer st.Close()
		if devices, err := st.ListDevices(); err == nil && len(devices) > 0 {
			fmt.Printf("  %d 台配对过的设备**不受影响**，照常能用。\n", len(devices))
			fmt.Println("  真要一起清掉：pe token rotate --revoke-devices")
		}
	}
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
