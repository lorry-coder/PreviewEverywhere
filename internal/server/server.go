// Package server 提供 HTTP 接口与前端静态资源。
package server

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/ingest"
	"previeweverywhere/internal/store"
	"previeweverywhere/web"
)

type Server struct {
	st    *store.Store
	cfg   *config.Live
	pipe  *ingest.Pipeline
	watch *ingest.Watcher // 可能为 nil（纯推送模式）
	hub   *hub
	// build 是内嵌前端的主脚本文件名，见 frontendBuild。
	build string
	// dataDir 用来重写 feedback.md 那份投影文件。
	dataDir string
	// stage 是导出物的临时寄存处，见 staging.go。
	stage *staging
}

func New(st *store.Store, cfg *config.Live, pipe *ingest.Pipeline, watch *ingest.Watcher) *Server {
	s := &Server{st: st, cfg: cfg, pipe: pipe, watch: watch, hub: newHub(), stage: newStaging()}
	if dist, err := fs.Sub(web.Dist, "dist"); err == nil {
		s.build = frontendBuild(dist)
	}
	// 文档一入库就推给在线的阅读端：手机页面开着的时候，
	// agent 写完文档它自己就冒出来，不用下拉刷新。
	pipe.OnIngest(func(e ingest.Event) { s.hub.broadcast("doc", e) })
	return s
}

// Close 让所有 SSE 长连接收尾。必须在 http.Server.Shutdown 之前调用，
// 否则 Shutdown 会一直等这些永不结束的连接，直到超时。
// SetDataDir 告诉服务端数据目录在哪。只有 feedback.md 这份投影用得上。
func (s *Server) SetDataDir(dir string) { s.dataDir = dir }

func logFeedbackFileError(err error) { log.Printf("重写 feedback.md 失败: %v", err) }

func (s *Server) Close() {
	s.hub.close()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 未鉴权即可访问：拿口令换 Cookie 的入口。
	mux.HandleFunc("POST /api/v1/session", s.handleSession)
	mux.HandleFunc("POST /api/v1/logout", s.handleLogout)

	mux.HandleFunc("GET /api/v1/status", s.requireAuth(s.handleStatus))
	mux.HandleFunc("GET /api/v1/projects", s.requireAuth(s.handleProjects))
	mux.HandleFunc("GET /api/v1/tags", s.requireAuth(s.handleTags))
	mux.HandleFunc("GET /api/v1/timeline", s.requireAuth(s.handleTimeline))
	mux.HandleFunc("GET /api/v1/search", s.requireAuth(s.handleSearch))
	mux.HandleFunc("GET /api/v1/docs", s.requireAuth(s.handleDocs))
	mux.HandleFunc("GET /api/v1/docs/{id}", s.requireAuth(s.handleDoc))
	mux.HandleFunc("PATCH /api/v1/docs/{id}", s.requireAuth(s.handlePatchDoc))
	mux.HandleFunc("DELETE /api/v1/docs/{id}", s.requireAuth(s.handleDeleteDoc))

	mux.HandleFunc("GET /api/v1/docs/{id}/download", s.requireAuth(s.handleDownloadDoc))
	mux.HandleFunc("POST /api/v1/export", s.requireAuth(s.handleStageExport))
	mux.HandleFunc("GET /api/v1/export/{token}", s.requireAuth(s.handleTakeExport))

	mux.HandleFunc("POST /api/v1/feedback", s.requireAuth(s.handleCreateFeedback))
	mux.HandleFunc("GET /api/v1/feedback", s.requireAuth(s.handleListFeedback))
	mux.HandleFunc("PATCH /api/v1/feedback/{id}", s.requireAuth(s.handlePatchFeedback))
	mux.HandleFunc("DELETE /api/v1/feedback/{id}", s.requireAuth(s.handleDeleteFeedback))
	mux.HandleFunc("PUT /api/v1/docs/{id}/tags", s.requireAuth(s.handleSetTags))
	mux.HandleFunc("GET /api/v1/docs/{id}/annotations", s.requireAuth(s.handleListAnnotations))
	mux.HandleFunc("POST /api/v1/docs/{id}/annotations", s.requireAuth(s.handleCreateAnnotation))
	mux.HandleFunc("GET /api/v1/docs/{id}/diff", s.requireAuth(s.handleDiff))
	mux.HandleFunc("GET /api/v1/annotations", s.requireAuth(s.handleActionable))
	mux.HandleFunc("PATCH /api/v1/annotations/{id}", s.requireAuth(s.handlePatchAnnotation))
	mux.HandleFunc("DELETE /api/v1/annotations/{id}", s.requireAuth(s.handleDeleteAnnotation))
	mux.HandleFunc("POST /api/v1/annotations/{id}/rebind", s.requireAuth(s.handleRebindAnnotation))
	mux.HandleFunc("PATCH /api/v1/tags/{name}", s.requireAuth(s.handleRenameTag))
	mux.HandleFunc("GET /api/v1/raw/{versionId}", s.requireAuth(s.handleRaw))
	mux.HandleFunc("GET /api/v1/asset/{sha}", s.requireAuth(s.handleAsset))
	mux.HandleFunc("POST /api/v1/ingest", s.requireAuth(s.handleIngest))
	mux.HandleFunc("GET /api/v1/events", s.requireAuth(s.hub.serve))

	mux.HandleFunc("/", s.serveFrontend())
	return recoverPanics(logRequests(mux))
}

