package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/ingest"
	"previeweverywhere/internal/server"
	"previeweverywhere/internal/store"
)

// `pe doctor` 把手册第八节那张排查表变成一条命令。
//
// 那张表有二十来行，每一行都是「症状 → 可能原因 → 去看什么」。
// 问题在于每一次排查都是人在做机器能做的事：去 stat 一个目录、
// 去数一遍 inotify 配额、去 curl 一下 endpoint。
//
// 所以这里的取舍是：**只放那些程序真的能查出结论的项**。
// 查不出结论就别列——一条永远显示「请手动确认」的检查项是纯噪音。
//
// 能自动修的带上修法（--fix）。不能自动修的给出确切的下一步，
// 而不是「请检查配置」这种等于没说的话。

type checkStatus string

const (
	statusOK   checkStatus = "ok"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
)

type checkResult struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail"`
	// Hint 是「你该做什么」。只在非 ok 时有意义。
	Hint string `json:"hint,omitempty"`
	// Fixed 说这一项被 --fix 修好了。
	Fixed bool `json:"fixed,omitempty"`
}

// checkCtx 是所有检查项共享的上下文。一次性算好，免得每项各读一遍配置。
type checkCtx struct {
	dataDir string
	cfg     *config.Config
	client  *config.Client
	rt      *runtimeInfo
	fix     bool
}

type check struct {
	name string
	// run 返回结论。fix 为真时它可以顺手把问题修掉，并在 Fixed 上打标记。
	run func(*checkCtx) checkResult
}

func allChecks() []check {
	return []check{
		{"数据目录", checkDataDir},
		{"服务", checkService},
		{"端口", checkPort},
		{"监听目录", checkSources},
		{"inotify 预算", checkInotify},
		{"客户端配置", checkClient},
		{"agent hook", checkHook},
		{"时区", checkTimezone},
		{"blobs", checkBlobs},
		{"前端资源", checkFrontend},
	}
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	list := fs.Bool("list", false, "只列出有哪些检查项")
	only := fs.String("run", "", "只跑这几项，逗号分隔")
	fix := fs.Bool("fix", false, "能自动修的直接修")
	asJSON := fs.Bool("json", false, "输出 JSON，给脚本用")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	checks := allChecks()
	if *list {
		for _, c := range checks {
			fmt.Printf("  %s\n", c.name)
		}
		return nil
	}
	if *only != "" {
		want := map[string]bool{}
		for _, n := range strings.Split(*only, ",") {
			want[strings.TrimSpace(n)] = true
		}
		var kept []check
		for _, c := range checks {
			if want[c.name] {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			return fmt.Errorf("没有叫这些名字的检查项。看看有哪些：pe doctor --list")
		}
		checks = kept
	}

	ctx := &checkCtx{dataDir: *dataDir, fix: *fix}
	ctx.cfg, _ = config.Load(*dataDir)
	ctx.client, _ = config.LoadClient()
	ctx.rt, _ = readRuntime(*dataDir)

	results := make([]checkResult, 0, len(checks))
	for _, c := range checks {
		r := c.run(ctx)
		r.Name = c.name
		results = append(results, r)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		printDoctor(results, *fix)
	}

	// 有 fail 就以非零退出，这样它能直接用在脚本和 CI 里。
	// warn 不算失败——警告是「你可能想知道」，不是「这儿坏了」。
	for _, r := range results {
		if r.Status == statusFail {
			return errSilent
		}
	}
	return nil
}

func printDoctor(results []checkResult, fixed bool) {
	fmt.Println()
	bad := 0
	for _, r := range results {
		mark := "✓"
		switch r.Status {
		case statusWarn:
			mark = "!"
		case statusFail:
			mark = "✗"
			bad++
		}
		suffix := ""
		if r.Fixed {
			suffix = "  （已修）"
		}
		fmt.Printf("  %s %-14s %s%s\n", mark, r.Name, r.Detail, suffix)
		if r.Hint != "" && r.Status != statusOK {
			for _, line := range strings.Split(r.Hint, "\n") {
				fmt.Printf("      %s\n", line)
			}
		}
	}
	fmt.Println()
	switch {
	case bad > 0 && !fixed:
		fmt.Printf("  %d 项有问题。能自动修的加 --fix 再跑一次。\n", bad)
	case bad > 0:
		fmt.Printf("  %d 项还有问题，得手动处理（上面写了怎么做）。\n", bad)
	default:
		fmt.Println("  没查出问题。")
	}
	fmt.Println()
}

// ── 具体的检查项 ──────────────────────────────────────────────────

func checkDataDir(c *checkCtx) checkResult {
	st, err := os.Stat(c.dataDir)
	if err != nil {
		if !c.fix {
			return checkResult{Status: statusFail, Detail: c.dataDir + " 不存在",
				Hint: "加 --fix 建它，或者直接跑 pe setup"}
		}
		if err := os.MkdirAll(c.dataDir, 0o755); err != nil {
			return checkResult{Status: statusFail, Detail: "建不了 " + c.dataDir, Hint: err.Error()}
		}
		return checkResult{Status: statusOK, Detail: c.dataDir, Fixed: true}
	}
	if !st.IsDir() {
		return checkResult{Status: statusFail, Detail: c.dataDir + " 不是目录"}
	}
	// 真的写一下。目录存在不等于写得进去——只读挂载、属主不对、
	// 磁盘满，这三种都会让「存在」和「能用」脱节。
	probe := filepath.Join(c.dataDir, ".doctor-write-test")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		return checkResult{Status: statusFail, Detail: "写不进 " + c.dataDir,
			Hint: err.Error() + "\n属主对不对？磁盘满没满？"}
	}
	os.Remove(probe)
	return checkResult{Status: statusOK, Detail: c.dataDir}
}

