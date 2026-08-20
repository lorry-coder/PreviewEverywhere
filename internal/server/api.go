package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"previeweverywhere/internal/ingest"
	"previeweverywhere/internal/search"
	"previeweverywhere/internal/store"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	total, unread, err := s.st.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{"total": total, "unread": unread}
	// 这个二进制携带的前端版本。前端拿它跟自己实际加载的脚本比对，
	// 不一致就说明浏览器还在用缓存里的旧前端——这件事此前完全不可见，
	// 只能靠「你是不是没重新编译」这种猜测来回答。
	out["build"] = s.build
	if s.watch != nil {
		out["watch"] = s.watch.Status()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.st.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.st.ListTags()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.DocFilter{
		Tag:    q.Get("tag"),
		Unread: q.Get("unread") == "1",
		Later:  q.Get("later") == "1",
		Limit:  atoiOr(q.Get("limit"), 200),
		Offset: atoiOr(q.Get("offset"), 0),
	}
	if v := q.Get("project"); v != "" {
		filter.ProjectID = int64(atoiOr(v, 0))
	}
	docs, err := s.st.ListDocs(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, docs)
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	detail, err := s.st.GetDoc(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "文档不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handlePatchDoc 目前只处理阅读状态。标签编辑与渲染模式切换在 P2/P4。
// handleDeleteDoc 删掉一篇文档。
//
// 默认留墓碑：源文件多半还在被监听的目录里，不留的话下次扫描原样收回，
// 删除按钮就成了假动作。带 ?forget=1 则不留——用于「我只是想清掉这条记录，
// 以后它再出现我还想要」。
func (s *Server) handleDeleteDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	tombstone := r.URL.Query().Get("forget") != "1"
	if err := s.st.DeleteDoc(id, tombstone); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "文档不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tombstone": tombstone})
}

