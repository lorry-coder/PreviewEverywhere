package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const sessionCookie = "pe_session"

// 单用户模式：一个访问口令 + 一年有效的 HttpOnly Cookie。
// 手机扫一次二维码就再也不用输任何东西，这是「没时间待在电脑旁」这条
// 约束下唯一说得过去的登录体验。多用户要等到把 doc 上的阅读状态
// 和 annotation 的归属拆成关联表之后再说。

func (s *Server) authed(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && s.cfg.CheckToken(c.Value) {
		return true
	}
	// 命令行与脚本走 Bearer，省得先换 Cookie。
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return s.cfg.CheckToken(strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
	}
	return false
}

// requireAuth 包住所有需要鉴权的接口。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeError(w, http.StatusUnauthorized, "未登录。用手机扫 `pe token` 打印的二维码。")
			return
		}
		next(w, r)
	}
}

// handleSession 把访问口令换成长期 Cookie。前端在扫码落地后调它一次，
// 然后立刻把地址栏里的 token 抹掉。
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if !s.cfg.CheckToken(strings.TrimSpace(body.Token)) {
		writeError(w, http.StatusUnauthorized, "口令不对。用 `pe token` 重新生成一个。")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    strings.TrimSpace(body.Token),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().AddDate(1, 0, 0),
		// 刻意不设 Secure：局域网里是 http，设了 Cookie 根本存不下来。
		// 走公网请用 Tailscale，而不是把这个端口暴露出去。
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
