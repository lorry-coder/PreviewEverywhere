//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// reloadSignals 是「重读配置」的信号。SIGHUP 是守护进程的老规矩，
// systemd 的 ExecReload= 也是走它。
func reloadSignals() []os.Signal { return []os.Signal{syscall.SIGHUP} }

// processAlive 用 signal 0 探活：不真的送信号，只做权限与存在性检查。
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// signalReload 通知运行中的服务重读配置。
func signalReload(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("给 pid %d 发 SIGHUP 失败: %w", pid, err)
	}
	return nil
}