func (s *Server) handlePatchDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		ReadRatio *float64 `json:"readRatio"`
		Read      *bool    `json:"read"`
		Later     *bool    `json:"later"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}

	if body.ReadRatio != nil || body.Read != nil {
		ratio := 0.0
		if body.ReadRatio != nil {
			ratio = *body.ReadRatio
		}
		read := ratio >= 0.9
		if body.Read != nil {
			read = *body.Read
		}
		if err := s.st.MarkRead(id, ratio, read); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if body.Later != nil {
		if err := s.st.SetLater(id, *body.Later); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	detail, err := s.st.GetDoc(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail.Doc)
}

// handleRaw 提供原样模式的 HTML，交给前端的 sandbox iframe 渲染。
//
// 前端那边只给 sandbox="allow-scripts"、不给 allow-same-origin，
// iframe 因此处于独立的不透明源：脚本能跑（图表画得出来），但读不到
// 父页面 DOM、拿不到 Cookie，也无法带凭证调这里的接口。
func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "versionId")
	if !ok {
		return
	}
	sha, mimeType, err := s.st.RawVersion(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := s.st.ReadBlob(sha)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "原文丢失")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
	w.Write(data)
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")
	if len(sha) != 64 || strings.ContainsAny(sha, "/.\\") {
		writeError(w, http.StatusBadRequest, "资源标识不合法")
		return
	}
	mimeType, path, err := s.st.BlobMeta(sha)
	if err != nil {
		writeError(w, http.StatusNotFound, "资源不存在")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "资源已丢失")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// SVG 直接导航访问时会以本站源渲染，脚本能拿到 Cookie。
	// 这条 CSP 把它彻底钉死；作为 <img> 用时本来就跑不了脚本，不受影响。
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	// 内容寻址的 URL 永远指向同一份字节，可以放心长缓存。
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, sha, info.ModTime(), f)
}

// handleIngest 是跨机器推送的入口，三种请求体都接受：
// multipart 表单（pe push / curl -F）、JSON（脚本）、裸 body（管道）。
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	src, err := parseIngestRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.pipe.Ingest(src)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"docId":   res.DocID,
		"seq":     res.Seq,
		"changed": res.Changed,
		"newDoc":  res.NewDoc,
		"url":     "/doc/" + strconv.FormatInt(res.DocID, 10),
	})
}

func parseIngestRequest(r *http.Request) (ingest.Source, error) {
	var src ingest.Source
	ct := r.Header.Get("Content-Type")

	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		if err := r.ParseMultipartForm(ingest.MaxDocSize); err != nil {
			return src, errors.New("表单解析失败")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return src, errors.New("缺少 file 字段")
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, ingest.MaxDocSize+1))
		if err != nil {
			return src, errors.New("读取上传内容失败")
		}
		src.Content = content
		src.Filename = header.Filename
		src.Assets = collectUploadedAssets(r)
		applyFormFields(&src, r)

	case strings.HasPrefix(ct, "application/json"):
		var body struct {
			Content     string   `json:"content"`
			Filename    string   `json:"filename"`
			Project     string   `json:"project"`
			ProjectHint string   `json:"projectHint"`
			SourceKey   string   `json:"sourceKey"`
			Title       string   `json:"title"`
			Tags        []string `json:"tags"`
			Run         string   `json:"run"`
			RunLabel    string   `json:"runLabel"`
			Path        string   `json:"path"`
			// Explicit 区分「人主动推的」和「自动通道扫到的」，
			// 只影响是否覆盖删除墓碑。
			Explicit bool `json:"explicit"`
			// 跨机推送时文档引用的图片，键是文档里写的原始引用，值是 base64。
			Assets map[string]string `json:"assets"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, ingest.MaxDocSize)).Decode(&body); err != nil {
			return src, errors.New("JSON 解析失败")
		}
		src = ingest.Source{
			Content: []byte(body.Content), Filename: body.Filename,
			Project: body.Project, ProjectHint: body.ProjectHint,
			SourceKey: body.SourceKey, Title: body.Title,
			Tags: body.Tags, Run: body.Run, RunLabel: body.RunLabel,
			Explicit: body.Explicit,
		}
		if len(body.Assets) > 0 {
			src.Assets = make(map[string][]byte, len(body.Assets))
			for ref, encoded := range body.Assets {
				if data, err := base64.StdEncoding.DecodeString(encoded); err == nil {
					src.Assets[ref] = data
				}
			}
		}
		// 服务端与推送端同机时，允许直接给路径——这样图片也能被本地化。
		if body.Path != "" && body.Content == "" {
			src.Content = nil
			src.Path = body.Path
		}

	default:
		content, err := io.ReadAll(io.LimitReader(r.Body, ingest.MaxDocSize+1))
		if err != nil {
			return src, errors.New("读取请求体失败")
		}
		src.Content = content
		src.Filename = r.URL.Query().Get("filename")
		applyFormFields(&src, r)
	}

	if len(src.Content) == 0 && src.Path == "" {
		return src, errors.New("内容为空")
	}
	return src, nil
}