func checkService(c *checkCtx) checkResult {
	if c.rt == nil {
		return checkResult{Status: statusWarn, Detail: "没在跑",
			Hint: "起它：pe serve   或   pe service start"}
	}
	detail := fmt.Sprintf("pid %d，监听 %s", c.rt.PID, c.rt.Bind)
	if c.rt.Version != "" && c.rt.Version != version {
		return checkResult{Status: statusWarn, Detail: detail,
			Hint: fmt.Sprintf("跑着的是 %s，盘上的是 %s —— 重启才会换过去：pe service restart",
				c.rt.Version, version)}
	}
	return checkResult{Status: statusOK, Detail: detail}
}

// checkPort 分三种情况，因为它们的处理方式完全不同：
// 端口有人应答且就是我们（好）、端口被别人占了（换端口或杀掉它）、
// 没人应答（服务没起来）。混成一句「端口不通」帮不上忙。
func checkPort(c *checkCtx) checkResult {
	bind := ""
	if c.rt != nil {
		bind = c.rt.Bind
	} else if c.cfg != nil {
		bind = c.cfg.Bind
	}
	if bind == "" {
		return checkResult{Status: statusWarn, Detail: "还不知道要监听哪个端口"}
	}
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return checkResult{Status: statusFail, Detail: "bind 写得不对: " + bind}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, port)

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		if c.rt == nil {
			return checkResult{Status: statusOK, Detail: port + " 空着（服务没在跑）"}
		}
		return checkResult{Status: statusFail, Detail: addr + " 连不上，但进程还在",
			Hint: "服务卡住了，或者还没绑上去。看日志：pe service logs"}
	}
	conn.Close()

	if c.rt != nil {
		return checkResult{Status: statusOK, Detail: addr + " 有应答"}
	}
	// 没有我们的进程，端口却有人应答 —— 被别的程序占了。
	return checkResult{Status: statusFail, Detail: addr + " 被别的程序占着",
		Hint: "换个端口：pe serve --bind 0.0.0.0:8081\n或者先找出占用的进程：lsof -i :" + port}
}

func checkSources(c *checkCtx) checkResult {
	if c.cfg == nil || len(c.cfg.Watch) == 0 {
		return checkResult{Status: statusOK, Detail: "没配（只接受推送）"}
	}
	var missing []string
	for _, w := range c.cfg.Watch {
		path := expandHome(w.Path)
		if strings.ContainsAny(path, "*?[") {
			if m, _ := filepath.Glob(path); len(m) == 0 {
				missing = append(missing, w.Path+"（通配符一个都没匹配到）")
			}
			continue
		}
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			missing = append(missing, w.Path)
		}
	}
	if len(missing) == 0 {
		return checkResult{Status: statusOK, Detail: fmt.Sprintf("%d 条，都在", len(c.cfg.Watch))}
	}
	return checkResult{Status: statusWarn,
		Detail: fmt.Sprintf("%d 条，其中 %d 条指向不存在的目录", len(c.cfg.Watch), len(missing)),
		Hint:   strings.Join(missing, "\n") + "\n删掉它：pe source rm <目录>"}
}

