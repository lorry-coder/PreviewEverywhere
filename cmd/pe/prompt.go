package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// 交互式提问的底座。
//
// 只有三个函数，刻意不引入任何 TUI 库：向导一共问三个问题，
// 为它背一套全屏渲染框架不划算，而且全屏 TUI 在 ssh、CI 和
// `curl | sh` 这些场景里恰恰最容易出问题。
//
// 一条硬规矩贯穿全部：**不是终端就绝不提问**。
// 管道里的 stdin 读到的是 EOF，把 EOF 当成「用户选了默认」是对的，
// 但必须把选了什么打出来——否则脚本跑完你不知道它替你决定了什么。

// interactive 说现在能不能问问题。
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

var stdin = bufio.NewReader(os.Stdin)

// ask 问一个带默认值的问题。回车即默认。
func ask(question, def string) string {
	if !interactive() {
		if def != "" {
			fmt.Printf("  ? %s › %s（非交互，用默认值）\n", question, def)
		}
		return def
	}
	if def != "" {
		fmt.Printf("  ? %s [%s] › ", question, def)
	} else {
		fmt.Printf("  ? %s › ", question)
	}
	line, err := stdin.ReadString('\n')
	if err != nil {
		fmt.Println()
		return def
	}
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return def
}

// confirm 问一个是非题。
func confirm(question string, def bool) bool {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	if !interactive() {
		fmt.Printf("  ? %s › %s（非交互，用默认值）\n", question, yesNo(def))
		return def
	}
	for {
		fmt.Printf("  ? %s [%s] › ", question, hint)
		line, err := stdin.ReadString('\n')
		if err != nil {
			fmt.Println()
			return def
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def
		case "y", "yes", "是":
			return true
		case "n", "no", "否":
			return false
		}
		// 打错了就再问一遍，而不是当成「默认」——「是」和「否」的
		// 后果差别很大（装不装服务、改不改 agent 配置），不该靠猜。
		fmt.Println("    请回答 y 或 n。")
	}
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}
