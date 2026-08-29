package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"previeweverywhere/internal/config"
)

// `pe client …` 管客户端配置（~/.config/pe/config.toml）。
//
// 在这之前，手册教的是手写一段 heredoc：
//
//	mkdir -p ~/.config/pe
//	cat > ~/.config/pe/config.toml <<EOF
//	endpoint = "http://127.0.0.1:8080"
//	token = "<把 pe serve 打印的那串粘过来>"
//	EOF
//
// 三个问题：口令要人手搬一趟；写错了不会有任何反馈；而 hook 的设计原则是
// 绝不打断 agent，所以配错时它**静默跳过**——你只会觉得「怎么没进来」。
//
// 所以这条命令写完之后一定去连一次，当场告诉你通没通。

func cmdClient(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: pe client set|show")
	}
	switch args[0] {
	case "set":
		return clientSet(args[1:])
	case "show":
		return clientShow(args[1:])
	default:
		return fmt.Errorf("未知子命令 %q，可用: set / show", args[0])
	}
}

func clientSet(args []string) error {
	fs := flag.NewFlagSet("client set", flag.ExitOnError)
	endpoint := fs.String("endpoint", "", "服务地址，如 http://192.168.1.10:8080")
	token := fs.String("token", "", "访问口令")
	skipCheck := fs.Bool("no-verify", false, "不去连一次验证")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	cur, err := config.LoadClient()
	if err != nil {
		return err
	}

	if *endpoint == "" {
		*endpoint = ask("服务地址", cur.Endpoint)
	}
	if *token == "" {
		// 已经配过就默认沿用，只是不回显——口令不该出现在终端回滚记录里。
		if cur.Token != "" {
			if !confirm("已经配过口令了，换一个吗？", false) {
				*token = cur.Token
			}
		}
		if *token == "" {
			*token = ask("访问口令（pe serve 或 pe pair 打印的那串）", "")
		}
	}
	if strings.TrimSpace(*token) == "" {
		return errors.New("口令不能为空。没有它 pe push 会报错、hook 会静默跳过")
	}

	next := &config.Client{
		Endpoint: strings.TrimSuffix(strings.TrimSpace(*endpoint), "/"),
		Token:    strings.TrimSpace(*token),
	}

	if !*skipCheck {
		if err := verifyClient(next); err != nil {
			fmt.Println()
			fmt.Println("  ✗", err)
			fmt.Println()
			if !confirm("还是把它存下来吗？", false) {
				return errors.New("没有保存")
			}
		} else {
			fmt.Println("  ✓ 连上了")
		}
	}

	if err := config.SaveClient(next); err != nil {
		return err
	}
	fmt.Printf("  ✓ 已写入 %s\n", clientConfigPath())
	return nil
}

func clientShow(args []string) error {
	fs := flag.NewFlagSet("client show", flag.ExitOnError)
	reveal := fs.Bool("reveal", false, "把口令原文打出来")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	c, err := config.LoadClient()
	if err != nil {
		return err
	}

	path := clientConfigPath()
	if _, err := os.Stat(path); err != nil {
		fmt.Printf("  %s 还不存在\n", path)
	} else {
		fmt.Printf("  %s\n", path)
	}
	fmt.Printf("  endpoint  %s\n", c.Endpoint)
	switch {
	case c.Token == "":
		fmt.Println("  token     （没配）")
	case *reveal:
		fmt.Printf("  token     %s\n", c.Token)
	default:
		// 默认遮掉：这条命令的典型场景是贴给别人看「我配的是啥」。
		fmt.Printf("  token     %s\n", mask(c.Token))
	}

	// 环境变量优先级更高，配了就一定要说——否则你会对着一份
	// 内容正确的配置文件，怎么也想不通为什么连的是别的地方。
	if v := os.Getenv("PE_ENDPOINT"); v != "" {
		fmt.Printf("\n  注意：环境变量 PE_ENDPOINT=%s 覆盖了配置文件\n", v)
	}
	if os.Getenv("PE_TOKEN") != "" {
		fmt.Printf("  注意：环境变量 PE_TOKEN 覆盖了配置文件\n")
	}
	return nil
}

// verifyClient 拿这份配置去真的连一次。
func verifyClient(c *config.Client) error {
	req, err := http.NewRequest(http.MethodGet, c.Endpoint+"/api/v1/status", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("连不上 %s：%v\n    服务起来了吗？跨机器的话地址要填局域网 IP，不是 127.0.0.1", c.Endpoint, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("连上了 %s，但口令不对。用 `pe pair --print` 取一个新的", c.Endpoint)
	default:
		return fmt.Errorf("%s 返回 %s，看着不像 pe 服务", c.Endpoint, resp.Status)
	}
}

func clientConfigPath() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "pe", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pe-client.toml"
	}
	return filepath.Join(home, ".config", "pe", "config.toml")
}

// mask 只留头尾各四位。够你确认「是不是那一串」，又不至于泄露出去。
func mask(s string) string {
	if len(s) <= 12 {
		return strings.Repeat("·", len(s))
	}
	return s[:4] + strings.Repeat("·", 8) + s[len(s)-4:]
}
