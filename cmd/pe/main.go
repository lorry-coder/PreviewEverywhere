// pe 是 PreviewEverywhere 的唯一可执行文件：它同时是服务端、命令行客户端
// 和文件监听器。整个平台「scp 一个文件过去就能跑」，靠的就是这一点。
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"

	// 把时区库编进二进制。
	//
	// 时间线是按「服务端本地日期」分组的，而精简系统（scratch/distroless 容器、
	// 某些 NAS 固件）根本没有 /usr/share/zoneinfo，那里 TZ=Asia/Shanghai 会被
	// 静默忽略、退回 UTC——于是「今天/昨天」在半夜前后就会和手机上看到的对不上。
	// 内嵌之后只要设 TZ 就一定生效，代价是几百 KB。
	_ "time/tzdata"
)

// 这三个由构建时用 -ldflags -X 注入。默认值是给 `go build` 直接跑的开发构建用的——
// 它说的是实话：这个二进制不是从某个 tag 发出来的。
// errSilent 表示「以失败退出，但话已经说完了」。pe doctor 查出问题时就是这样：
// 该说的都在报告里写着，再补一句「错误: ...」只会让人以为是程序自己出了错。
var errSilent = errors.New("有检查项没通过")

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	log.SetFlags(log.Ltime)

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "setup":
		err = cmdSetup(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "reload":
		err = cmdReload(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "service":
		err = cmdService(os.Args[2:])
	case "client":
		err = cmdClient(os.Args[2:])
	case "push":
		err = cmdPush(os.Args[2:])
	case "watch":
		err = cmdWatch(os.Args[2:])
	case "token":
		err = cmdToken(os.Args[2:])
	case "pair":
		err = cmdPair(os.Args[2:])
	case "device":
		err = cmdDevice(os.Args[2:])
	case "feedback":
		err = cmdFeedback(os.Args[2:])
	case "hook-ingest":
		err = cmdHookIngest(os.Args[2:])
	case "hook-install":
		err = cmdHookInstall(os.Args[2:])
	case "mcp":
		err = cmdMCP(os.Args[2:])
	case "version", "-v", "--version":
		printVersion()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}

	// errSilent 的意思是「以失败退出，但话已经说完了」。
	// pe doctor 查出问题时就是这样：该说的都在报告里写着了，
	// 再补一句「错误: ...」只会让人以为是程序自己出了错。
	if errors.Is(err, errSilent) {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

// printVersion 除了版本号还报出 commit 与构建时间。
// 排查线上问题时「你跑的到底是哪个构建」是第一个要问的，
// 而 `pe upgrade` 之后人很容易记不清自己在哪个版本上。
func printVersion() {
	fmt.Printf("pe %s\n", version)
	fmt.Printf("  commit  %s\n", commit)
	fmt.Printf("  built   %s\n", buildDate)
	fmt.Printf("  go      %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func usage() {
	fmt.Print(`PreviewEverywhere — 把 agent 产出的文档变成手机上读得完的东西

用法:
  pe setup [--dir <目录>] [--yes]
        首次配置。问三个问题（盯哪儿、要不要开机自启、要不要接进 agent），
        剩下的自己做完，末尾打印二维码。重复跑是安全的。

  pe serve [--bind 0.0.0.0:8080] [--data <目录>]
        直接启动服务（前台）。改了配置不用重启，它自己会重读。

  pe reload
        让运行中的服务立刻重读配置。平时不需要——改动最多两秒自动生效。

  pe service install|uninstall|start|stop|restart|status|logs
        装成开机自启的用户服务（Linux 走 systemd --user，macOS 走 launchd），
        并顺手开启 linger，免得你一退出登录服务就停了。

  pe status [--json]
        服务在不在、盯着什么、收了多少、客户端配好没。
        直接读库，所以服务没跑时也答得上来。

  pe doctor [--fix] [--list] [--run 名字,…] [--json]
        自检。端口、inotify 预算、监听目录、客户端配置、hook、时区、
        孤儿 blob、二进制里有没有前端——能自动修的加 --fix 就修了。

  pe client set|show
        管客户端配置（~/.config/pe/config.toml），pe push / hook / MCP 用它。
        set 写完会去连一次，当场告诉你通没通。

  pe watch add <目录> [--project 名称] [--include '*.md']
  pe watch list
  pe watch rm <目录>
        管理监听目录。支持 glob，例如 ~/Code/*/docs。
        可以直接指向仓库根，agent 写在哪个目录都能收到；添加时会报出
        需要多少 inotify 句柄，超预算才需要收窄范围。

  pe push <文件|-> [--project 名称] [--tag 标签]... [--title 标题] [--run 会话ID]
        推送单篇文档。读 ~/.config/pe/config.toml，也认 PE_ENDPOINT / PE_TOKEN。

  pe hook-install [--write]
        打印（或直接写入）Claude Code 的 PostToolUse hook 配置。装上之后
        agent 每写一个 .md / .html 就自动进平台，不用管它写在哪个目录，
        同一次会话的产出还会在时间线上聚成一组。

  pe hook-ingest [--verbose]
        hook 的接收端，从 stdin 读 Claude Code 传来的 JSON。不用手动调。

  pe mcp
        以 stdio MCP server 运行，向 agent 暴露 publish_document 工具。
        与 hook 的分工是「主动 vs 被动」：hook 把每个 md 都收进来留档，
        MCP 让 agent 自己决定「这份值得给人看」并附上标题、标签和摘要。

  pe feedback [list] [--status open|fixed|wontfix|all]
  pe feedback show <编号>
  pe feedback fix|wontfix|reopen <编号> [--note "…"]
  pe feedback rm <编号>
  pe feedback export [--format md|jsonl]
        看和处理界面上提交的问题反馈。每条都带着提交时的环境快照，
        省掉「你那边是什么环境」这一轮问答。
        数据目录下还有一份自动生成的 feedback.md，打开就能看。

  pe pair [--name 名字] [--minutes 10] [--print]
        加一台设备：打印一个一次性配对码，扫完那台设备就有自己的登录了。
        不出示主口令，也不影响其它设备。

  pe device list [--json]
  pe device revoke <编号> | --all
        看有哪些设备登录着，单独撤掉其中一台。

  pe token rotate [--revoke-devices]
        换主口令。主口令是给机器用的（pe push / hook / MCP），
        换掉它们要重配；配对过的设备不受影响。
        有了 pe pair 之后，这条命令只在「怀疑口令泄露了」时才需要。

  pe version

环境变量:
  PE_DATA_DIR   数据目录，默认 ~/.local/share/pe
  PE_ENDPOINT   pe push 的目标地址，默认 http://127.0.0.1:8080
  PE_TOKEN      pe push 使用的访问口令
`)
}
