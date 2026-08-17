package ingest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agent 生成的 HTML 常从 CDN 引图表库。没网时那些图表全是空白，
// 而带图表恰恰是这类文档走原样模式的唯一理由。
func TestCDNScriptIsInlined(t *testing.T) {
	// 必须是 TLS：生产路径只接受 https，明文一律拒绝。
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte("window.CHART_LIB = 1;"))
	}))
	defer srv.Close()
	allowTestHost(t, srv.URL)

	p, _ := newPipeline(t)
	p.cdnClient = srv.Client()
	raw := []byte(`<html><head>
<script src="` + srv.URL + `/echarts.min.js"></script>
<link rel="stylesheet" href="` + srv.URL + `/theme.css">
</head><body><div id="chart"></div></body></html>`)

	out, n := p.localizeHTML(raw, "", "")
	if n != 2 {
		t.Fatalf("应内联 2 项（脚本 + 样式），实得 %d", n)
	}
	got := string(out)
	if !strings.Contains(got, "window.CHART_LIB = 1;") {
		t.Error("脚本内容没有被内联进来")
	}
	if strings.Contains(got, "src=\"http") {
		t.Error("内联之后不该还留着外部 src")
	}
	if !strings.Contains(got, "<style>") {
		t.Error("样式表应被换成 <style>")
	}
}

// 文档内容是不可信输入：不能让一句 <script src> 把服务器
// 变成任意 URL 的抓取器。
func TestNonAllowlistedHostIsNotFetched(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()

	p, _ := newPipeline(t)
	raw := []byte(`<html><body><script src="` + srv.URL + `/x.js"></script></body></html>`)
	_, n := p.localizeHTML(raw, "", "")

	if hit {
		t.Error("白名单之外的主机被抓取了")
	}
	if n != 0 {
		t.Errorf("不该有任何内联，实得 %d", n)
	}
}

func TestHTTPSchemeIsRejected(t *testing.T) {
	p, _ := newPipeline(t)
	// 即便主机在白名单里，明文 http 也不接受
	raw := []byte(`<html><body><script src="http://cdn.jsdelivr.net/x.js"></script></body></html>`)
	if _, n := p.localizeHTML(raw, "", ""); n != 0 {
		t.Errorf("http 应被拒绝，实得 %d 项内联", n)
	}
}

// 原样模式下相对路径解析不到平台里的资源，内联成 data: URI
// 是唯一能让图片显示出来的办法。
func TestRelativeImageBecomesDataURI(t *testing.T) {
	p, _ := newPipeline(t)
	root := fakeRepo(t, "proj")
	dir := filepath.Join(root, "docs")
	if err := os.WriteFile(filepath.Join(dir, "chart.png"), []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := []byte(`<html><body><img src="./chart.png"></body></html>`)
	out, n := p.localizeHTML(raw, dir, root)
	if n != 1 {
		t.Fatalf("应内联 1 张图片，实得 %d", n)
	}
	if !strings.Contains(string(out), "data:image/png;base64,") {
		t.Errorf("图片没有被转成 data URI:\n%s", out)
	}
}

// 越界引用即便走内联这条路也必须挡住。
func TestInlineImageRespectsRootBoundary(t *testing.T) {
	p, _ := newPipeline(t)
	root := fakeRepo(t, "proj")
	outside := filepath.Join(filepath.Dir(root), "secret.png")
	if err := os.WriteFile(outside, []byte("绝密"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := []byte(`<html><body><img src="../../secret.png"></body></html>`)
	if _, n := p.localizeHTML(raw, filepath.Join(root, "docs"), root); n != 0 {
		t.Error("越界的图片引用不该被内联")
	}
}

// 库文件里出现 </script> 会把内联的 script 标签提前闭合，
// 剩下的代码泄漏成页面正文。这种情况宁可不内联。
func TestScriptWithClosingTagIsSkipped(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`var t = "</script>";`))
	}))
	defer srv.Close()
	allowTestHost(t, srv.URL)

	p, _ := newPipeline(t)
	p.cdnClient = srv.Client()
	raw := []byte(`<html><body><script src="` + srv.URL + `/x.js"></script></body></html>`)
	if _, n := p.localizeHTML(raw, "", ""); n != 0 {
		t.Error("含 </script> 的内容不该被内联")
	}
}

// allowTestHost 把测试服务器临时加进白名单，并在测试结束后摘掉。
func allowTestHost(t *testing.T, rawURL string) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	cdnHosts[u.Host] = true
	t.Cleanup(func() { delete(cdnHosts, u.Host) })
}
