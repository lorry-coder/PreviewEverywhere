package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
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
	stagingMaxBytes = 64 << 20 // 单份上限，也是内存占用的天花板
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

	// 满了就把最早过期的挤掉。导出是即时动作，旧的没人要了。
	for len(s.items) >= stagingMaxItems {
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
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, stagingMaxBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "导出内容太大或格式不对")
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

	token := s.stage.put(name, mimeType, []byte(body.Content))
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
	serveDownload(w, f.name, f.mime, f.data)
}