func checkInotify(c *checkCtx) checkResult {
	if runtime.GOOS != "linux" {
		return checkResult{Status: statusOK, Detail: "不适用（只有 Linux 有 inotify）"}
	}
	if c.cfg == nil || len(c.cfg.Watch) == 0 {
		return checkResult{Status: statusOK, Detail: "不监听目录，用不上"}
	}
	budget := ingest.WatchBudget()
	total := 0
	for _, w := range c.cfg.Watch {
		pattern := expandHome(w.Path)
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			matches = []string{pattern}
		}
		for _, m := range matches {
			if n, ok := ingest.CountWatchDirs(m, c.cfg.Ignore); ok {
				total += n
			}
		}
	}
	detail := fmt.Sprintf("要 %d 个，预算 %d", total, budget)
	if total > budget {
		// 这是 Linux 上最容易踩、又最难察觉的问题：症状是
		// 「新文档有时候不进来」，而且没有任何报错。
		return checkResult{Status: statusFail, Detail: detail,
			Hint: "超预算的目录不会被监听，表现是「新文档有时候不进来」且不报错。\n" +
				"提高上限：sudo sysctl -w fs.inotify.max_user_watches=524288\n" +
				"或者收窄监听范围，也可以改用 hook（那样目录问题根本不存在）"}
	}
	if budget > 0 && total > budget*8/10 {
		return checkResult{Status: statusWarn, Detail: detail, Hint: "已经用掉八成预算了"}
	}
	return checkResult{Status: statusOK, Detail: detail}
}

func checkClient(c *checkCtx) checkResult {
	if c.client == nil || c.client.Token == "" {
		hint := "pe push 会报错，hook 会静默跳过（它的设计原则是绝不打断 agent）。\n" +
			"配上：pe client set"
		if c.fix {
			// 自动修只在同机、且我们知道口令的情况下才可能——而我们不知道
			// 口令原文（只存 sha256）。所以这一项永远只能给出下一步。
			hint = "口令只存 sha256，程序自己拿不到原文，修不了。\n" +
				"换一个新的再配：pe token && pe client set"
		}
		return checkResult{Status: statusFail, Detail: "没配口令", Hint: hint}
	}

	req, err := http.NewRequest(http.MethodGet, c.client.Endpoint+"/api/v1/status", nil)
	if err != nil {
		return checkResult{Status: statusFail, Detail: "endpoint 写得不对: " + c.client.Endpoint}
	}
	req.Header.Set("Authorization", "Bearer "+c.client.Token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return checkResult{Status: statusFail, Detail: "连不上 " + c.client.Endpoint,
			Hint: "服务起来了吗？跨机器的话地址要填局域网 IP，不是 127.0.0.1"}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return checkResult{Status: statusOK, Detail: c.client.Endpoint + " 通"}
	case http.StatusUnauthorized:
		return checkResult{Status: statusFail, Detail: "口令不对",
			Hint: "换一个再配：pe token && pe client set"}
	default:
		return checkResult{Status: statusFail,
			Detail: c.client.Endpoint + " 返回 " + resp.Status,
			Hint:   "这个地址后面像是别的服务"}
	}
}

func checkHook(c *checkCtx) checkResult {
	path := defaultSettingsPath()
	// settings.json 不存在和「存在但没有我们那段」是同一回事：都还没装上。
	// 之前这两条走了不同的分支，于是文件压根不存在时 --fix 够不着，
	// 只能眼睁睁看着它一直说「没装」。
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "hook-ingest") {
		return checkResult{Status: statusOK, Detail: "已装（" + path + "）"}
	}
	if !c.fix {
		return checkResult{Status: statusWarn, Detail: "没装",
			Hint: "装上之后 agent 写的每个 .md / .html 都会自动进来，不用管目录：\n" +
				"pe agent install --write   （或 pe doctor --fix）"}
	}
	if err := cmdHookInstall([]string{"--write"}); err != nil {
		return checkResult{Status: statusFail, Detail: "装不上", Hint: err.Error()}
	}
	return checkResult{Status: statusOK, Detail: "已装（" + path + "）", Fixed: true}
}