// serveFrontend 提供嵌进二进制的前端，并做 SPA 兜底：
// 任何非 /api 的未知路径都回 index.html，让前端路由自己处理。
func (s *Server) serveFrontend() http.HandlerFunc {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		log.Fatalf("前端资源嵌入异常: %v", err)
	}
	files := http.FileServer(http.FS(dist))

	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "接口不存在")
			return
		}
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			serveIndex(w, r, dist)
			return
		}
		if f, err := dist.Open(clean); err == nil {
			f.Close()
			switch {
			case strings.HasPrefix(clean, "assets/"):
				// 构建产物带内容哈希文件名，可以长缓存。
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			case clean == "sw.js":
				// service worker 必须每次都去问一遍，否则前端更新推不下去。
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Service-Worker-Allowed", "/")
			case clean == "manifest.webmanifest":
				// Go 的内置 MIME 表里没有这个扩展名，得自己写。
				w.Header().Set("Content-Type", "application/manifest+json")
				w.Header().Set("Cache-Control", "no-cache")
			}
			files.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, dist)
	}
}

// frontendBuild 返回内嵌的 index.html 里引用的主 JS 文件名。
//
// 它就是这个二进制携带的前端版本号：文件名带内容哈希，前端一变它就变。
// 有这个才能回答「我手机上看到的到底是不是最新的」——这个问题问过两次，
// 两次都只能靠猜，因为版本号在任何地方都不可见。
// EmbeddedBuild 报出这个二进制里嵌着的前端主脚本名，空串表示没嵌进去。
//
// `pe doctor` 用它确认构建是完整的：少了前端，服务照常启动、接口照常应答，
// 只是网页一片空白——而这正是发布流水线最容易出的错
// （忘了在 go build 之前跑 npm build）。
func EmbeddedBuild() string {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return ""
	}
	return frontendBuild(dist)
}

func frontendBuild(dist fs.FS) string {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return ""
	}
	m := mainScriptRe.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return string(m[1])
}

var mainScriptRe = regexp.MustCompile(`assets/(index-[A-Za-z0-9_-]+\.js)`)

func serveIndex(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		// 新克隆的仓库里 dist/ 只有一个 .gitkeep。直接说清楚该敲什么，
		// 比让人对着白屏猜强。
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`<!doctype html><meta charset="utf-8">
<title>前端未构建</title>
<body style="font:16px/1.7 system-ui;max-width:40em;margin:15vh auto;padding:0 1.5em">
<h1>前端还没构建</h1>
<p>这个二进制是在没有前端产物的情况下编译的（<code>web/dist/</code> 是空的）。
后端接口都能用，只是没有界面。</p>
<p>在仓库根目录执行：</p>
<pre style="background:#f4f4f4;padding:1em;border-radius:6px">make build</pre>
<p>它会先跑 <code>npm run build</code> 生成前端，再把产物嵌进二进制。
只跑 <code>go build</code> 是不够的。</p>
</body>`))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

// recoverPanics 把 handler 里的 panic 变成一个说得清的 500。
//
// net/http 自己会 recover 并保住进程，但它的做法是直接掐断连接——
// 前端只看到「Failed to fetch」，既不知道是服务端崩了，也拿不到任何线索。
// 实测踩过一次：某个字段忘了初始化，前端排查了半天以为是网络问题。
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("处理 %s %s 时发生内部错误: %v\n%s",
					r.Method, r.URL.Path, v, debug.Stack())
				writeError(w, http.StatusInternalServerError, "服务端内部错误，详情见服务端日志")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 采集与鉴权之外的请求量很大（资源、轮询），只记有意义的动作。
		if r.Method != http.MethodGet || strings.HasPrefix(r.URL.Path, "/api/v1/ingest") {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

// ── 响应helpers ───────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("写响应失败: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
