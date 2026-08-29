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

// `pe setup` 是首次配置的唯一入口。
//
// 在这之前，把它从零跑起来要八条命令、两个前置工具链、一次手工搬口令、
// 两段要粘的文件（客户端配置和 systemd unit）。更糟的是中间任何一步做漏了
// 都不会报错——hook 的设计原则是绝不打断 agent，配漏了它只会静默跳过。
//
// 所以这里问三个问题，剩下的自己做完。每一步都对应一条能单独重跑的命令
// （pe source add / pe service install / pe agent install / pe client set），
// 向导只是把它们串起来——这样写脚本的人不必被迫走交互。
//
// 重复跑是安全的：已经配好的部分会认出来并跳过，不会重复添加或覆盖。

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	bind := fs.String("bind", "", "监听地址，默认 0.0.0.0:8080")
	dir := fs.String("dir", "", "要监听的目录，跳过对应的提问")
	wantService := fs.Bool("service", false, "装成开机自启，跳过对应的提问")
	wantAgent := fs.Bool("agent", false, "接进 Claude Code，跳过对应的提问")
	yes := fs.Bool("yes", false, "不提问。可选步骤要用 --service / --agent 显式打开")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(absData)
	if err != nil {
		return err
	}
	if *bind != "" {
		cfg.Bind = *bind
	}

	// 不是终端就等同于 --yes：管道里 confirm 会直接返回默认值，
	// 而「装系统服务」「改 agent 配置」这两件事默认是「是」——
	// 在脚本里替人做了这些是不能接受的。要做就显式用 --service / --agent。
	noPrompt := *yes || !interactive()

	first := cfg.TokenHash == ""
	fmt.Println()
	if first {
		fmt.Println("  PreviewEverywhere · 首次配置")
	} else {
		fmt.Println("  PreviewEverywhere · 重新配置（已有的部分会跳过）")
	}
	fmt.Printf("  数据目录 %s\n", absData)
	fmt.Println()

	// ── ① 盯哪儿 ──────────────────────────────────────────────────
	if err := setupWatch(cfg, *dir, noPrompt); err != nil {
		return err
	}

	// ── ② 口令 ────────────────────────────────────────────────────
	token, err := setupToken(cfg, noPrompt)
	if err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	// ── ③ 开机自启 ────────────────────────────────────────────────
	installed := false
	if *wantService || (!noPrompt && confirm("开机自启（装成用户服务）", true)) {
		fmt.Println()
		if err := serviceInstall([]string{"--data", absData}); err != nil {
			fmt.Printf("  ✗ 装服务失败：%v\n", err)
			fmt.Println("    不影响别的，手动起也行：pe serve")
		} else {
			installed = true
		}
	}

	// ── ④ 接进 agent ──────────────────────────────────────────────
	// 客户端配置无论如何都写：pe push 要用它，而它只需要地址和口令，
	// 和装不装 hook 无关。口令拿不到时（重跑且没换口令）才跳过。
	fmt.Println()
	if token != "" {
		if err := setupClient(cfg, token); err != nil {
			fmt.Printf("  ✗ 写客户端配置失败：%v\n", err)
		}
	} else {
		fmt.Println("  · 没有口令原文，跳过客户端配置")
		fmt.Println("    需要的话：pe token rotate 换一个新的，再 pe client set")
	}

	if *wantAgent || (!noPrompt && confirm("接进 Claude Code（agent 写的文档自动进来）", true)) {
		fmt.Println()
		if err := cmdHookInstall([]string{"--write"}); err != nil {
			fmt.Printf("  ✗ 装 hook 失败：%v\n", err)
		}
	}

	// ── ⑤ 怎么进去 ────────────────────────────────────────────────
	printSetupDone(cfg, token, installed)
	return nil
}

// setupWatch 问「盯哪个目录」。
//
// 允许留空：装了 hook 之后目录这件事根本不存在，agent 写在哪都收得到。
// 所以这里不能逼人填一个——填一个错的比不填更糟。
func setupWatch(cfg *config.Config, dir string, yes bool) error {
	if dir == "" && len(cfg.Watch) > 0 {
		fmt.Printf("  · 已经在监听 %d 条规则，跳过\n", len(cfg.Watch))
		for _, w := range cfg.Watch {
			fmt.Printf("      %s\n", w.Path)
		}
		return nil
	}
	if dir == "" && !yes {
		dir = ask("盯哪个目录（留空就只接受推送）", guessWatchDir())
	}
	if strings.TrimSpace(dir) == "" {
		fmt.Println("  · 不监听目录，只接受推送（hook / pe push / MCP）")
		return nil
	}

	target := dir
	if !strings.ContainsAny(target, "*?[") {
		abs, err := filepath.Abs(expandHome(target))
		if err != nil {
			return err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return fmt.Errorf("%s 不是一个存在的目录", dir)
		}
		target = abs
	}
	cfg.AddWatch(config.Watch{Path: target})
	fmt.Printf("  ✓ 监听 %s\n", target)

	// 当场数一遍。inotify 句柄不够是 Linux 上最容易踩、又最难察觉的问题：
	// 症状是「新文档有时候不进来」，且没有任何报错。
	if n, ok := ingest.CountWatchDirs(target, cfg.Ignore); ok {
		budget := ingest.WatchBudget()
		fmt.Printf("    %d 个子目录（已扣除忽略规则），本机预算 %d\n", n, budget)
		if n > budget {
			fmt.Println("    ⚠ 超预算，部分目录不会被监听。提高上限：")
			fmt.Println("      sudo sysctl -w fs.inotify.max_user_watches=524288")
		}
	}
	return nil
}

