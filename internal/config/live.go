package config

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Live 是一份可以整体换掉的配置快照。
//
// 存在的理由：`pe watch add` 和 `pe token` 原先都打印「重启 pe serve 后生效」，
// 而这是「我明明配了却没进来」最常见的来源——配置改了没生效不会报错，
// 只是安安静静地什么都不发生。
//
// 做法是不让任何人长期持有 *Config。要读就 Get() 拿当下这一份，
// 换配置时整个指针原子替换。读方不用加锁，也不会读到改了一半的配置。
//
// 有一项做不到热更：Bind。端口是在 net.Listen 那一刻定下来的，
// 改它必须重启。所以那一项要如实说，而不是笼统地讲「改完要重启」。
type Live struct {
	dir string
	ptr atomic.Pointer[Config]

	mu    sync.Mutex
	subs  []func(*Config)
	stamp fileStamp
}

// fileStamp 是 pe.toml 的「有没有变过」的判据。
// 用 mtime + size 而不是内容哈希：这是每两秒一次的轮询，
// 一次 stat 就够，读整个文件没必要。
type fileStamp struct {
	mod  time.Time
	size int64
}

// NewLive 用一份已经载入的配置建立快照持有者。
func NewLive(dir string, cfg *Config) *Live {
	l := &Live{dir: dir}
	l.ptr.Store(cfg)
	l.stamp = statFile(filepath.Join(dir, "pe.toml"))
	return l
}

// Static 把一份固定配置包成 Live，给不需要热更的调用方用（测试、一次性工具）。
func Static(cfg *Config) *Live {
	l := &Live{}
	l.ptr.Store(cfg)
	return l
}

// Get 取当下这一份配置。拿到之后不要改它——它是共享的。
func (l *Live) Get() *Config { return l.ptr.Load() }

// Dir 是数据目录。
func (l *Live) Dir() string { return l.dir }

// OnReload 注册一个「配置换了」的回调。回调在持有者的锁外调用。
func (l *Live) OnReload(fn func(*Config)) {
	l.mu.Lock()
	l.subs = append(l.subs, fn)
	l.mu.Unlock()
}

// Reload 从盘上重读并换掉当前快照。
//
// Bind 刻意保持原值：它已经绑上去了，让快照里写着一个没在用的端口
// 只会让 `pe status` 报出一个假的地址。
func (l *Live) Reload() (*Config, error) {
	if l.dir == "" {
		return l.Get(), nil
	}
	next, err := Load(l.dir)
	if err != nil {
		return nil, err
	}
	next.Bind = l.Get().Bind
	l.ptr.Store(next)

	l.mu.Lock()
	l.stamp = statFile(filepath.Join(l.dir, "pe.toml"))
	subs := append([]func(*Config){}, l.subs...)
	l.mu.Unlock()

	for _, fn := range subs {
		fn(next)
	}
	return next, nil
}

// Changed 说 pe.toml 在盘上是不是变过了。
//
// 之所以要轮询而不是用 fsnotify：改这个文件的是**另一个进程**
// （`pe watch add` 在你另一个终端里跑），而它是截断重写而不是原子换名，
// 那种写法在各家 fsnotify 实现下的事件序列并不一致。stat 一下最省事也最稳。
func (l *Live) Changed() bool {
	if l.dir == "" {
		return false
	}
	now := statFile(filepath.Join(l.dir, "pe.toml"))
	l.mu.Lock()
	defer l.mu.Unlock()
	return now != l.stamp
}

func statFile(path string) fileStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{mod: st.ModTime(), size: st.Size()}
}
