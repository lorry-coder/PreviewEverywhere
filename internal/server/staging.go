package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"previeweverywhere/internal/pdf"
)

// 导出物的临时寄存处。
//
// 单文件 HTML 是前端生成的——只有浏览器里才有已经渲染好的图表和公式，
// 服务端拿不到。但直接让浏览器下载一个 Blob 在 iOS 上不可靠（<a download>
// 的支持时好时坏），而「真实 URL + Content-Disposition」一直可靠。
// 所以前端把生成好的东西交上来，换一个下载地址。
//
// 刻意不进 blob 库：那里只增不减，会攒下一堆再也用不到的导出物。
// 放内存、几分钟过期，下载紧跟着生成发生，过期了重点一次即可。
const (
	stagingTTL      = 5 * time.Minute
	stagingMaxItems = 8
	// 单份请求体的上限。前端会把内容压在更低的水位（见 exportDoc.ts），
	// 这里留出 JSON 转义的余量。
	stagingMaxBytes = 48 << 20
	// 整个寄存区的内存预算。只限单份是不够的——8 份各 48MB 就是 384MB，
	// 跑在 NAS 上足以把内存吃光。超预算时从最早的开始挤。
	stagingBudget = 96 << 20
)

type stagedFile struct {
	name     string
	mime     string
	data     []byte
	expireAt time.Time
}

type staging struct {
	mu    sync.Mutex
	items map[string]stagedFile
}

func newStaging() *staging { return &staging{items: map[string]stagedFile{}} }

func (s *staging) put(name, mimeType string, data []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()

	// 条数或总字节超了就从最早的开始挤。导出是即时动作，旧的没人要了。
	for len(s.items) >= stagingMaxItems || s.bytesLocked()+len(data) > stagingBudget {
		if len(s.items) == 0 {
			break // 单份就超预算：让它进来，由 stagingMaxBytes 兜底
		}
		var oldest string
		var t time.Time
		for k, v := range s.items {
			if oldest == "" || v.expireAt.Before(t) {
				oldest, t = k, v.expireAt
			}
		}
		delete(s.items, oldest)
	}

	buf := make([]byte, 16)
	rand.Read(buf)
	token := hex.EncodeToString(buf)
	s.items[token] = stagedFile{
		name: name, mime: mimeType, data: data,
		expireAt: time.Now().Add(stagingTTL),
	}
	return token
}

func (s *staging) take(token string) (stagedFile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	f, ok := s.items[token]
	return f, ok
}

func (s *staging) bytesLocked() int {
	n := 0
	for _, v := range s.items {
		n += len(v.data)
	}
	return n
}

func (s *staging) sweepLocked() {
	now := time.Now()
	for k, v := range s.items {
		if now.After(v.expireAt) {
			delete(s.items, k)
		}
	}
}

// handleStageExport 收下前端生成的导出物，返回一个下载地址。
func (s *Server) handleStageExport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Filename string `json:"filename"`
		Mime     string `json:"mime"`
		Content  string `json:"content"`
		// Format 为 "pdf" 时，服务端把 Content 当作自包含 HTML 转成 PDF。
		// 留空表示原样寄存（导出单文件 HTML 走这条）。
		Format string `json:"format"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, stagingMaxBytes)).Decode(&body); err != nil {
		// 「太大」和「格式不对」要分开说。混成一句的话，用户导出一篇大文档
		// 失败时无从判断该缩小内容还是该报 bug。
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("这篇导出后超过了 %d MB。图片转成内联会比原来大约三分之一，"+
					"可以改用「打印 / 存为 PDF」。", stagingMaxBytes>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "导出请求格式不对")
		return
	}
	if body.Content == "" {
		writeError(w, http.StatusBadRequest, "导出内容为空")
		return
	}
	name := safeFileName(body.Filename)
	if name == "文档" && body.Filename != "" {
		name = "导出"
	}
	mimeType := body.Mime
	if mimeType == "" {
		mimeType = "text/html; charset=utf-8"
	}

	content := []byte(body.Content)

	// PDF 由服务端从这份自包含 HTML 转出来。
	//
	// 为什么不让浏览器自己打印：手册引导用户「加到主屏」，那就是 standalone
	// 模式，而 Safari 在这个模式下不实现 window.print()，点了没有任何反应。
	// 为什么不在服务端从 Markdown 直接生成：图表和公式是浏览器渲染的，
	// 服务端手上那份 HTML 还没执行过 JS，转出来图表只剩一段代码。
	if body.Format == "pdf" {
		out, err := pdf.Render(body.Content, pdf.Options{})
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "生成 PDF 失败: "+err.Error())
			return
		}
		content = out
		mimeType = "application/pdf"
		// 前端传来的文件名可能已经带扩展名，不去重就会得到「评估.pdf.pdf」。
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".pdf"), ".html") + ".pdf"
	}

	token := s.stage.put(name, mimeType, content)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":       "/api/v1/export/" + token,
		"expiresIn": int(stagingTTL.Seconds()),
	})
}

// handleTakeExport 把寄存的导出物作为下载发出去。
func (s *Server) handleTakeExport(w http.ResponseWriter, r *http.Request) {
	f, ok := s.stage.take(r.PathValue("token"))
	if !ok {
		writeError(w, http.StatusNotFound, "这份导出已经过期了，回去重新点一次即可")
		return
	}
	serveDownload(w, f.name, f.mime, f.data, wantsInline(r))
}
