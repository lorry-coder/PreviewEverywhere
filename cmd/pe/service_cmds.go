package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kardianos/service"

	"previeweverywhere/internal/config"
)

// `pe service …` 把「照着手册粘一份 systemd unit」这件事收进程序里。
//
// 手册第七节原先是一段 20 行的 heredoc，外加一条极容易漏掉的
// `loginctl enable-linger`——漏了它不会报错，只会在你退出登录时
// 静默停掉服务，然后你在手机上发现文档不更新了，却查不出为什么。
//
// 装成**用户服务**而不是系统服务，是刻意的：
// 这个程序读的是你自己的项目目录、写的是 ~/.local/share/pe，
// 用 root 跑它会在你的目录里留下一堆 root 属主的文件。

const serviceName = "pe"

// systemdUserUnit 换掉 kardianos 自带的模板。
//
// 它自带的那份是给**系统服务**写的，用在用户服务上有两处会坏，而且都是静默的：
//
//  1. WantedBy=multi-user.target —— 用户管理器里根本没有这个 target。
//     `systemctl --user enable` 照样成功、`is-enabled` 照样说 enabled，
//     但那个 .wants 目录永远不会被拉起来，于是**重启之后服务不会自己起来**。
//     现象就是「昨天还好好的，今天手机上打不开了」，而且查不出原因。
//     用户服务要挂在 default.target 上。
//  2. RestartSec=120 是写死的，传选项覆盖不掉。崩了要等两分钟才拉起来，
//     对一个随时可能被打开的阅读服务来说太久了。
//
// 顺带去掉 EnvironmentFile=-/etc/sysconfig/pe：那是系统服务的做法，
// 用户服务读不到它，留着只是噪音。
//
// 模板变量由 kardianos 的迷你模板引擎替换，可用的名字见它的 service_systemd_linux.go。
const systemdUserUnit = `[Unit]
Description={{Description}}
ConditionFileIsExecutable={{Path | cmdEscape}}
{{range Dependencies}}{{.}}
{{end}}
[Service]
Type=simple
ExecStart={{Path | cmdEscape}}{{range Arguments}} {{. | cmd}}{{end}}
{{if WorkingDirectory}}WorkingDirectory={{WorkingDirectory | cmdEscape}}
{{end}}{{if ReloadSignal}}ExecReload=/bin/kill -{{ReloadSignal}} "$MAINPID"
{{end}}{{if Restart}}Restart={{Restart}}
{{end}}RestartSec=5
{{range EnvVars}}{{.}}
{{end}}
[Install]
WantedBy=default.target
`

func cmdService(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: pe service install|uninstall|start|stop|restart|status|logs")
	}
	action := args[0]
	rest := args[1:]

	switch action {
	case "install":
		return serviceInstall(rest)
	case "logs":
		return serviceLogs(rest)
	case "uninstall", "start", "stop", "restart", "status":
		return serviceControl(action, rest)
	default:
		return fmt.Errorf("未知子命令 %q，可用: install / uninstall / start / stop / restart / status / logs", action)
	}
}

// buildService 造出服务定义。install 之外的动作也要用它——
// kardianos 靠这份定义算出服务名和它在系统里的位置。
func buildService(dataDir, bind string) (service.Service, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("找不到自己的可执行文件路径: %w", err)
	}
	// 解开符号链接。Homebrew 装的东西 /opt/homebrew/bin/pe 是个链接，
	// 把链接写进 unit 里，以后 brew 升级换掉链接目标就可能指到不存在的地方。
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}

	args := []string{"serve", "--data", dataDir}
	if bind != "" {
		args = append(args, "--bind", bind)
	}

	return service.New(nil, &service.Config{
		Name:        serviceName,
		DisplayName: "PreviewEverywhere",
		Description: "把 agent 产出的 md / html 变成手机上读得完的东西",
		Executable:  exe,
		Arguments:   args,
		Option: service.KeyValue{
			// 用户服务：不需要 root，也不会在你的项目目录里写出 root 属主的文件。
			"UserService": true,
			// 让 `systemctl --user reload pe` 变成一次配置热重读，
			// 而不是「不支持该操作」。serve 那边接的就是 SIGHUP。
			"ReloadSignal":  "HUP",
			"Restart":       "on-failure",
			"SystemdScript": systemdUserUnit,
		},
	})
}

