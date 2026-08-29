package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/store"
)

// `pe pair` 与 `pe device`：把「加一台设备」和「换主口令」拆开。
//
// 在这之前它们是同一个动作。口令只存 sha256，忘了就拿不回来，
// 于是想让一台新设备进来，唯一的办法是 `pe token` 换一个新的——
// 而那会让家里所有已登录的设备一起失效。
//
// 现在扫一次配对码，那台设备就拿到属于它自己的长期会话。
// 加设备不影响别人，撤掉一台也不影响别人。

// pairTTL 是配对码的有效期。
//
// 十分钟：够你从终端走到沙发上拿起手机，又短到即使这串东西
// 留在了终端回滚记录里也不再是个凭据。
const pairTTL = 10 * time.Minute

func cmdPair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	name := fs.String("name", "", "这台设备叫什么，留空则按浏览器猜")
	minutes := fs.Int("minutes", int(pairTTL.Minutes()), "有效期（分钟）")
	printOnly := fs.Bool("print", false, "只打印链接和码，不画二维码")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	st, err := store.Open(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	code, err := st.NewPairCode(*name, time.Duration(*minutes)*time.Minute)
	if err != nil {
		return err
	}

	port := "8080"
	if info, err := readRuntime(*dataDir); err == nil && info != nil {
		if i := strings.LastIndex(info.Bind, ":"); i >= 0 {
			port = info.Bind[i+1:]
		}
	} else if cfg, err := config.Load(*dataDir); err == nil {
		if i := strings.LastIndex(cfg.Bind, ":"); i >= 0 {
			port = cfg.Bind[i+1:]
		}
	}
	url := fmt.Sprintf("http://%s:%s/#p=%s", firstLANIP(), port, code)

	fmt.Println()
	fmt.Printf("  用手机扫这个码，%d 分钟内有效，只能用一次：\n", *minutes)
	fmt.Println()
	if !*printOnly {
		qrterminal.GenerateHalfBlock(url, qrterminal.L, os.Stdout)
		fmt.Println()
	}
	fmt.Printf("  %s\n", url)
	fmt.Println()
	// 主口令一个字都不出现在这里，这正是这条命令存在的意义。
	fmt.Println("  扫完这台设备就有自己的登录了，不影响其它设备。")
	fmt.Println("  看有哪些设备：pe device list")
	fmt.Println()
	return nil
}

func cmdDevice(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: pe device list|revoke")
	}
	switch args[0] {
	case "list", "ls":
		return deviceList(args[1:])
	case "revoke", "rm":
		return deviceRevoke(args[1:])
	default:
		return fmt.Errorf("未知子命令 %q，可用: list / revoke", args[0])
	}
}

func deviceList(args []string) error {
	fs := flag.NewFlagSet("device list", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	st, err := store.Open(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	devices, err := st.ListDevices()
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(devices)
	}

	fmt.Println()
	if len(devices) == 0 {
		fmt.Println("  还没有配对过的设备。")
		fmt.Println("  加一台：pe pair")
	} else {
		fmt.Printf("  %-5s %-14s %-20s %s\n", "编号", "名字", "最后活跃", "配对于")
		for _, d := range devices {
			fmt.Printf("  %-5d %-14s %-20s %s\n", d.ID, d.Name,
				since(d.LastSeen), time.Unix(d.CreatedAt, 0).Format("2006-01-02"))
		}
		fmt.Println()
		fmt.Println("  撤掉一台：pe device revoke <编号>")
	}

	// 旧登录看不见也数不出来——Cookie 里存的是主口令，服务端手上只有它的哈希，
	// 没有任何办法知道有几台设备正拿着它。不说这一句，这张列表就在暗示
	// 「能进来的只有这些」，而那不是事实。
	fmt.Println()
	fmt.Println("  注意：配对机制之前的旧登录仍然有效，且不出现在上面这张表里。")
	fmt.Println("  要把它们一并清掉，换一次主口令：pe token rotate")
	fmt.Println()
	return nil
}

func deviceRevoke(args []string) error {
	fs := flag.NewFlagSet("device revoke", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "数据目录")
	all := fs.Bool("all", false, "撤掉全部")
	positional, err := parseFlags(fs, args)
	if err != nil {
		return err
	}

	st, err := store.Open(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	if *all {
		n, err := st.RevokeAllDevices()
		if err != nil {
			return err
		}
		fmt.Printf("  ✓ 撤掉了 %d 台设备。它们要重新扫码：pe pair\n", n)
		return nil
	}

	if len(positional) != 1 {
		return errors.New("用法: pe device revoke <编号>    （编号看 pe device list）")
	}
	id, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		return fmt.Errorf("编号得是数字。看一眼有哪些：pe device list")
	}
	name, err := st.RevokeDevice(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("没有编号 %d 这台设备。看一眼：pe device list", id)
		}
		return err
	}
	fmt.Printf("  ✓ 已撤掉「%s」。别的设备不受影响。\n", name)
	return nil
}

// since 把时间戳说成人话。「3 分钟前」比一个时间戳更快回答
// 「这台还在用吗」——而那正是你打开这张表要问的问题。
func since(ts int64) string {
	if ts == 0 {
		return "从未"
	}
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	default:
		return time.Unix(ts, 0).Format("2006-01-02")
	}
}
