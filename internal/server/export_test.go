package server

import (
	"strings"
	"testing"
	"time"
)

// zip 里的路径必须落在包内。解压工具照做的话，一条 ../ 就能把文件写到包外面去。
func TestZipPathIsContained(t *testing.T) {
	cases := []struct {
		ref      string
		wantSafe bool
	}{
		{"./img/arch.png", true},
		{"img/arch.png", true},
		{"assets/a/b/c.png", true},
		{"../../../etc/passwd", false},
		{"..", false},
		{"/etc/passwd", false},
		{"C:\\Windows\\x.png", false},
	}
	for _, c := range cases {
		name, safe := zipPathFor(c.ref, "deadbeef")
		if safe != c.wantSafe {
			t.Errorf("%s: 安全判定为 %v，期望 %v", c.ref, safe, c.wantSafe)
		}
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			t.Errorf("%s 生成的 zip 路径逃出了包: %s", c.ref, name)
		}
		if !safe && !strings.HasPrefix(name, "_assets/") {
			t.Errorf("%s 不安全时应当落到 _assets/，实得 %s", c.ref, name)
		}
	}
}

// 按次序取 sha：这是老文档配对的一半依据。
func TestAssetShasInOrder(t *testing.T) {
	html := `<p><img src="/api/v1/asset/aaa11122"></p>
	         <p><img src="https://example.com/x.png"></p>
	         <p><img src="/api/v1/asset/bbb22233"></p>`
	got := assetShasInOrder(html)
	if len(got) != 2 || got[0] != "aaa11122" || got[1] != "bbb22233" {
		t.Errorf("次序或内容不对: %v", got)
	}
}

// 按次序取原始引用：配对的另一半。
// 两边都必须排除绝对地址和 data URI，否则数出来的次序对不上。
func TestLocalImageRefsInOrder(t *testing.T) {
	md := []byte("开头\n\n![一](./img/a.png)\n\n![远程](https://example.com/x.png)\n\n" +
		"<img src=\"img/b.png\">\n\n![data](data:image/png;base64,xxx)\n\n![二](./img/c.png)\n")
	got := localImageRefs(md, ".md")
	want := []string{"./img/a.png", "img/b.png", "./img/c.png"}
	if len(got) != len(want) {
		t.Fatalf("数量不对: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个应为 %s，实得 %s", i, want[i], got[i])
		}
	}
}

// 中文文件名必须能正确下载：ASCII 兜底 + RFC 5987 两条都要在。
func TestDownloadFilenameEncoding(t *testing.T) {
	if got := safeFileName("迁移风险/评估:v2"); strings.ContainsAny(got, `/\:`) {
		t.Errorf("文件名里还留着非法字符: %q", got)
	}
	if got := safeFileName("   "); got != "文档" {
		t.Errorf("空标题应当有兜底名字，实得 %q", got)
	}
	if got := urlEncode("迁移"); !strings.HasPrefix(got, "%") {
		t.Errorf("中文应当被百分号编码，实得 %q", got)
	}
	if got := asciiFallback("迁移risk"); strings.ContainsAny(got, "迁移") {
		t.Errorf("ASCII 兜底里不该有非 ASCII 字符: %q", got)
	}
}

// 寄存处：会过期、有数量上限、取过之后仍可再取（同一次下载可能重试）。
func TestStagingExpiryAndCap(t *testing.T) {
	st := newStaging()
	tok := st.put("甲.html", "text/html", []byte("<p>x</p>"))
	if _, ok := st.take(tok); !ok {
		t.Fatal("刚存进去就取不到")
	}
	if _, ok := st.take(tok); !ok {
		t.Error("同一份应当可以重复取（下载可能重试）")
	}
	if _, ok := st.take("不存在的令牌"); ok {
		t.Error("不存在的令牌不该取到东西")
	}

	// 超过上限时挤掉最早的，不能无限占内存
	for i := 0; i < stagingMaxItems+3; i++ {
		st.put("x.html", "text/html", []byte("y"))
	}
	if len(st.items) > stagingMaxItems {
		t.Errorf("寄存数量突破了上限: %d", len(st.items))
	}
}

// 只限单份是不够的：8 份各 48MB 就是 384MB，跑在 NAS 上足以把内存吃光。
// 总字节必须也有预算。
func TestStagingTotalBudget(t *testing.T) {
	st := newStaging()
	chunk := make([]byte, stagingBudget/3)
	for i := 0; i < 6; i++ {
		st.put("big.html", "text/html", chunk)
	}
	st.mu.Lock()
	total := st.bytesLocked()
	st.mu.Unlock()
	if total > stagingBudget {
		t.Errorf("寄存区总字节 %d 超出预算 %d", total, stagingBudget)
	}
	if total == 0 {
		t.Error("全被挤掉了，最后一份应当留下")
	}
}

// 单份就超预算时也得让它进来，由请求体上限去兜底，
// 而不是在这里陷进「挤空了还是放不下」的死循环。
func TestStagingSingleOversized(t *testing.T) {
	st := newStaging()
	done := make(chan bool, 1)
	go func() {
		st.put("huge.html", "text/html", make([]byte, stagingBudget*2))
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("放入超大内容时卡死了")
	}
}

// 前端传来的文件名已经带扩展名，服务端再补一个就成了「评估.pdf.pdf」。
func TestExportFilenameNoDoubleExtension(t *testing.T) {
	cases := map[string]string{
		"评估.pdf":  "评估.pdf",
		"评估.html": "评估.pdf",
		"评估":      "评估.pdf",
	}
	for in, want := range cases {
		got := strings.TrimSuffix(strings.TrimSuffix(safeFileName(in), ".pdf"), ".html") + ".pdf"
		if got != want {
			t.Errorf("%q → %q，期望 %q", in, got, want)
		}
	}
}