func serviceFlags(name string, args []string) (*service.Service, error) {
	fs := flag.NewFlagSet("service "+name, flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	bind := fs.String("bind", "", "监听地址，留空则用配置里的")
	if _, err := parseFlags(fs, args); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(*dataDir)
	if err != nil {
		return nil, err
	}
	svc, err := buildService(abs, *bind)
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

func serviceInstall(args []string) error {
	svc, err := serviceFlags("install", args)
	if err != nil {
		return err
	}

	// 先卸再装，让这条命令可以重复跑。改了 --bind 或者换了二进制位置之后
	// 直接重跑一次就行，不必先想起来要卸载。
	_ = (*svc).Uninstall()

	if err := (*svc).Install(); err != nil {
		// 装失败会留下一个写出来了但没 enable 的单元文件。
		// 不清掉的话，下一次重试报的是「Init already exists」——
		// 一条把真正的原因完全盖住的信息（实测撞到过）。
		//
		// 敢直接删是因为这个函数开头刚 Uninstall 过一次：此刻还在那儿的
		// 单元文件只可能是这一次写出来的。
		if unit := userUnitPath(); unit != "" {
			os.Remove(unit) //nolint:errcheck // 清不掉也只是回到原先那个状态
		}
		return serviceFailure("安装服务失败", err)
	}
	fmt.Println("  ✓ 已安装为用户服务")

	enableLinger()

	if err := (*svc).Start(); err != nil {
		// 装好了却起不来，多半是服务自己退了（端口被占是最常见的一种），
		// 而不是 systemd 的问题。所以这里除了通用诊断还要指向服务自己的日志。
		return serviceFailure("服务装好了但没起来", err)
	}
	fmt.Println("  ✓ 已启动")
	fmt.Println()
	fmt.Println("    pe service status    看它在不在")
	fmt.Println("    pe service logs      看日志")
	return nil
}

// enableLinger 让服务在你退出登录之后继续跑。
//
// 这是用户服务唯一的坑：不开 linger，systemd 会在你最后一个会话结束时
// 把整个用户实例连同服务一起停掉。而它不报错——你只会发现
// 「昨天还好好的，今天手机上打不开了」。
func enableLinger() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := exec.LookPath("loginctl"); err != nil {
		return
	}
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("LOGNAME")
	}
	if user == "" {
		return
	}
	if out, err := exec.Command("loginctl", "enable-linger", user).CombinedOutput(); err != nil {
		fmt.Printf("  · 没能开启 linger（退出登录后服务会停）：%s\n", strings.TrimSpace(string(out)))
		fmt.Printf("    手动开：sudo loginctl enable-linger %s\n", user)
		return
	}
	fmt.Println("  ✓ 已开启 linger（退出登录后服务继续跑）")
}

func serviceControl(action string, args []string) error {
	svc, err := serviceFlags(action, args)
	if err != nil {
		return err
	}

	switch action {
	case "status":
		st, err := (*svc).Status()
		switch {
		case errors.Is(err, service.ErrNotInstalled):
			fmt.Println("  未安装。装上：pe service install")
			return nil
		case err != nil:
			return err
		}
		switch st {
		case service.StatusRunning:
			fmt.Println("  运行中")
		case service.StatusStopped:
			fmt.Println("  已停止")
		default:
			fmt.Println("  状态未知")
		}
		return nil

	case "uninstall":
		if err := (*svc).Stop(); err != nil && !errors.Is(err, service.ErrNotInstalled) {
			// 本来就没在跑也算成功，不用打扰使用者。
			_ = err
		}
		if err := (*svc).Uninstall(); err != nil {
			return serviceFailure("卸载服务失败", err)
		}
		fmt.Println("  ✓ 已卸载。数据一个字节都没动，还在数据目录里。")
		return nil

	case "start":
		if err := (*svc).Start(); err != nil {
			return serviceFailure("启动失败", err)
		}
		fmt.Println("  ✓ 已启动")
	case "stop":
		if err := (*svc).Stop(); err != nil {
			return serviceFailure("停止失败", err)
		}
		fmt.Println("  ✓ 已停止")
	case "restart":
		if err := (*svc).Restart(); err != nil {
			return serviceFailure("重启失败", err)
		}
		fmt.Println("  ✓ 已重启")
	}
	return nil
}

// serviceLogs 直接把平台的日志命令接过来，而不是让人去背它。
//
// 认不出平台时如实说，并把该敲的命令打出来——比假装自己能做要有用。
func serviceLogs(args []string) error {
	fs := flag.NewFlagSet("service logs", flag.ExitOnError)
	follow := fs.Bool("f", true, "跟着输出（Ctrl-C 退出）")
	lines := fs.Int("n", 100, "先显示最近多少行")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("journalctl"); err != nil {
			return errors.New("这台机器上没有 journalctl。服务日志请按你的 init 系统去看")
		}
		argv := []string{"--user", "-u", serviceName, "-n", fmt.Sprint(*lines)}
		if *follow {
			argv = append(argv, "-f")
		}
		return runInherit("journalctl", argv...)

	case "darwin":
		// launchd 把 stdout/stderr 写成文件，位置见 kardianos 的默认日志目录。
		path := filepath.Join("/usr/local/var/log", serviceName+".err.log")
		if _, err := os.Stat(path); err != nil {
			path = filepath.Join("/var/log", serviceName+".err.log")
		}
		argv := []string{"-n", fmt.Sprint(*lines)}
		if *follow {
			argv = append(argv, "-f")
		}
		return runInherit("tail", append(argv, path)...)

	default:
		return fmt.Errorf("还不知道怎么在 %s 上看服务日志", runtime.GOOS)
	}
}

func runInherit(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
