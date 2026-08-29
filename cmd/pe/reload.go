package main

import (
	"flag"
	"fmt"

	"previeweverywhere/internal/config"
)

// `pe reload` 让运行中的服务立刻重读配置。
//
// 严格说它不是必需的：serve 每两秒会自己看一眼 pe.toml，改动最多两秒就生效。
// 留这条命令是为了脚本——「改完配置，确认它已经生效，再往下做」这件事
// 需要一个能等的动作，而不是一句「大概两秒后吧」。

func cmdReload(args []string) error {
	fs := flag.NewFlagSet("reload", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	info, err := readRuntime(*dataDir)
	if err != nil {
		return err
	}
	if info == nil {
		// 没在跑不算错误：配置已经写在盘上了，下次启动自然会读到。
		// 这条命令的语义是「让它生效」，而它已经生效了。
		fmt.Println("  服务没在跑。配置已经写好，下次启动就会读到。")
		return nil
	}

	if err := signalReload(info.PID); err != nil {
		return err
	}
	fmt.Printf("  ✓ 已通知 pe serve (pid %d) 重读配置\n", info.PID)
	return nil
}