// collectUploadedAssets 收下 multipart 里以 asset: 开头的附件。
// 跨机推送时文档引用的图片就是这样送过来的——服务端看不到对方的磁盘。
func collectUploadedAssets(r *http.Request) map[string][]byte {
	if r.MultipartForm == nil {
		return nil
	}
	out := map[string][]byte{}
	for field, headers := range r.MultipartForm.File {
		ref, ok := strings.CutPrefix(field, "asset:")
		if !ok || len(headers) == 0 {
			continue
		}
		f, err := headers[0].Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, 4<<20))
		f.Close()
		if err == nil && len(data) > 0 {
			out[ref] = data
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func applyFormFields(src *ingest.Source, r *http.Request) {
	get := func(k string) string {
		if v := r.FormValue(k); v != "" {
			return v
		}
		return r.URL.Query().Get(k)
	}
	if v := get("project"); v != "" {
		src.Project = v
	}
	if v := get("title"); v != "" {
		src.Title = v
	}
	if v := get("sourceKey"); v != "" {
		src.SourceKey = v
	}
	if v := get("projectHint"); v != "" {
		src.ProjectHint = v
	}
	if v := get("explicit"); v == "1" || v == "true" {
		src.Explicit = true
	}
	if v := get("run"); v != "" {
		src.Run = v
	}
	if v := get("runLabel"); v != "" {
		src.RunLabel = v
	}
	if v := get("filename"); v != "" && src.Filename == "" {
		src.Filename = v
	}
	// tags 既支持 tags=a,b 也支持重复的 tags 字段。
	raw := r.URL.Query()["tags"]
	if r.Form != nil {
		raw = append(raw, r.Form["tags"]...)
	}
	for _, group := range raw {
		for _, t := range strings.Split(group, ",") {
			if t = strings.TrimSpace(t); t != "" {
				src.Tags = append(src.Tags, t)
			}
		}
	}
}

// ── 小工具 ────────────────────────────────────────────────────────

func pathInt(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "标识不合法")
		return 0, false
	}
	return id, true
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// ── P2：检索、时间线、标签编辑 ────────────────────────────────────

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := search.Parse(r.URL.Query().Get("q"))
	hits, err := s.st.Search(q, atoiOr(r.URL.Query().Get("limit"), 60))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query": q,
		"hits":  hits,
	})
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	groups, err := s.st.Timeline(atoiOr(r.URL.Query().Get("limit"), 150))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) handleSetTags(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	tags, err := s.st.SetDocTags(id, body.Tags)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Server) handleRenameTag(w http.ResponseWriter, r *http.Request) {
	oldName := r.PathValue("name")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if err := s.st.RenameTag(oldName, body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── P3：批注、失联重挂、版本 diff ──────────────────────────────────

func (s *Server) handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	list, err := s.st.ListDocAnnotations(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateAnnotation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Kind     string `json:"kind"`
		Color    string `json:"color"`
		Body     string `json:"body"`
		Blk      string `json:"blk"`
		StartOff int    `json:"startOff"`
		EndOff   int    `json:"endOff"`
		Exact    string `json:"exact"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	ann, err := s.st.CreateAnnotation(store.NewAnnotation{
		DocID: id, Kind: body.Kind, Color: body.Color, Body: body.Body,
		Blk: body.Blk, StartOff: body.StartOff, EndOff: body.EndOff, Exact: body.Exact,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ann)
}

func (s *Server) handlePatchAnnotation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Kind  *string `json:"kind"`
		Color *string `json:"color"`
		Body  *string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	ann, err := s.st.UpdateAnnotation(id, body.Kind, body.Color, body.Body)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "批注不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ann)
}

func (s *Server) handleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := s.st.DeleteAnnotation(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRebindAnnotation 把失联批注重挂到新选区。
// 原文真的消失时任何算法都只能猜，这里是把判断权交回给人的地方。
func (s *Server) handleRebindAnnotation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Blk      string `json:"blk"`
		StartOff int    `json:"startOff"`
		EndOff   int    `json:"endOff"`
		Exact    string `json:"exact"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	ann, err := s.st.Rebind(id, body.Blk, body.StartOff, body.EndOff, body.Exact)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "批注不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ann)
}

// handleActionable 汇总所有待办与疑问。
// 这张清单是「在通勤路上读文档」的产出物，也可以直接喂给下一轮 agent。
func (s *Server) handleActionable(w http.ResponseWriter, r *http.Request) {
	var kinds []string
	for _, k := range strings.Split(r.URL.Query().Get("kind"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			kinds = append(kinds, k)
		}
	}
	list, err := s.st.ListActionable(kinds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()
	diff, err := s.st.DiffVersions(id, atoiOr(q.Get("from"), 0), atoiOr(q.Get("to"), 0))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, diff)
}
