package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"previeweverywhere/internal/config"
)

// 同机推送必须把本地路径发出去。少了它，服务端无从做归属判定
// （向上找 .pe.toml / .git），文档里引用的相对路径图片也收不进来——
// 而这两件事光有正文是做不到的。曾经算出了路径却忘了塞进请求。
func TestSameMachinePushSendsPath(t *testing.T) {
	client := &config.Client{Endpoint: "http://127.0.0.1:8080", Token: "t"}
	req, err := buildIngestRequest(client, pushRequest{
		content:  []byte("# 标题\n"),
		filename: "a.md",
		path:     "/home/me/repo/docs/a.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("同机推送应走 JSON，实得 %s", ct)
	}

	raw, _ := io.ReadAll(req.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["path"] != "/home/me/repo/docs/a.md" {
		t.Errorf("请求里没有带上本地路径: %v", body["path"])
	}
	if _, ok := body["content"]; ok {
		t.Error("给了路径就不必再搬一遍正文")
	}
}

// 管道推送没有文件名，必须走 JSON：multipart 里 filename 为空时
// Go 会把它当成普通表单字段，服务端的 FormFile 直接找不到。
func TestPipedPushUsesJSONWithContent(t *testing.T) {
	client := &config.Client{Endpoint: "http://127.0.0.1:8080", Token: "t"}
	req, err := buildIngestRequest(client, pushRequest{content: []byte("# 管道\n")})
	if err != nil {
		t.Fatal(err)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("管道推送应走 JSON，实得 %s", ct)
	}
	raw, _ := io.ReadAll(req.Body)
	var body map[string]any
	json.Unmarshal(raw, &body)
	if body["content"] != "# 管道\n" {
		t.Errorf("管道推送必须带正文: %v", body["content"])
	}
	if _, ok := body["path"]; ok {
		t.Error("管道推送不该有路径")
	}
}

// 跨机器推送拿不到对方的文件系统，只能把正文随 multipart 传过去。
func TestCrossMachinePushUsesMultipart(t *testing.T) {
	client := &config.Client{Endpoint: "http://192.168.1.10:8080", Token: "t"}
	req, err := buildIngestRequest(client, pushRequest{
		content: []byte("# 远程\n"), filename: "a.md", tags: []string{"风险"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ct := req.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
		t.Fatalf("跨机器推送应走 multipart，实得 %s", ct)
	}
	raw, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(raw), "# 远程") {
		t.Error("跨机器推送必须把正文带上")
	}
}

// collectLocalAssets 要能认出 Markdown 与内联 HTML 两种图片写法，
// 并且拒绝越界引用——文档内容是不可信输入。
func TestCollectLocalAssets(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	docs := filepath.Join(repo, "docs")
	if err := os.MkdirAll(filepath.Join(docs, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "img", "a.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "outside.png"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	content := []byte(`# 标题

![图](./img/a.png)
<img src="./img/a.png">
![越界](../../outside.png)
![远程](https://example.com/x.png)
![不存在](./img/nope.png)
`)
	got := collectLocalAssets(content, filepath.Join(docs, "doc.md"))

	if len(got) != 1 {
		t.Fatalf("只应收到 1 张图，实得 %d: %v", len(got), keysOf(got))
	}
	if string(got["./img/a.png"]) != "png" {
		t.Errorf("内容不对: %q", got["./img/a.png"])
	}
	if _, bad := got["../../outside.png"]; bad {
		t.Error("越界引用不该被收取")
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
