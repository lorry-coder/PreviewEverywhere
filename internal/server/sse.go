package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// hub 是一个极简的 SSE 广播器。单用户场景下同时在线的客户端不会超过个位数，
// 所以不做背压和重连游标，订阅者跟不上就直接丢事件。
type hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
	// done 在服务准备退出时关闭，让所有 SSE 处理器自己返回。
	//
	// 没有它的话优雅退出根本走不完：http.Server.Shutdown 会等所有连接结束，
	// 而 SSE 按定义就是永不结束的长连接，于是必然卡到超时，
	// 然后把 context deadline exceeded 当成错误报出来。
	done chan struct{}
}

func newHub() *hub {
	return &hub{subs: map[chan []byte]struct{}{}, done: make(chan struct{})}
}

// close 通知所有在线的 SSE 连接收工。可重复调用。
func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.done: // 已经关过了
	default:
		close(h.done)
	}
}

func (h *hub) broadcast(event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload))

	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default: // 订阅者堵住了就跳过，不阻塞采集管线
		}
	}
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前环境不支持流式响应")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch := make(chan []byte, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
		close(ch)
	}()

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	// 手机上切走再切回来，中间的连接常常已经被系统掐掉。
	// 定期心跳让浏览器能及时察觉并触发重连。
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.done:
			// 服务要退出了。主动收尾，Shutdown 才能在等待期内走完。
			return
		case msg := <-ch:
			w.Write(msg)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