// guessWatchDir 猜一个默认值。猜错没关系，人可以改；
// 但给一个具体的候选，比让人对着空白提示符想「该填什么」有用得多。
func guessWatchDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"Code", "code", "Projects", "projects", "src", "工作"} {
		p := filepath.Join(home, name)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return filepath.Join("~", name, "*")
		}
	}
	return ""
}

// setupToken 返回口令原文；沿用旧口令时返回空串。
//
// 返回空串是有意义的信息：口令只存 sha256，沿用就意味着我们**拿不到原文**，
// 因此写不了客户端配置。调用方据此如实说明，而不是写一份空口令进去。
func setupToken(cfg *config.Config, yes bool) (string, error) {
	if cfg.TokenHash == "" {
		token, err := cfg.NewToken()
		if err != nil {
			return "", err
		}
		fmt.Println("  ✓ 已生成访问口令")
		return token, nil
	}

	fmt.Println("  · 已有访问口令")
	if yes || !confirm("换一个新的（所有设备要重新扫码）", false) {
		return "", nil
	}
	token, err := cfg.NewToken()
	if err != nil {
		return "", err
	}
	fmt.Println("  ✓ 已换成新口令")
	return token, nil
}

// setupClient 把地址和口令写进客户端配置。
//
// 地址用回环而不是局域网 IP：同机使用是默认情形，而局域网 IP 会变
// （换 Wi-Fi、DHCP 续租），写死一个会变的地址是给以后埋雷。
// 真要跨机器，那台机器上单独跑 `pe client set --endpoint …`。
func setupClient(cfg *config.Config, token string) error {
	port := "8080"
	if i := strings.LastIndex(cfg.Bind, ":"); i >= 0 {
		port = cfg.Bind[i+1:]
	}
	c := &config.Client{Endpoint: "http://127.0.0.1:" + port, Token: token}
	if err := config.SaveClient(c); err != nil {
		return err
	}
	fmt.Printf("  ✓ 客户端配置已写入 %s\n", clientConfigPath())
	return nil
}

func printSetupDone(cfg *config.Config, token string, serviceInstalled bool) {
	port := "8080"
	if i := strings.LastIndex(cfg.Bind, ":"); i >= 0 {
		port = cfg.Bind[i+1:]
	}

	fmt.Println()
	fmt.Println("  ────────────────────────────────────────")
	fmt.Println()

	if serviceInstalled {
		fmt.Println("  服务已经在跑了。手机连同一个 Wi-Fi，打开：")
	} else {
		fmt.Println("  还差一步 —— 起服务：")
		fmt.Println()
		fmt.Println("    pe serve")
		fmt.Println()
		fmt.Println("  起来之后手机连同一个 Wi-Fi，打开：")
	}
	fmt.Println()
	for _, ip := range lanIPs() {
		fmt.Printf("    http://%s:%s\n", ip, port)
	}
	fmt.Println()

	if token == "" {
		fmt.Println("  用原来的口令登录。忘了就 `pe token rotate` 换一个。")
		fmt.Println()
		return
	}

	// 口令一定要在这里打出来。它只存 sha256，这是**唯一**一次能看到原文的机会——
	// 之前这里只在装了服务时才打印，于是不装服务的人刚生成的口令当场就丢了，
	// 而 `pe serve` 看见 token_hash 非空只会说「已有访问口令」，帮不上忙。
	fmt.Println("  扫码登录（有效期一年）：")
	fmt.Println()
	qrterminal.GenerateHalfBlock(
		fmt.Sprintf("http://%s:%s/#t=%s", firstLANIP(), port, token),
		qrterminal.L, os.Stdout)
	fmt.Println()
	fmt.Printf("  口令: %s\n", token)
	fmt.Println("  这串东西只显示这一次。客户端配置里已经写了一份，")
	fmt.Println("  要看：pe client show --reveal")
	fmt.Println()
}
