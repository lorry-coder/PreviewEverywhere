package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/ingest"
)

// hookPayload 是 Claude Code 通过 stdin 传给 hook 的 JSON。
// 只挑我们需要的字段，其余忽略——协议以后加字段不该让这里挂掉。
type hookPayload struct {
	SessionID     string          `json:"session_id"`
	Cwd           string          `json:"cwd"`
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResult    json.RawMessage `json:"tool_result"`
	ToolResponse  json.RawMessage `json:"tool_response"`
}

// cmdHookIngest 是 PostToolUse hook 的接收端：agent 每写一个 md/html
// 就自动进平台，写在哪个目录都行——这条通道彻底绕开了「文档必须放在
// 约定目录里」这个前提。
//
// 铁律：无论发生什么都返回 nil。hook 跑在 agent 的关键路径上，
// 平台没起、网络不通、payload 变了格式，都不该让用户的 agent 卡住或报错。
func cmdHookIngest(args []string) error {
	fs := flag.NewFlagSet("hook-ingest", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verbose := fs.Bool("verbose", false, "把处理结果打到 stderr，用于排查")
	endpoint := fs.String("endpoint", "", "服务地址，默认取配置或 PE_ENDPOINT")
	if err := fs.Parse(args); err != nil {
		return nil
	}

	note := func(format string, a ...any) {
		if *verbose {
			fmt.Fprintf(os.Stderr, "pe hook: "+format+"\n", a...)
		}
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
	if err != nil || len(raw) == 0 {
		note("读不到 stdin，跳过")
		return nil
	}
	var payload hookPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		note("payload 不是合法 JSON，跳过: %v", err)
		return nil
	}

	path := extractFilePath(payload.ToolInput, payload.ToolResult, payload.ToolResponse)
	if path == "" {
		note("payload 里没有文件路径，跳过")
		return nil
	}
	if !filepath.IsAbs(path) && payload.Cwd != "" {
		path = filepath.Join(payload.Cwd, path)
	}

	if reason := skipReason(path); reason != "" {
		note("跳过 %s：%s", path, reason)
		return nil
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		note("跳过 %s：文件不存在", path)
		return nil
	}

	client, err := config.LoadClient()
	if err != nil {
		note("读客户端配置失败: %v", err)
		return nil
	}
	if *endpoint != "" {
		client.Endpoint = strings.TrimSuffix(*endpoint, "/")
	}
	if client.Token == "" {
		note("没有访问口令，跳过。设置 PE_TOKEN 或写进 ~/.config/pe/config.toml")
		return nil
	}

	if err := hookPush(client, path, payload); err != nil {
		note("推送失败: %v", err)
		return nil
	}
	note("已推送 %s", path)
	return nil
}

// extractFilePath 在几种 tool_input 结构里找文件路径。
// Write/Edit 用 file_path，NotebookEdit 用 notebook_path，
// 字段名以后再变也只是多一个候选键的事。
func extractFilePath(sources ...json.RawMessage) string {
	keys := []string{"file_path", "filePath", "notebook_path", "notebookPath", "path"}
	for _, raw := range sources {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for _, k := range keys {
			if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
	}
	return ""
}

// skipReason 过滤掉不该进平台的文件。hook 会对 agent 写的每个文件触发，
// 所以这里必须便宜且严格。
func skipReason(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".html", ".htm":
	default:
		return "不是 md/html"
	}
	// hook 拿不到服务端的 pe.toml（可能压根在另一台机器上），只能用内置规则。
	// 但判断方式必须和监听器一致，否则会出现「watch 不收、hook 却收了」的分歧。
	dir := filepath.Dir(path)
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if part != "" && config.MatchIgnore(part, config.DefaultIgnore) {
			return "位于 " + part + " 内"
		}
	}
	// 构建树认标记文件而不是名字。沿目录链往上找，最多 12 层——
	// 再深就不值得为一次 hook 调用付这些 stat 了。
	for i := 0; i < 12 && dir != "/" && dir != "."; i++ {
		if ingest.IsBuildTree(dir) {
			return "位于构建树 " + dir + " 内"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func hookPush(client *config.Client, path string, payload hookPayload) error {
	body := map[string]any{
		// session_id 让同一次 agent 运行的产出在时间线上聚成一组——
		// 这正是「昨晚那次跑出了什么」这个问题需要的信息。
		"run": payload.SessionID,
	}
	if payload.Cwd != "" {
		body["runLabel"] = filepath.Base(payload.Cwd)
	}

	if isLoopback(client.Endpoint) {
		// 同机时给路径就行：服务端顺着它做归属判定，还能把相对路径的图片一起收进来。
		body["path"] = path
	} else {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// 跨机时本地算好身份再发，保证和同机推送落到同一篇文档上。
		ref := ingest.DetectProject(path)
		key := filepath.Base(path)
		if rel, err := filepath.Rel(ref.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
			key = filepath.ToSlash(rel)
		}
		body["content"] = string(content)
		body["filename"] = filepath.Base(path)
		body["sourceKey"] = key
		body["projectHint"] = ref.Name
		// 服务端看不到这台机器的磁盘，被引用的图片得随正文一起送过去。
		if assets := collectLocalAssets(content, path); len(assets) > 0 {
			encoded := make(map[string]string, len(assets))
			for ref, data := range assets {
				encoded[ref] = base64.StdEncoding.EncodeToString(data)
			}
			body["assets"] = encoded
		}
	}

	payloadJSON, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, client.Endpoint+"/api/v1/ingest", bytes.NewReader(payloadJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.Token)

	// 超时压到 3 秒：平台没起的时候，agent 不该为此多等。
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("服务端返回 %d", resp.StatusCode)
	}
	return nil
}

// ── 安装 ──────────────────────────────────────────────────────────

func cmdHookInstall(args []string) error {
	fs := flag.NewFlagSet("hook-install", flag.ExitOnError)
	write := fs.Bool("write", false, "直接写入 ~/.claude/settings.json（会先备份）")
	settingsPath := fs.String("settings", defaultSettingsPath(), "settings.json 路径")
	if err := fs.Parse(args); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "pe"
	}
	command := exe + " hook-ingest"

	if !*write {
		fmt.Printf(`把下面这段合并进 %s：

{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Write|Edit",
        "hooks": [
          { "type": "command", "command": %q }
        ]
      }
    ]
  }
}

之后 agent 每写一个 .md / .html 就会自动进平台，不管它写在哪个目录。
同一次会话的产出会按 session_id 聚成一组，显示在时间线首页上。

要我直接写进去就加 --write（会先备份成 settings.json.bak）。
先确认口令已经配好：
  echo 'endpoint = "http://127.0.0.1:8080"' >  ~/.config/pe/config.toml
  echo 'token = "<你的口令>"'                >> ~/.config/pe/config.toml
`, *settingsPath, command)
		return nil
	}

	return installHook(*settingsPath, command)
}

func defaultSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude/settings.json"
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// installHook 把 hook 合并进 settings.json。刻意保留文件里的其它内容，
// 并且先备份——这是用户的全局配置，不能想当然地覆盖。
func installHook(path, command string) error {
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &settings); err != nil {
				return fmt.Errorf("%s 不是合法 JSON，先手动修一下: %w", path, err)
			}
			if err := os.WriteFile(path+".bak", data, 0o644); err != nil {
				return fmt.Errorf("备份失败: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	postToolUse, _ := hooks["PostToolUse"].([]any)

	for _, entry := range postToolUse {
		if strings.Contains(fmt.Sprint(entry), "hook-ingest") {
			fmt.Println("已经装过了，没有改动。")
			return nil
		}
	}

	hooks["PostToolUse"] = append(postToolUse, map[string]any{
		"matcher": "Write|Edit",
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	})
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return err
	}

	fmt.Printf("已写入 %s（原文件备份为 %s.bak）\n", path, path)
	fmt.Println("重开一个 Claude Code 会话后生效。")
	fmt.Println()
	fmt.Println("验证方式：让 agent 随便写一个 .md，然后看平台的时间线。")
	fmt.Println("没反应的话跑 `pe hook-ingest --verbose` 看它抱怨什么：")
	fmt.Printf("  echo '{\"session_id\":\"t\",\"cwd\":\"%s\",\"tool_input\":{\"file_path\":\"/some/doc.md\"}}' | pe hook-ingest --verbose\n",
		filepath.Dir(path))
	return nil
}

// cmdAgent 是「把平台接进 agent 工作流」这件事的入口。
//
// 它就是原来的 hook-install，换了个名字：hook 是实现手段，
// 而人想做的事情是「让 agent 写的东西自动进来」。
// 顺带把 MCP 这条路也在这里指出来——两者是「被动留档」和「主动投递」的分工，
// 分散在两处讲，人很难意识到自己该用哪个。
func cmdAgent(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: pe agent install [--write]   |   pe agent status")
	}
	switch args[0] {
	case "install":
		return cmdHookInstall(args[1:])
	case "status":
		return agentStatus()
	default:
		return fmt.Errorf("未知子命令 %q，可用: install / status", args[0])
	}
}

// agentStatus 只回答一个问题：hook 装上了没有。
//
// 这个问题值得单列，因为 hook 的设计原则是绝不打断 agent——
// 没装上不会有任何报错，你只会觉得「怎么没进来」。
func agentStatus() error {
	path := defaultSettingsPath()
	data, err := os.ReadFile(path)
	switch {
	case err != nil:
		fmt.Printf("  ○ 没装（%s 还不存在）\n", path)
		fmt.Println("    装上：pe agent install --write")
	case !strings.Contains(string(data), "hook-ingest"):
		fmt.Printf("  ○ 没装（%s 里没有 pe 的 hook）\n", path)
		fmt.Println("    装上：pe agent install --write")
	default:
		fmt.Printf("  ● 已装（%s）\n", path)
		fmt.Println("    agent 每写一个 .md / .html 就会自动进来，不管它写在哪个目录。")
	}
	fmt.Println()
	fmt.Println("  另一条路是 MCP：`pe mcp` 让 agent 自己决定「这份值得给人看」，")
	fmt.Println("  并附上标题、标签和摘要。两者的分工是「被动留档」和「主动投递」。")
	return nil
}
