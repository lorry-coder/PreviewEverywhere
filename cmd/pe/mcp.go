package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"previeweverywhere/internal/config"
)

// pe mcp 是一个最小的 stdio MCP server。
//
// 它和 hook 的分工是「主动 vs 被动」：hook 把 agent 写的每个 md 都收进来，
// 适合留档；MCP 让 agent 自己决定「这份产出值得给人看」，并附上它自己写的
// 标题、标签和一句摘要。长任务结束时做一次总结投递，比一股脑倒进来有用得多。
//
// 协议是 JSON-RPC 2.0，一行一条消息。stdout 是协议通道，
// 所以任何日志都只能走 stderr——往 stdout 打一个字都会把客户端搞崩。

const mcpProtocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func cmdMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	endpoint := fs.String("endpoint", "", "服务地址，默认取配置或 PE_ENDPOINT")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := config.LoadClient()
	if err != nil {
		return err
	}
	if *endpoint != "" {
		client.Endpoint = strings.TrimSuffix(*endpoint, "/")
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 16<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // 解析不了的行直接丢，别让整个连接死掉
		}

		resp, hasResponse := handleMCP(client, req)
		if !hasResponse {
			continue // 通知类消息不回包
		}
		payload, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		out.Write(payload)
		out.WriteByte('\n')
		out.Flush()
	}
	return in.Err()
}

func handleMCP(client *config.Client, req rpcRequest) (rpcResponse, bool) {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = mcpProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "previeweverywhere", "version": version_()},
		}

	case "tools/list":
		resp.Result = map[string]any{"tools": []any{publishToolSchema()}}

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "参数解析失败"}
			break
		}
		if p.Name != "publish_document" {
			resp.Error = &rpcError{Code: -32602, Message: "未知的工具: " + p.Name}
			break
		}
		text, isErr := callPublish(client, p.Arguments)
		resp.Result = map[string]any{
			"content": []any{map[string]any{"type": "text", "text": text}},
			"isError": isErr,
		}

	case "ping":
		resp.Result = map[string]any{}

	default:
		if isNotification {
			return resp, false // notifications/initialized 之类，静默接受
		}
		resp.Error = &rpcError{Code: -32601, Message: "不支持的方法: " + req.Method}
	}

	if isNotification {
		return resp, false
	}
	return resp, true
}

func version_() string { return version }

func publishToolSchema() map[string]any {
	return map[string]any{
		"name": "publish_document",
		"description": "把一份文档发布到 PreviewEverywhere 阅读平台，供人在手机上阅读。" +
			"适合在一段较长的工作结束时，投递一份值得人类过目的总结、评估或报告。" +
			"content 与 path 二选一：已经写成文件了就给 path，否则直接给 content。",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "Markdown 正文。与 path 二选一。",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "已存在的 .md/.html 文件的绝对路径。与 content 二选一；用它还能把文档里引用的图片一并收进来。",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "标题。留空则取正文第一个 H1。",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "归属项目。留空时按文件路径自动判定（向上找 .git）。",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "标签，例如 [\"风险\", \"待复核\"]。人类会用它检索。",
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "一句话摘要，显示在列表里，让人一眼判断值不值得点开。",
				},
				"run": map[string]any{
					"type":        "string",
					"description": "本次会话的标识。同一个 run 的产出会在时间线上聚成一组。",
				},
			},
		},
	}
}

func callPublish(client *config.Client, argsRaw json.RawMessage) (string, bool) {
	var args struct {
		Content string   `json:"content"`
		Path    string   `json:"path"`
		Title   string   `json:"title"`
		Project string   `json:"project"`
		Tags    []string `json:"tags"`
		Summary string   `json:"summary"`
		Run     string   `json:"run"`
	}
	if err := json.Unmarshal(argsRaw, &args); err != nil {
		return "参数解析失败: " + err.Error(), true
	}
	if client.Token == "" {
		return "平台未配置访问口令。请在 ~/.config/pe/config.toml 里写 token，或设置 PE_TOKEN 环境变量。", true
	}
	if args.Content == "" && args.Path == "" {
		return "content 与 path 至少要给一个。", true
	}

	body := map[string]any{
		"title":   args.Title,
		"project": args.Project,
		"tags":    args.Tags,
		"run":     args.Run,
	}
	if args.Path != "" && isLoopback(client.Endpoint) {
		body["path"] = args.Path
	} else {
		content := args.Content
		if content == "" {
			data, err := os.ReadFile(args.Path)
			if err != nil {
				return "读不到 " + args.Path + ": " + err.Error(), true
			}
			content = string(data)
		}
		// summary 通过 front-matter 传进去，省得给采集管线单开一个字段。
		if args.Summary != "" {
			content = "---\nsummary: " + strconv.Quote(args.Summary) + "\n---\n\n" + content
		}
		body["content"] = content
		body["explicit"] = true
		if args.Path != "" {
			// 有原文件名时带上，好让服务端按扩展名判断 md/html。
			body["filename"] = filepath.Base(args.Path)
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err.Error(), true
	}
	req, err := http.NewRequest(http.MethodPost, client.Endpoint+"/api/v1/ingest", bytes.NewReader(payload))
	if err != nil {
		return err.Error(), true
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.Token)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "连接 " + client.Endpoint + " 失败：平台可能没在运行。" + err.Error(), true
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		return fmt.Sprintf("发布失败 (%d): %s", resp.StatusCode, e.Error), true
	}

	var out struct {
		DocID   int64 `json:"docId"`
		Seq     int   `json:"seq"`
		Changed bool  `json:"changed"`
		NewDoc  bool  `json:"newDoc"`
	}
	json.Unmarshal(raw, &out)

	switch {
	case !out.Changed:
		return "内容与平台上已有的版本一致，未产生新版本。", false
	case out.NewDoc:
		return fmt.Sprintf("已发布。阅读地址：%s/#/doc/%d", client.Endpoint, out.DocID), false
	default:
		return fmt.Sprintf("已更新到 v%d。阅读地址：%s/#/doc/%d", out.Seq, client.Endpoint, out.DocID), false
	}
}
