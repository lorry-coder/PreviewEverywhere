package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/ingest"
	"previeweverywhere/internal/server"
	"previeweverywhere/internal/store"
)

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	bind := fs.String("bind", "", "监听地址，默认取配置或 0.0.0.0:8080")
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		return err
	}
	if *bind != "" {
		cfg.Bind = *bind
	}

	st, err := store.Open(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	// 首次启动没有口令，生成一个并把二维码打在终端上。
	freshToken := ""
	if cfg.TokenHash == "" {
		if freshToken, err = cfg.NewToken(); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
	}

	// 配置不再被任何人长期持有：谁要读都从这份快照里取当下那一份。
	// 于是 `pe source add` / `pe token` 改完配置，运行中的服务能当场跟上，
	// 不必重启——见 config.Live 里的说明。
	live := config.NewLive(*dataDir, cfg)

	pipe := ingest.New(st, live)
	watcher, err := ingest.NewWatcher(pipe, live)
	if err != nil {
		return fmt.Errorf("初始化文件监听失败: %w", err)
	}
	live.OnReload(func(*config.Config) { watcher.Reload() })

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := watcher.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("文件监听退出: %v", err)
		}
	}()

	go watchConfig(ctx, live)

	api := server.New(st, live, pipe, watcher)
	api.SetDataDir(*dataDir)
	srv := &http.Server{
		Addr:    cfg.Bind,
		Handler: api.Handler(),
		// SSE 是长连接，写超时必须留空，否则连接会被定期掐断。
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 记一份运行状态，`pe status` / `pe reload` 靠它找到这个进程。
	if err := writeRuntime(*dataDir, cfg.Bind); err != nil {
		log.Printf("写运行状态失败（不影响服务）: %v", err)
	}
	defer removeRuntime(*dataDir)

	printBanner(cfg, freshToken)

	errc := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Println("正在退出…")
		// 先让 SSE 长连接收尾。不这么做的话 Shutdown 会一直等它们——
		// 而它们按定义永不结束，结果就是每次 Ctrl-C 都要卡满超时，
		// 最后再报一句 context deadline exceeded。
		api.Close()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			// 还是有连接赖着不走。强行关掉即可——进程本来就要结束了，
			// 这不是使用者需要看到的「错误」。
			srv.Close()
			log.Println("有连接未在期限内关闭，已强制断开。")
		}
		return nil
	}
}

// configPollInterval 是 pe.toml 的轮询间隔。
//
// 两秒是照着人的节奏定的：你在另一个终端敲完 `pe source add`，
// 抬眼看服务日志时它已经生效了。更短没有意义，更长就会让人怀疑没生效。
const configPollInterval = 2 * time.Second

// watchConfig 让配置改动自动生效，同时也接 SIGHUP。
//
// 两条路都留着，各有各的场景：
//
//	自动轮询  你在另一个终端敲 `pe source add`，不必再想起还要通知谁。
//	SIGHUP    脚本里改完配置要确定它已经生效（`pe reload` 会等一下）。
//
// 轮询看的是 mtime + size 而不是内容：改这个文件的是另一个进程，
// 而它是截断重写、不是原子换名，那种写法的 fsnotify 事件序列各家不一致。
func watchConfig(ctx context.Context, live *config.Live) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, reloadSignals()...)
	defer signal.Stop(hup)

	tick := time.NewTicker(configPollInterval)
	defer tick.Stop()

	reload := func(why string) {
		next, err := live.Reload()
		if err != nil {
			log.Printf("重读配置失败，仍用旧的: %v", err)
			return
		}
		log.Printf("%s：监听规则 %d 条，忽略规则 %d 条。", why, len(next.Watch), len(next.Ignore))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			reload("收到 SIGHUP，已重读配置")
		case <-tick.C:
			if live.Changed() {
				reload("pe.toml 有改动，已重读")
			}
		}
	}
}

// printBanner 打印访问地址；首次启动时附带二维码，
// 手机扫一下就完成登录，之后一年不用再输任何东西。
func printBanner(cfg *config.Config, freshToken string) {
	port := cfg.Bind
	if i := strings.LastIndex(port, ":"); i >= 0 {
		port = port[i+1:]
	}

	fmt.Println()
	fmt.Println("  PreviewEverywhere 已启动")
	fmt.Println()
	for _, ip := range lanIPs() {
		fmt.Printf("    http://%s:%s\n", ip, port)
	}
	fmt.Printf("    http://127.0.0.1:%s\n", port)
	fmt.Println()

	if freshToken == "" {
		fmt.Println("  已有访问口令。忘了就跑 `pe token` 重新生成。")
		fmt.Println()
		return
	}

	loginURL := fmt.Sprintf("http://%s:%s/#t=%s", firstLANIP(), port, freshToken)
	fmt.Println("  首次启动，用手机扫码登录（有效期一年）：")
	fmt.Println()
	qrterminal.GenerateHalfBlock(loginURL, qrterminal.L, os.Stdout)
	fmt.Println()
	fmt.Printf("  口令: %s\n", freshToken)
	fmt.Println("  这串东西只显示这一次，但随时可以用 `pe token` 换新的。")
	fmt.Println()
}

func lanIPs() []string {
	out := []string{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		out = append(out, ipnet.IP.String())
	}
	return out
}

func firstLANIP() string {
	if ips := lanIPs(); len(ips) > 0 {
		return ips[0]
	}
	return "127.0.0.1"
}
