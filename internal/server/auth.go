package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"previeweverywhere/internal/store"
)

const sessionCookie = "pe_session"

// 仍然是单用户模式，但凭据分成两种，因为它们服务于两类完全不同的东西：
//
//	主口令      给机器用（pe push / hook / MCP 走 Bearer）。只存 sha256。
//	设备会话    给人用。每台设备一行、一串自己的随机口令，见 store/device.go。
//
// 分开之前，登录 Cookie 里存的就是主口令本身，于是「想让手机也能看」
// 这件小事的代价是家里每台设备重新扫一遍码——因为主口令拿不回来，
// 只能换一个新的，而换新的会让所有登录一起失效。
//
// 旧的登录（Cookie 里是主口令）继续有效，不强迫任何人重新扫码。
// 它们会在下一次 `pe token rotate` 时失效，而配对过的设备不受影响。

func (s *Server) authed(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		// 配对出来的设备会话。放在前面，因为这是往后的主要路径。
		if s.st.CheckDevice(c.Value, r.Header.Get("User-Agent")) {
			return true
		}
		// 主口令直接当 Cookie —— 配对机制之前的旧登录。
		if s.cfg.Get().CheckToken(c.Value) {
			return true
		}
	}
	// 命令行与脚本走 Bearer，省得先换 Cookie。
	// 这里只认主口令：设备会话是给浏览器的，不该被拿去当机器凭据。
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return s.cfg.Get().CheckToken(strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
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
	if !s.cfg.Get().CheckToken(strings.TrimSpace(body.Token)) {
		writeError(w, http.StatusUnauthorized, "口令不对。用 `pe token` 重新生成一个。")
		return
	}
	setSessionCookie(w, strings.TrimSpace(body.Token))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePair 用一次性配对码换一个属于这台设备的长期会话。
//
// 刻意不需要鉴权：**配对码本身就是凭据**。这也正是它必须一次性 + 有有效期的原因。
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	token, dev, err := s.st.RedeemPairCode(body.Code, r.Header.Get("User-Agent"))
	if err != nil {
		if errors.Is(err, store.ErrPairCode) {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "device": dev})
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.st.ListDevices()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, devices)
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	name, err := s.st.RevokeDevice(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "没有这台设备")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}

func setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().AddDate(1, 0, 0),
		// 刻意不设 Secure：局域网里是 http，设了 Cookie 根本存不下来。
		// 走公网请用 Tailscale，而不是把这个端口暴露出去。
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
