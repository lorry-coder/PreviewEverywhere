package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/ingest"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func cmdPush(args []string) error {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	project := fs.String("project", "", "项目名，覆盖自动判定与 front-matter")
	title := fs.String("title", "", "标题，覆盖 H1 与 front-matter")
	run := fs.String("run", "", "所属运行的外部 ID（如 agent 的 session_id）")
	runLabel := fs.String("run-label", "", "运行的显示名")
	endpoint := fs.String("endpoint", "", "服务地址，默认取配置或 PE_ENDPOINT")
	token := fs.String("token", "", "访问口令，默认取配置或 PE_TOKEN")
	var tags stringList
	fs.Var(&tags, "tag", "标签，可重复指定")
	files, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("需要指定文件路径，或用 - 从标准输入读")
	}

	client, err := config.LoadClient()
	if err != nil {
		return err
	}
	if *endpoint != "" {
		client.Endpoint = strings.TrimSuffix(*endpoint, "/")
	}
	if *token != "" {
		client.Token = *token
	}
	if client.Token == "" {
		return fmt.Errorf("没有访问口令。设置 PE_TOKEN，或写进 ~/.config/pe/config.toml")
	}

	for _, arg := range files {
		content, filename, err := readPushSource(arg)
		if err != nil {
			return err
		}
		// 与服务端同机时直接传路径，服务端才能顺着相对路径把图片一起收进来。
		localPath := ""
		if arg != "-" && isLoopback(client.Endpoint) {
			if abs, err := filepath.Abs(arg); err == nil {
				localPath = abs
			}
		}
		absPath := ""
		if arg != "-" {
			if a, err := filepath.Abs(arg); err == nil {
				absPath = a
			}
		}
		if err := pushOne(client, pushRequest{
			content:   content,
			filename:  filename,
			path:      localPath,
			localPath: absPath,
			project:   *project,
			title:     *title,
			tags:      tags,
			run:       *run,
			runLabel:  *runLabel,
		}); err != nil {
			return err
		}
	}
	return nil
}

type pushRequest struct {
	content  []byte
	filename string
	path     string // 同机时给服务端的绝对路径；跨机时为空
	// localPath 无论同机跨机都是本地绝对路径，用来在客户端侧算归属和收图片。
	localPath string
	project   string
	title     string
	tags      []string
	run       string
	runLabel  string
}

func readPushSource(arg string) ([]byte, string, error) {
	if arg == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, "", fmt.Errorf("读取标准输入失败: %w", err)
		}
		// 刻意不编造文件名。文档身份的兜底顺序是
		// 路径 → 显式 key → 文件名 → 标题 → 内容哈希，
		// 给个假名字会让两次 `pe push -` 撞进同一个 source_key，
		// 第二篇把第一篇当成新版本覆盖掉。
		return data, "", nil
	}
	data, err := os.ReadFile(arg)
	if err != nil {
		return nil, "", fmt.Errorf("读取 %s 失败: %w", arg, err)
	}
	return data, filepath.Base(arg), nil
}

func pushOne(client *config.Client, req pushRequest) error {
	httpReq, err := buildIngestRequest(client, req)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+client.Token)

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return fmt.Errorf("连接 %s 失败: %w", client.Endpoint, err)
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(payload, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(payload))
		}
		return fmt.Errorf("推送 %s 失败 (%d): %s", req.filename, resp.StatusCode, e.Error)
	}

	var out struct {
		DocID   int64 `json:"docId"`
		Seq     int   `json:"seq"`
		Changed bool  `json:"changed"`
		NewDoc  bool  `json:"newDoc"`
	}
	json.Unmarshal(payload, &out)

	switch {
	case !out.Changed:
		fmt.Printf("%s 内容未变，跳过\n", req.filename)
	case out.NewDoc:
		fmt.Printf("已收入 %s → %s/doc/%d\n", req.filename, client.Endpoint, out.DocID)
	default:
		fmt.Printf("已更新 %s (v%d) → %s/doc/%d\n", req.filename, out.Seq, client.Endpoint, out.DocID)
	}
	return nil
}

// buildIngestRequest 挑请求形式：
//
//	JSON      —— 管道推送（没有文件名），或与服务端同机（给路径就够了）
//	multipart —— 跨机器推送一个文件，正文必须随请求带过去
//
// 管道推送必须走 JSON：multipart 里 filename 为空时，Go 的解析器会把这一部分
// 当成普通表单字段而不是文件，服务端的 FormFile 直接找不到。
//
// 同机时也走 JSON 并且只给路径：服务端顺着它做归属判定（向上找 .pe.toml / .git），
// 还能把文档里引用的相对路径图片一并收进来——这两件事光有正文是做不到的。
func buildIngestRequest(client *config.Client, req pushRequest) (*http.Request, error) {
	url := client.Endpoint + "/api/v1/ingest"

	if req.path != "" || req.filename == "" {
		body := map[string]any{
			"filename": req.filename,
			"project":  req.project,
			"title":    req.title,
			"tags":     req.tags,
			"run":      req.run,
			"runLabel": req.runLabel,
		}
		if req.path != "" {
			// 同机：给路径，正文让服务端自己读，省一次搬运也拿到了上下文。
			body["path"] = req.path
		} else {
			body["content"] = string(req.content)
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		return httpReq, nil
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", req.filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(req.content); err != nil {
		return nil, err
	}

	// 跨机时服务端看不到你的文件系统，本地能算出来的东西得一并送过去，
	// 否则归属退化成「推送」、图片变成一张无声的坏图。
	projectHint, sourceKey := "", ""
	if req.localPath != "" {
		ref := ingest.DetectProject(req.localPath)
		projectHint = ref.Name
		sourceKey = filepath.Base(req.localPath)
		if rel, err := filepath.Rel(ref.Root, req.localPath); err == nil && !strings.HasPrefix(rel, "..") {
			sourceKey = filepath.ToSlash(rel)
		}
		for ref, data := range collectLocalAssets(req.content, req.localPath) {
			ap, err := mw.CreateFormFile("asset:"+ref, filepath.Base(ref))
			if err != nil {
				return nil, err
			}
			if _, err := ap.Write(data); err != nil {
				return nil, err
			}
		}
	}

	fields := map[string]string{
		"project": req.project, "title": req.title,
		"run": req.run, "runLabel": req.runLabel, "filename": req.filename,
		"projectHint": projectHint, "sourceKey": sourceKey,
	}
	for k, v := range fields {
		if v != "" {
			mw.WriteField(k, v)
		}
	}
	if len(req.tags) > 0 {
		mw.WriteField("tags", strings.Join(req.tags, ","))
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	return httpReq, nil
}

func isLoopback(endpoint string) bool {
	return strings.Contains(endpoint, "127.0.0.1") || strings.Contains(endpoint, "localhost")
}
