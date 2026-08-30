package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// 把服务管理器那句被丢掉的话找回来。
//
// kardianos/service 在 Linux 上调 systemctl 走的是它的 run()：
//
//	func run(command string, arguments ...string) error {
//		_, _, err := runCommand(command, false, arguments...)
//		return err
//	}
//
// 而 runCommand 给 stderr 接了管道，却只在 `command == "launchctl"` 那一支
// 去读它。systemctl 不在那一支里，于是管道被打开、然后随进程一起关掉，
// 里面的字一个都没人看；返回的是裸的 *exec.ExitError，Error() 就四个字：
// exit status 1。
//
// 丢掉的恰恰是最有用的那句，实测两种真实故障：
//
//	Failed to connect to bus: $DBUS_SESSION_BUS_ADDRESS and $XDG_RUNTIME_DIR not defined
//	Failed to enable unit: Unit file pe.service does not exist.
//
// 两句都直接告诉你该干什么。而这个失败出现的位置最糟——`pe setup` 的第三步，
// 一个刚装完的人在这里撞上 exit status 1，连「是哪一步失败的」都不知道。
//
// 所以自己补一次诊断。规矩有两条：
//   - **只做只读探测**，不重跑会改变状态的命令。重跑一次 enable 可能「碰巧成功」，
//     那样留下的是一个半配好的系统和一条对不上的错误信息。
//   - 查不出就老实说查不出，不猜。

// explainServiceErr 在服务操作失败后补一段「到底为什么」。
// 什么也没查出来时返回空串——由调用方决定这时候说什么。
func explainServiceErr(action string) string {
	switch runtime.GOOS {
	case "linux":
		return explainSystemd(action)
	case "darwin":
		// launchctl 那一支 kardianos 自己读了 stderr，错误信息不会丢，
		// 所以这里不需要重复做一遍。
		return ""
	default:
		return ""
	}
}

func explainSystemd(action string) string {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "这台机器上没有 systemctl。\n" +
			"不是 systemd 的系统就手动起：pe serve（或者交给你自己的进程管理器）"
	}

	// 只读探测：能不能连上用户实例的总线。
	//
	// 用 `show --property=Version` 而不是 `is-system-running`：后者在
	// degraded / starting 状态下也返回非零，会把「系统有别的服务挂了」
	// 误报成「连不上」。show 只在真的连不上时才失败。
	out, err := exec.Command("systemctl", "--user", "show", "--property=Version").CombinedOutput()
	if err != nil {
		msg := headLine(string(out))
		hint := "systemctl --user 用不了：" + msg
		if strings.Contains(msg, "Failed to connect to bus") {
			// 最常见的一种：ssh 进来但没开 linger，或者跑在容器里。
			// 这时候没有用户会话，用户实例根本没起来。
			hint += "\n\n这台机器上没有你的 systemd 用户会话。常见于两种情况：" +
				"\n  · ssh 登进来，而这个账号没开 linger" +
				"\n      解法：sudo loginctl enable-linger " + currentUser() + "，然后重新登录" +
				"\n  · 跑在容器里，里面压根没有 systemd" +
				"\n      解法：别装服务，直接 pe serve（容器本来就会替你重启它）"
		}
		return hint
	}

	// 总线是通的，那问题在别处。看看单元文件到底写出来没有——
	// 这一条能把「写文件失败」和「enable 失败」分开。
	unit := userUnitPath()
	if unit == "" {
		return ""
	}
	if _, err := os.Stat(unit); err != nil {
		return "单元文件没写出来：" + unit + "\n" +
			"那个目录写得进去吗？（磁盘满、属主不对都会这样）"
	}
	return "单元文件已经写出来了：" + unit + "\n" +
		"systemd 那边怎么说：systemctl --user status " + serviceName
}

// userUnitPath 是用户单元该在的位置，按 XDG 约定算。
// 和 kardianos 写文件的位置保持一致，不一致的话这条诊断会指到一个空处。
func userUnitPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", serviceName+".service")
}

func currentUser() string {
	for _, k := range []string{"USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "$USER"
}

// headLine 取第一行，且**不截断**。
// feedback.go 里那个 firstLine 会截到 46 字并加省略号——那是给表格用的，
// 而 systemctl 的报错恰恰是越长越有用，不能借它。
func headLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// serviceFailure 把「原始错误 + 查出来的原因」拼成一条能照着做的信息。
//
// 原始错误照样留着（exit status 1 本身没有信息量，但它证明确实是外部命令
// 失败了，而不是我们自己的逻辑出错）。
func serviceFailure(action string, err error) error {
	msg := fmt.Sprintf("%s: %v", action, err)
	if why := explainServiceErr(action); why != "" {
		msg += "\n\n" + why
	}
	return fmt.Errorf("%s", msg)
}
