package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// 运行状态文件：数据目录下的 runtime.json。
//
// 存在的理由是别的命令需要回答「服务在不在跑、跑在哪个端口上」。
// 在这之前没有任何办法知道，`pe status` 只能靠猜，`pe reload` 更是无从下手。
//
// 刻意不叫 pe.pid：里面除了 pid 还有端口和版本，而它们恰恰是排查时
// 最先要问的两件事（「你连的是哪个端口」「你跑的是哪个构建」）。

type runtimeInfo struct {
	PID     int    `json:"pid"`
	Bind    string `json:"bind"`
	Version string `json:"version"`
	Started string `json:"started"`
}

func runtimePath(dataDir string) string { return filepath.Join(dataDir, "runtime.json") }

func writeRuntime(dataDir, bind string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(runtimeInfo{
		PID:     os.Getpid(),
		Bind:    bind,
		Version: version,
		Started: time.Now().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(runtimePath(dataDir), append(data, '\n'), 0o600)
}

func removeRuntime(dataDir string) {
	os.Remove(runtimePath(dataDir)) //nolint:errcheck // 退出时的清理，失败也没什么可做的
}

// readRuntime 读运行状态，并核实那个进程真的还在。
//
// 核实这一步不能省：进程被 kill -9 或者机器掉电时，文件会留在原地。
// 一个说「在跑」的陈旧文件比没有文件更糟——它会让 `pe reload` 把信号
// 发给一个刚好复用了同一个 pid 的无关进程。
func readRuntime(dataDir string) (*runtimeInfo, error) {
	data, err := os.ReadFile(runtimePath(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var info runtimeInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", runtimePath(dataDir), err)
	}
	if info.PID <= 0 || !processAlive(info.PID) {
		return nil, nil
	}
	return &info, nil
}
