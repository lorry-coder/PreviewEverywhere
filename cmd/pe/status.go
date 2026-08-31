package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/store"
)

// `pe status` 回答四个问题：服务在不在、盯着什么、收了多少、客户端配好没。
//
// 在这之前这四件事没有任何命令行入口。网页上有一页「环境自查」，
// 但你得先能打开网页——而「打不开网页」恰恰是最需要查状态的时候。
//
// 数据直接读库，不走 HTTP：这样服务没在跑时它照样能回答「盯着什么、收了多少」。
// SQLite 是 WAL 模式，另一个进程正在写的时候读是安全的。

type statusReport struct {
	Version string `json:"version"`
	DataDir string `json:"dataDir"`

	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Bind    string `json:"bind,omitempty"`
	Started string `json:"started,omitempty"`
	// Reachable 说端口上真的有人应答。它和 Running 可能不一致——
	// 进程还在但卡死了，或者端口被别的东西占了，都属于这种。
	Reachable bool   `json:"reachable"`
	Serving   string `json:"serving,omitempty"`

	Sources []string `json:"sources"`
	Docs    int      `json:"docs"`
	Unread  int      `json:"unread"`
	Latest  string   `json:"latest,omitempty"`
	// Devices 只数**配对过的**设备。配对机制之前的旧登录（Cookie 里直接放主口令）
	// 数不出来——服务端手上只有主口令的哈希，没有办法知道有几台设备正拿着它。
	// 所以这个字段的名字要说清它数的是什么，不能让它暗示「能进来的只有这些」。
	Devices int `json:"pairedDevices"`

	ClientEndpoint string `json:"clientEndpoint,omitempty"`
	ClientToken    bool   `json:"clientToken"`

	Addresses []string `json:"addresses,omitempty"`
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	asJSON := fs.Bool("json", false, "输出 JSON，给脚本用")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	rep := collectStatus(*dataDir)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printStatus(rep)
	return nil
}

func collectStatus(dataDir string) statusReport {
	rep := statusReport{Version: version, DataDir: dataDir}

	if info, err := readRuntime(dataDir); err == nil && info != nil {
		rep.Running = true
		rep.PID = info.PID
		rep.Bind = info.Bind
		rep.Started = info.Started
		rep.Serving = info.Version
		rep.Reachable = probe(info.Bind)
		rep.Addresses = accessURLs(info.Bind)
	}

	if cfg, err := config.Load(dataDir); err == nil {
		for _, w := range cfg.Watch {
			rep.Sources = append(rep.Sources, w.Path)
		}
		if !rep.Running {
			rep.Addresses = accessURLs(cfg.Bind)
		}
	}

	// 服务没在跑时库也读得到；只在库压根不存在时才跳过。
	if _, err := os.Stat(filepath.Join(dataDir, "pe.db")); err == nil {
		if st, err := store.Open(dataDir); err == nil {
			defer st.Close()
			if total, unread, err := st.Stats(); err == nil {
				rep.Docs, rep.Unread = total, unread
			}
			rep.Latest = latestDoc(st)
			if devices, err := st.ListDevices(); err == nil {
				rep.Devices = len(devices)
			}
		}
	}

	if c, err := config.LoadClient(); err == nil {
		rep.ClientEndpoint = c.Endpoint
		rep.ClientToken = c.Token != ""
	}
	return rep
}

// latestDoc 报出最近一次采集的时间。「上一次收到东西是什么时候」
// 是判断采集有没有停掉的最直接依据，比「一共多少篇」有用得多。
func latestDoc(st *store.Store) string {
	var at int64
	if err := st.DB.QueryRow(`SELECT COALESCE(MAX(updated_at), 0) FROM doc`).Scan(&at); err != nil || at == 0 {
		return ""
	}
	return time.Unix(at, 0).Format("2006-01-02 15:04:05")
}

// probe 只做一次 TCP 连接，不发 HTTP 请求。
// 发请求就要带口令，而 status 不该因为「口令配错了」就说服务挂了——
// 那是两件不同的事，混在一起报会把人引到错误的方向。
func probe(bind string) bool {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// accessURLs 只列真的连得上的地址。
//
// 绑在 127.0.0.1 上时**不能**列局域网 IP —— 那些地址会被拒连接，
// 而把连不上的地址打出来，比不打更让人困惑（「你不是说这个地址吗」）。
func accessURLs(bind string) []string {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return nil
	}
	local := "http://127.0.0.1:" + port
	if host != "" && host != "0.0.0.0" && host != "::" {
		if host == "127.0.0.1" || host == "localhost" || host == "::1" {
			return []string{local}
		}
		// 绑在某个具体网卡上，那就只有那一个地址。
		return []string{fmt.Sprintf("http://%s:%s", host, port)}
	}
	out := []string{}
	for _, ip := range lanIPs() {
		out = append(out, fmt.Sprintf("http://%s:%s", ip, port))
	}
	return append(out, local)
}

func printStatus(r statusReport) {
	fmt.Println()
	switch {
	case r.Running && r.Reachable:
		fmt.Printf("  ● 运行中   pid %d，监听 %s\n", r.PID, r.Bind)
	case r.Running:
		// 进程在但端口不应答。这是个真实的故障态（卡死、还在启动、
		// 端口被别的东西占了），说「运行中」会把人引偏。
		fmt.Printf("  ◐ 进程在但端口不应答   pid %d，%s\n", r.PID, r.Bind)
		fmt.Println("    看日志：pe service logs")
	default:
		fmt.Println("  ○ 没在跑")
		fmt.Println("    起它：pe serve      或   pe service start")
	}
	if r.Running && r.Serving != "" && r.Serving != r.Version {
		// 这条很值钱：`pe upgrade` 换了盘上的二进制，但服务还跑着旧的。
		fmt.Printf("    ⚠ 跑着的是 %s，盘上的是 %s —— 重启才会换过去\n", r.Serving, r.Version)
	}
	fmt.Println()

	if len(r.Addresses) > 0 && r.Running {
		for _, u := range r.Addresses {
			fmt.Printf("    %s\n", u)
		}
		fmt.Println()
	}

	fmt.Printf("  数据目录   %s\n", r.DataDir)
	fmt.Printf("  文档       %d 篇，%d 未读\n", r.Docs, r.Unread)
	if r.Latest != "" {
		fmt.Printf("  最近入库   %s\n", r.Latest)
	}

	// 配对设备数。0 台时给出下一步，而不是干巴巴报一个 0——
	// 「还没配对过」这件事本身就是一条可以行动的信息。
	if r.Devices == 0 {
		fmt.Println("  已配对设备 无（加一台：pe pair）")
	} else {
		fmt.Printf("  已配对设备 %d 台（看明细：pe device list）\n", r.Devices)
	}

	if len(r.Sources) == 0 {
		fmt.Println("  监听目录   无（只接受推送）")
	} else {
		fmt.Printf("  监听目录   %d 条\n", len(r.Sources))
		for _, s := range r.Sources {
			fmt.Printf("             %s\n", s)
		}
	}

	fmt.Printf("  客户端     %s", r.ClientEndpoint)
	if r.ClientToken {
		fmt.Println("（已配口令）")
	} else {
		fmt.Println("（没配口令 —— pe push 会报错，hook 会静默跳过）")
	}
	fmt.Println()
}
