package main

import (
	"fmt"
	"net/http"
	"time"

	"previeweverywhere/internal/config"
)

// hook 到底推不推得上去。
//
// 这个判断存在的理由，是一次真实的故障：hook 装上了、`pe agent status` 说「已装」、
// `pe agent install` 说「已写入」，而它每次触发都在静默跳过——因为客户端配置里
// 的口令是空的。三个地方都说好，唯一说实话的 `pe doctor` 又把
// 「客户端配置 ✗」和「agent hook ✓」报成两条互不相干的项，
// 没人把因果连起来。
//
// hook 的设计原则是**绝不打断 agent**，所以它推不上去时只能安静地跳过。
// 正因为它自己永远不会喊，别的地方就必须替它喊。
//
// 关键是「推不上去」有两种，处理方式完全不同：
//
//	结构性的   没配口令、口令不对 —— 你不动手它永远好不了。
//	一时的     连不上 —— 很可能只是服务没起来，起来就好了。
//
// 混成一句「推不上去」会让人去修一个根本没坏的东西。

type pushState int

const (
	pushOK          pushState = iota // 配好了，也连得通
	pushNoToken                      // 没配口令：结构性
	pushBadToken                     // 口令不对：结构性
	pushUnreachable                  // 连不上：可能只是服务没跑
)

// broken 说这是不是「你不动手就永远好不了」的那种。
func (s pushState) broken() bool { return s == pushNoToken || s == pushBadToken }

// checkPush 拿当前的客户端配置去真的连一次。
// 返回状态和一句给人看的话（已经包含下一步该做什么）。
func checkPush() (pushState, string) {
	c, err := config.LoadClient()
	if err != nil {
		return pushNoToken, "读不了客户端配置：" + err.Error()
	}
	if c.Token == "" {
		return pushNoToken, "客户端没配口令。hook 会静默跳过，pe push 会报错。\n" +
			"配上：pe client set     （不知道口令就先 pe token rotate 换一个）"
	}

	req, err := http.NewRequest(http.MethodGet, c.Endpoint+"/api/v1/status", nil)
	if err != nil {
		return pushBadToken, "客户端配置里的地址不对：" + c.Endpoint
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return pushUnreachable, fmt.Sprintf(
			"现在连不上 %s —— 服务起来之后就好（配置本身没问题）", c.Endpoint)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return pushOK, c.Endpoint
	case http.StatusUnauthorized:
		return pushBadToken, fmt.Sprintf(
			"%s 说口令不对。hook 会静默跳过。\n换一个再配：pe token rotate && pe client set", c.Endpoint)
	default:
		return pushBadToken, fmt.Sprintf(
			"%s 返回 %s，那个地址后面像是别的服务", c.Endpoint, resp.Status)
	}
}
