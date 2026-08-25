// pe 是 PreviewEverywhere 的唯一可执行文件：它同时是服务端、命令行客户端
// 和文件监听器。整个平台「scp 一个文件过去就能跑」，靠的就是这一点。
package main

import (
	"fmt"
	"log"
	"os"

	// 把时区库编进二进制。
	//
	// 时间线是按「服务端本地日期」分组的，而精简系统（scratch/distroless 容器、
	// 某些 NAS 固件）根本没有 /usr/share/zoneinfo，那里 TZ=Asia/Shanghai 会被
	// 静默忽略、退回 UTC——于是「今天/昨天」在半夜前后就会和手机上看到的对不上。
	// 内嵌之后只要设 TZ 就一定生效，代价是几百 KB。
	_ "time/tzdata"
)

const version = "1.0.0"

func main() {
	log.SetFlags(log.Ltime)

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "push":
		err = cmdPush(os.Args[2:])
	case "watch":
		err = cmdWatch(os.Args[2:])
	case "token":
		err = cmdToken(os.Args[2:])
	case "feedback":
		err = cmdFeedback(os.Args[2:])
	case "hook-ingest":
		err = cmdHookIngest(os.Args[2:])
	case "hook-install":
		err = cmdHookInstall(os.Args[2:])
	case "mcp":
		err = cmdMCP(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("pe", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`PreviewEverywhere — 把 agent 产出的文档变成手机上读得完的东西

用法:
  pe serve [--bind 0.0.0.0:8080] [--data <目录>]
        启动服务。首次启动会生成访问口令并打印二维码，手机扫一次即可。

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

  pe token
        重新生成访问口令并打印二维码。需重启 pe serve 才生效，
        重启后旧的 Cookie 一并失效。

  pe version

环境变量:
  PE_DATA_DIR   数据目录，默认 ~/.local/share/pe
  PE_ENDPOINT   pe push 的目标地址，默认 http://127.0.0.1:8080
  PE_TOKEN      pe push 使用的访问口令
`)
}
