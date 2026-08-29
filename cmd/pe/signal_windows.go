//go:build windows

package main

import (
	"errors"
	"os"
)

// Windows 上没有 SIGHUP。配置改动仍然会被自动轮询发现（见 watchConfig），
// 所以这里缺的只是「立刻生效」这一下，不是功能本身。
func reloadSignals() []os.Signal { return nil }

// processAlive 在 Windows 上靠 OpenProcess 是否成功来判断。
// os.FindProcess 在这个平台上会真的去打开句柄，找不到就返回错误。
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release() //nolint:errcheck // 只是探活，句柄放掉即可
	return true
}

func signalReload(int) error {
	return errors.New("Windows 上没有 SIGHUP。改完配置最多等两秒它会自己生效，" +
		"要立刻生效就重启服务")
}
