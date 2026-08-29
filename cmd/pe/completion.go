package main

import (
	"errors"
	"fmt"
	"strings"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/store"
)

// `pe completion <shell>` 打印补全脚本。
//
// 手写而不是让某个 CLI 框架生成，是因为这里能补的东西比框架知道的多：
// 设备编号、doctor 的检查项名字、service 的动作——这些都要查当前状态才知道，
// 框架生成的那份只会补命令名。
//
//	pe completion bash >> ~/.bashrc          # 或者放进 /etc/bash_completion.d/
//	pe completion zsh  > ~/.zsh/_pe          # 目录要在 $fpath 里
//	pe completion fish > ~/.config/fish/completions/pe.fish

// topLevel 是所有顶层命令。改名之后的旧名字（watch / hook-install）
// 刻意不放进来：它们仍然能用，但不该再被补出来教给新的人。
var topLevel = []string{
	"setup", "serve", "status", "doctor", "reload",
	"source", "service", "client", "pair", "device",
	"push", "agent", "mcp", "feedback", "token",
	"upgrade", "completion", "version", "help",
}

// subCommands 是二级命令。补全的价值大半在这里——
// 「pe device 后面能跟什么」比「pe 后面能跟什么」更难记。
var subCommands = map[string][]string{
	"source":   {"add", "list", "rm"},
	"service":  {"install", "uninstall", "start", "stop", "restart", "status", "logs"},
	"client":   {"set", "show"},
	"device":   {"list", "revoke"},
	"agent":    {"install", "status"},
	"token":    {"rotate"},
	"feedback": {"list", "show", "fix", "wontfix", "reopen", "rm", "export"},
	"watch":    {"add", "list", "rm"},
}

func cmdCompletion(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: pe completion bash|zsh|fish")
	}
	switch args[0] {
	case "bash":
		fmt.Print(bashCompletion())
	case "zsh":
		fmt.Print(zshCompletion())
	case "fish":
		fmt.Print(fishCompletion())
	case "__complete":
		// 内部用：补全脚本回头问我们「这个位置能填什么」。
		// 放在程序里而不是脚本里，是为了让 doctor 的检查项这类
		// 会变的东西不必在两处各维护一份。
		return dynamicComplete(args[1:])
	default:
		return fmt.Errorf("还不支持 %s。可用: bash / zsh / fish", args[0])
	}
	return nil
}

// dynamicComplete 回答「pe <cmd> <sub> 之后能填什么」。
func dynamicComplete(args []string) error {
	out := []string{}
	switch {
	case len(args) == 0:
		out = topLevel
	case len(args) == 1:
		if subs, ok := subCommands[args[0]]; ok {
			out = subs
		} else {
			out = topLevel
		}
	case args[0] == "doctor" && args[1] == "--run":
		for _, c := range allChecks() {
			out = append(out, c.name)
		}
	case args[0] == "device" && args[1] == "revoke":
		// 设备编号。查不到就什么都不补——补一个错的比不补更糟。
		out = deviceIDs()
	case args[0] == "completion":
		out = []string{"bash", "zsh", "fish"}
	}
	fmt.Println(strings.Join(out, " "))
	return nil
}

func deviceIDs() []string {
	// 这里刻意吞掉所有错误：补全跑在你敲命令的过程中，
	// 任何一句报错都会糊在提示符上。答不上来就答不上来。
	defer func() { _ = recover() }()
	st, err := store.Open(config.DefaultDataDir())
	if err != nil {
		return nil
	}
	defer st.Close()
	devices, err := st.ListDevices()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(devices))
	for _, d := range devices {
		out = append(out, fmt.Sprint(d.ID))
	}
	return out
}

func bashCompletion() string {
	return `# PreviewEverywhere 的 bash 补全
# 装它：pe completion bash >> ~/.bashrc
_pe_complete() {
  local cur prev words
  cur="${COMP_WORDS[COMP_CWORD]}"
  # 把已经敲下的命令词交给 pe 自己去想能填什么。
  local ctx=("${COMP_WORDS[@]:1:COMP_CWORD-1}")
  local opts
  opts=$(pe completion __complete "${ctx[@]}" 2>/dev/null)
  COMPREPLY=($(compgen -W "${opts}" -- "${cur}"))
  # 补不出东西时退回补文件名 —— pe source add 后面跟的就是路径。
  if [ ${#COMPREPLY[@]} -eq 0 ]; then
    COMPREPLY=($(compgen -f -- "${cur}"))
  fi
}
complete -F _pe_complete pe
`
}

func zshCompletion() string {
	return `#compdef pe
# PreviewEverywhere 的 zsh 补全
# 装它：pe completion zsh > ~/.zsh/_pe   （那个目录要在 $fpath 里）
_pe() {
  local -a opts
  local ctx
  ctx=(${words[2,$((CURRENT-1))]})
  opts=(${(f)"$(pe completion __complete $ctx 2>/dev/null)"})
  opts=(${=opts})
  if (( ${#opts} )); then
    _describe 'pe' opts && return
  fi
  _files
}
_pe "$@"
`
}

func fishCompletion() string {
	return `# PreviewEverywhere 的 fish 补全
# 装它：pe completion fish > ~/.config/fish/completions/pe.fish
function __pe_complete
    set -l tokens (commandline -opc)
    set -e tokens[1]
    pe completion __complete $tokens 2>/dev/null | string split ' '
end
complete -c pe -f -a '(__pe_complete)'
# 补不出命令时补路径（pe source add 后面跟的是目录）
complete -c pe -n '__fish_seen_subcommand_from source watch push' -F
`
}
