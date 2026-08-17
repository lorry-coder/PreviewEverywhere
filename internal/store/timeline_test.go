package store

import (
	"testing"
	"time"
)

// 时间线按「服务端本地日期」分组，而界面上的「今天 / 昨天」按浏览器时区显示。
// 两边时区不一致时，半夜前后写的文档会被归到昨天却标着「今天」。
//
// 部署在容器里尤其容易踩：容器默认 UTC，而你的手机是 UTC+8。
// 所以容器必须显式设 TZ——这个测试就是把这件事钉下来。
func TestGroupKeyIsTimezoneSensitive(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("加载时区失败（二进制应当内嵌 time/tzdata）: %v", err)
	}

	// 2026-08-16 07:00 +08:00 == 2026-08-15 23:00 UTC —— 两地不同日
	at := time.Date(2026, 8, 16, 7, 0, 0, 0, shanghai).Unix()

	inShanghai := groupKey(0, 1, at, shanghai)
	inUTC := groupKey(0, 1, at, time.UTC)

	if inShanghai == inUTC {
		t.Fatal("这个时刻在两个时区应当落在不同的日期分组")
	}
	if inShanghai != "day:2026-08-16:p1" {
		t.Errorf("上海时区应归到 08-16，实得 %s", inShanghai)
	}
	if inUTC != "day:2026-08-15:p1" {
		t.Errorf("UTC 应归到 08-15，实得 %s", inUTC)
	}
}

// 有 agent 会话 ID 时按会话分组，与时区无关。
func TestGroupKeyByRunIgnoresTimezone(t *testing.T) {
	at := time.Now().Unix()
	if groupKey(7, 1, at, time.UTC) != groupKey(7, 1, at, time.Local) {
		t.Error("按 run 分组不该受时区影响")
	}
}
