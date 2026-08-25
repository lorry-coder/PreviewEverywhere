package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"previeweverywhere/internal/store"
)

// 反馈的四个接口。写操作之后都要重写数据目录下的 feedback.md——
// 那份文件是投影，必须跟着变，否则「打开文件就能看」就成了句空话。

const maxFeedbackBody = 8 << 10 // 8KB：够写清一个问题，也挡住误贴一整篇日志
const maxFeedbackEnv = 16 << 10

func (s *Server) handleCreateFeedback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body     string `json:"body"`
		DocID    int64  `json:"docId"`
		DocTitle string `json:"docTitle"`
		Route    string `json:"route"`
		// Env 是前端采集的环境快照，原样透传。这里不解析它的结构：
		// 里面的字段会随浏览器演进增减，服务端不该跟着改。
		Env json.RawMessage `json:"env"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if len([]rune(body.Body)) > maxFeedbackBody {
		writeError(w, http.StatusBadRequest, "反馈内容太长了")
		return
	}
	env := string(body.Env)
	if len(env) > maxFeedbackEnv {
		env = ""
	}

	fb, err := s.st.AddFeedback(store.NewFeedback{
		Body: body.Body, DocID: body.DocID, DocTitle: body.DocTitle,
		Route: body.Route, Env: env,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.syncFeedbackFile()
	writeJSON(w, http.StatusOK, fb)
}

func (s *Server) handleListFeedback(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !store.ValidFeedbackStatus(status) {
		writeError(w, http.StatusBadRequest, "未知状态")
		return
	}
	list, err := s.st.ListFeedback(status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePatchFeedback(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Status     string `json:"status"`
		Resolution string `json:"resolution"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	fb, err := s.st.SetFeedbackStatus(id, body.Status, body.Resolution)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "反馈不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.syncFeedbackFile()
	writeJSON(w, http.StatusOK, fb)
}

func (s *Server) handleDeleteFeedback(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := s.st.DeleteFeedback(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "反馈不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.syncFeedbackFile()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// syncFeedbackFile 重写投影文件。写不成不算致命——数据库才是事实来源，
// 所以只记一行日志，不把一次成功的反馈变成失败。
func (s *Server) syncFeedbackFile() {
	if s.dataDir == "" {
		return
	}
	if err := s.st.WriteFeedbackFile(s.dataDir); err != nil {
		logFeedbackFileError(err)
	}
}