// checkTimezone 只在容器里才是真问题：时间线按「服务端本地日期」分组，
// 而界面上的「今天/昨天」按手机时区显示。容器默认 UTC，
// 于是半夜前后写的文档会被归到昨天却标着「今天」。
func checkTimezone(c *checkCtx) checkResult {
	name, offset := time.Now().Zone()
	detail := fmt.Sprintf("%s (UTC%+d)", name, offset/3600)
	if os.Getenv("TZ") == "" && inContainer() {
		return checkResult{Status: statusWarn, Detail: detail + "，没设 TZ",
			Hint: "容器默认 UTC，会让时间线的「今天/昨天」和手机上对不上。\n" +
				"compose 里加：TZ: Asia/Shanghai"}
	}
	return checkResult{Status: statusOK, Detail: detail}
}

func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	return err == nil && (strings.Contains(string(data), "docker") ||
		strings.Contains(string(data), "containerd"))
}

// checkBlobs 找孤儿：blobs/ 里存在、但库里没有任何地方引用的文件。
//
// 它们的来源是入库到一半失败（先落盘、后写库）。不影响使用，只占地方，
// 所以是 warn 而不是 fail。--fix 会删掉它们。
func checkBlobs(c *checkCtx) checkResult {
	blobDir := filepath.Join(c.dataDir, "blobs")
	if _, err := os.Stat(blobDir); err != nil {
		return checkResult{Status: statusOK, Detail: "还没有"}
	}
	if _, err := os.Stat(filepath.Join(c.dataDir, "pe.db")); err != nil {
		return checkResult{Status: statusOK, Detail: "还没有库"}
	}

	st, err := store.Open(c.dataDir)
	if err != nil {
		return checkResult{Status: statusWarn, Detail: "打不开库，跳过", Hint: err.Error()}
	}
	defer st.Close()

	known := map[string]bool{}
	rows, err := st.DB.Query(`SELECT sha FROM blob`)
	if err != nil {
		return checkResult{Status: statusWarn, Detail: "查不了库，跳过", Hint: err.Error()}
	}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err == nil {
			known[sha] = true
		}
	}
	rows.Close()

	var orphans []string
	var bytes int64
	filepath.Walk(blobDir, func(p string, info os.FileInfo, err error) error { //nolint:errcheck
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // 走不动的目录跳过就行
		}
		if !known[info.Name()] {
			orphans = append(orphans, p)
			bytes += info.Size()
		}
		return nil
	})

	if len(orphans) == 0 {
		return checkResult{Status: statusOK, Detail: fmt.Sprintf("库里 %d 个，磁盘上没有多余的", len(known))}
	}
	detail := fmt.Sprintf("库里 %d 个，磁盘上多出 %d 个没人引用的（%s）", len(known), len(orphans), humanBytes(bytes))
	if !c.fix {
		return checkResult{Status: statusWarn, Detail: detail,
			Hint: "入库中途失败留下的，不影响使用。清掉：pe doctor --fix"}
	}
	removed := 0
	for _, p := range orphans {
		if os.Remove(p) == nil {
			removed++
		}
	}
	return checkResult{Status: statusOK,
		Detail: fmt.Sprintf("库里 %d 个，清掉 %d 个没人引用的（%s）", len(known), removed, humanBytes(bytes)),
		Fixed:  true}
}

// checkFrontend 确认这个二进制里真的嵌进了前端。
//
// 少了它服务照常启动、接口照常应答，只是网页一片空白——
// 而这恰恰是发布流水线最容易出的错（忘了在 go build 之前跑 npm build）。
func checkFrontend(c *checkCtx) checkResult {
	name := server.EmbeddedBuild()
	if name == "" {
		return checkResult{Status: statusFail, Detail: "二进制里没有前端",
			Hint: "这个构建是坏的：网页会一片空白，但接口一切正常。\n" +
				"从源码构建的话，用 make build（它会先构建前端再嵌进去）"}
	}
	return checkResult{Status: statusOK, Detail: name}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatInt(n/(1<<10), 10) + " KB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}
