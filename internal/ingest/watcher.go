package ingest

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"previeweverywhere/internal/config"
)

const (
	// 监听目录数的软上限。写死一个数是不对的：内核的
	// fs.inotify.max_user_watches 常见值是 65536，而一个写死的
	// 4000 会在完全健康的机器上把用户拦住——真实碰到过，
	// 两个代码目录加起来 6730 个子目录，只占系统的 10%，却被自己的限制卡死。
	// 所以按系统实际值取一份，读不到时才退回保守默认。
	watchBudgetShare = 0.5
	fallbackMaxWatch = 8192
	minWatchBudget   = 2000
	maxWatchBudget   = 100000
	// agent 写文件常常是分多次 write，去抖避免对同一份文件反复跑管线。
	debounceDelay = 600 * time.Millisecond
	// 监听目录支持 glob（~/Code/*/docs）。glob 只在启动时展开一次的话，
	// 之后新建的匹配目录永远不会被监听——而「新建一个 docs 目录然后往里写」
	// 恰恰是最自然的用法。所以定期重新展开一次。
	rootRescanInterval = 30 * time.Second
)

type watchRoot struct {
	Path    string
	Project string
	Include []string
}

// WatchBudget 是 watchBudget 的导出版本，给 CLI 在 `watch add` 时
// 用同一套口径估算，避免帮助文本和实际行为对不上。
func WatchBudget() int { return watchBudget() }

// watchBudget 从内核的 fs.inotify.max_user_watches 里取一半。
// 取一半而不是全部：这个上限是整个用户共享的，编辑器、文件管理器
// 也在用同一份配额。
func watchBudget() int {
	raw, err := os.ReadFile("/proc/sys/fs/inotify/max_user_watches")
	if err != nil {
		return fallbackMaxWatch
	}
	limit, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || limit <= 0 {
		return fallbackMaxWatch
	}
	budget := int(float64(limit) * watchBudgetShare)
	if budget < minWatchBudget {
		budget = minWatchBudget
	}
	if budget > maxWatchBudget {
		budget = maxWatchBudget
	}
	return budget
}

type Watcher struct {
	pipe *Pipeline
	cfg  *config.Live
	fsw  *fsnotify.Watcher

	// budget 是本次运行允许监听的目录数上限，按系统实际配额算出来。
	budget int

	mu        sync.Mutex
	pending   map[string]*time.Timer
	watched   map[string]bool
	roots     []watchRoot
	failed    int
	truncated bool
	firstErr  string
	// skipped 记下几个因为超预算而没监听的目录。
	// 光说「超上限了」没用，得让人看见到底是什么在吃预算。
	skipped []string

	// nudge 是「配置换了，立刻重算监听集合」的信号。
	// 平时靠 30 秒的定时复扫就够，但人刚敲完 `pe source add` 之后
	// 等半分钟才生效，感觉上和「没生效」没区别。
	nudge chan struct{}
}

// Status 汇报监听健康度。inotify 句柄不足是 Linux 上最容易踩、
// 又最难察觉的问题，所以它必须能被 UI 显式看到。
type Status struct {
	Roots    []string `json:"roots"`
	Dirs     int      `json:"dirs"`
	Failed   int      `json:"failed"`
	Degraded bool     `json:"degraded"`
	Message  string   `json:"message,omitempty"`
}

func NewWatcher(p *Pipeline, cfg *config.Live) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		pipe:    p,
		cfg:     cfg,
		fsw:     fsw,
		budget:  watchBudget(),
		pending: map[string]*time.Timer{},
		watched: map[string]bool{},
		nudge:   make(chan struct{}, 1),
	}, nil
}

func (w *Watcher) Run(ctx context.Context) error {
	w.roots = w.resolveRoots()
	if len(w.roots) == 0 {
		log.Printf("没有配置监听目录，只接受推送。用 `pe source add <目录>` 添加。")
	}
	for _, r := range w.roots {
		w.addTree(r.Path)
	}
	w.reportWatchHealth()

	go w.scanRoots(w.roots)

	rescan := time.NewTicker(rootRescanInterval)
	defer rescan.Stop()

	for {
		select {
		case <-ctx.Done():
			return w.fsw.Close()
		case <-rescan.C:
			w.refreshRoots()
		case <-w.nudge:
			w.refreshRoots()
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return nil
			}
			log.Printf("监听出错: %v", err)
		}
	}
}

// Reload 让监听器立刻按最新配置重算一遍监听集合。
// 配置换了之后调它，不必等定时复扫。
//
// 缓冲是 1 且满了就丢：重算本来就是幂等的，排队等第二次没有意义。
func (w *Watcher) Reload() {
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

func (w *Watcher) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()

	st := Status{Dirs: len(w.watched), Failed: w.failed}
	for _, r := range w.roots {
		st.Roots = append(st.Roots, r.Path)
	}
	switch {
	case w.truncated:
		st.Degraded = true
		st.Message = "监听目录数达到上限 " + itoa(w.budget) +
			"，部分子目录未被监听。把体积大的子目录加进 pe.toml 的 ignore，或提高 fs.inotify.max_user_watches。"
	case w.failed > 0:
		st.Degraded = true
		st.Message = "有 " + itoa(w.failed) + " 个目录无法监听：" + w.firstErr
	}
	return st
}

// ── 监听树 ────────────────────────────────────────────────────────

func (w *Watcher) resolveRoots() []watchRoot {
	out := []watchRoot{}
	for _, wc := range w.cfg.Get().Watch {
		pattern := expandHome(wc.Path)
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 && pathExists(pattern) {
			matches = []string{pattern}
		}
		include := wc.Include
		if len(include) == 0 {
			include = config.DefaultInclude
		}
		for _, m := range matches {
			abs, err := filepath.Abs(m)
			if err != nil {
				continue
			}
			if !pathExists(abs) {
				continue
			}
			out = append(out, watchRoot{Path: abs, Project: wc.Project, Include: include})
		}
	}
	return out
}

func (w *Watcher) addTree(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && (w.isIgnoredDir(d.Name()) || isBuildTree(p)) {
			return filepath.SkipDir
		}

		w.mu.Lock()
		if len(w.watched) >= w.budget {
			w.truncated = true
			if len(w.skipped) < 8 {
				w.skipped = append(w.skipped, p)
			}
			w.mu.Unlock()
			return filepath.SkipDir
		}
		already := w.watched[p]
		w.mu.Unlock()
		if already {
			return nil
		}

		if err := w.fsw.Add(p); err != nil {
			w.mu.Lock()
			w.failed++
			if w.firstErr == "" {
				w.firstErr = err.Error()
			}
			w.mu.Unlock()
			return nil
		}
		w.mu.Lock()
		w.watched[p] = true
		w.mu.Unlock()
		return nil
	})
}

// reportWatchHealth 把 inotify 的问题讲清楚，并给出可执行的修复办法。
func (w *Watcher) reportWatchHealth() {
	st := w.Status()
	log.Printf("已监听 %d 个目录（%d 个根）", st.Dirs, len(st.Roots))
	if !st.Degraded {
		return
	}
	log.Printf("⚠ %s", st.Message)
	// 光说「超了」帮不上忙，把吃掉预算的目录举几个例子出来。
	if len(w.skipped) > 0 {
		log.Printf("  最先被跳过的几个目录（多半就是元凶）：")
		for _, p := range w.skipped {
			log.Printf("    %s", p)
		}
		log.Printf("  把其中的公共父目录名加进 pe.toml 的 ignore 即可。")
	}
	if strings.Contains(w.firstErr, "no space left") || strings.Contains(w.firstErr, "ENOSPC") {
		log.Printf("  这通常不是磁盘满，而是 inotify 句柄用尽。两种解法：")
		log.Printf("  1) 把监听范围收窄，例如只监听 <仓库>/docs 而不是仓库根目录；")
		log.Printf("  2) 提高上限：sudo sysctl fs.inotify.max_user_watches=524288")
	}
}

func (w *Watcher) isIgnoredDir(name string) bool {
	if config.MatchIgnore(name, w.cfg.Get().Ignore) {
		return true
	}
	// 隐藏目录默认不进，但显式配成监听根的除外（上面的 p != root 已经保证了这点）。
	return strings.HasPrefix(name, ".")
}

// CountWatchDirs 按与监听器完全相同的规则数一遍子目录，
// 供 `pe watch add` 当场给出量级。必须复用同一套判断——
// 提示里的数字和实际监听数对不上，比不给数字更糟。
func CountWatchDirs(root string, ignore []string) (int, bool) {
	w := &Watcher{cfg: config.Static(&config.Config{Ignore: ignore})}
	const limit = 200000
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // 数不动的目录跳过就行，不是错误
		}
		if p != root && (w.isIgnoredDir(d.Name()) || isBuildTree(p)) {
			return filepath.SkipDir
		}
		if n++; n > limit {
			return filepath.SkipAll
		}
		return nil
	})
	return n, err == nil
}

// buildTreeMarkers 是「这整棵子树是构建产物」的确凿证据。
// 光靠目录名挡不住：真实碰到过 build_rerun/_deps/…/mimalloc_ep/docs/，
// 里面是几百页 doxygen HTML，名字既不叫 build 也不叫 dist，
// 却会把人真正要读的 agent 文档淹掉。
// 认标记文件比认名字可靠——构建系统自己写下的东西不会骗人。
var buildTreeMarkers = []string{
	"CMakeCache.txt",  // CMake
	"build.ninja",     // Ninja（含 meson）
	"CMakeFiles",      // 保险起见，CMakeCache 被删了也还在
	"_CPack_Packages", // CPack 打包输出
}

// IsBuildTree 是 isBuildTree 的导出版本，供 hook 走同一套判断。
func IsBuildTree(dir string) bool { return isBuildTree(dir) }

// isBuildTree 判断一个目录是不是构建树的根。命中就整棵跳过。
func isBuildTree(dir string) bool {
	for _, marker := range buildTreeMarkers {
		if _, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// ── 事件处理 ──────────────────────────────────────────────────────

func (w *Watcher) handleEvent(ev fsnotify.Event) {
	// 删除与改名一律忽略：平台是文件系统的下游，文件没了文档也该留着。
	if ev.Op&(fsnotify.Create|fsnotify.Write) == 0 {
		return
	}
	info, err := statPath(ev.Name)
	if err != nil {
		return
	}
	if info.IsDir() {
		if ev.Op&fsnotify.Create != 0 && !w.isIgnoredDir(filepath.Base(ev.Name)) {
			w.addTree(ev.Name)
		}
		return
	}
	if root, ok := w.matchRoot(ev.Name); ok {
		w.schedule(ev.Name, root)
	}
}

func (w *Watcher) schedule(path string, root watchRoot) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if t, ok := w.pending[path]; ok {
		t.Stop()
	}
	w.pending[path] = time.AfterFunc(debounceDelay, func() {
		w.mu.Lock()
		delete(w.pending, path)
		w.mu.Unlock()

		res, err := w.pipe.Ingest(Source{Path: path, Project: root.Project})
		if err != nil {
			log.Printf("采集 %s 失败: %v", path, err)
			return
		}
		if res.Changed {
			verb := "更新"
			if res.NewDoc {
				verb = "收入"
			}
			log.Printf("%s %s (v%d)", verb, filepath.Base(path), res.Seq)
		}
	})
}

// matchRoot 找出文件属于哪条监听规则，并校验它没落在被忽略的目录里。
func (w *Watcher) matchRoot(path string) (watchRoot, bool) {
	w.mu.Lock()
	roots := append([]watchRoot(nil), w.roots...)
	w.mu.Unlock()

	var best watchRoot
	found := false
	for _, r := range roots {
		rel, err := filepath.Rel(r.Path, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
			if part != "." && part != "" && w.isIgnoredDir(part) {
				return watchRoot{}, false
			}
		}
		if !found || len(r.Path) > len(best.Path) {
			best, found = r, true
		}
	}
	if !found || !matchesInclude(filepath.Base(path), best.Include) {
		return watchRoot{}, false
	}
	return best, true
}

func matchesInclude(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// refreshRoots 重新展开 glob，把新出现的目录纳入监听并补扫一遍。
// 没有这一步的话，服务启动之后才创建的 docs/ 目录会被永久忽略，
// 症状是「文档明明写进去了却始终不出现」，且没有任何报错。
func (w *Watcher) refreshRoots() {
	resolved := w.resolveRoots()
	live := make(map[string]bool, len(resolved))
	for _, r := range resolved {
		live[r.Path] = true
	}

	w.mu.Lock()
	known := make(map[string]bool, len(w.roots))
	for _, r := range w.roots {
		known[r.Path] = true
	}
	var added []watchRoot
	for _, r := range resolved {
		if !known[r.Path] {
			added = append(added, r)
		}
	}
	// 规则被删掉（`pe source rm`）或者目录本身没了，对应的根要退出监听。
	// 原先这里只加不减，于是删掉一条规则要重启服务才算数——
	// 而「删了却还在收」和「加了却没收」一样让人以为程序坏了。
	var dropped []string
	for _, r := range w.roots {
		if !live[r.Path] {
			dropped = append(dropped, r.Path)
		}
	}
	if len(dropped) > 0 {
		w.roots = resolved
	} else {
		w.roots = append(w.roots, added...)
	}
	w.mu.Unlock()

	if len(dropped) > 0 {
		for _, path := range dropped {
			log.Printf("不再监听: %s", path)
		}
		w.unwatchOrphans()
	}
	if len(added) == 0 {
		return
	}
	for _, r := range added {
		log.Printf("发现新的监听目录: %s", r.Path)
		w.addTree(r.Path)
	}
	w.scanRoots(added)
}

// unwatchOrphans 撤掉那些已经不属于任何一条规则的目录。
//
// 判据是「还有没有某个根是它的前缀」，而不是「它属于刚被删的那个根」——
// 两条规则可能重叠（同时监听 ~/Code 和 ~/Code/proj/docs），
// 按后者判会把仍然该监听的目录一起撤掉。
func (w *Watcher) unwatchOrphans() {
	w.mu.Lock()
	roots := append([]watchRoot(nil), w.roots...)
	var orphans []string
	for dir := range w.watched {
		covered := false
		for _, r := range roots {
			if rel, err := filepath.Rel(r.Path, dir); err == nil && !strings.HasPrefix(rel, "..") {
				covered = true
				break
			}
		}
		if !covered {
			orphans = append(orphans, dir)
		}
	}
	for _, dir := range orphans {
		delete(w.watched, dir)
	}
	// 超预算的记录一起清掉：撤掉一批目录之后，之前「放不下」的结论就不成立了。
	if len(orphans) > 0 {
		w.truncated = false
		w.skipped = nil
	}
	w.mu.Unlock()

	for _, dir := range orphans {
		w.fsw.Remove(dir) //nolint:errcheck // 目录可能已经没了，撤不掉也无所谓
	}
}

// scanRoots 把给定根目录下已有的文档补齐。内容没变的文件会在管线的
// sha 快速路径上被挡掉，所以重复扫描的代价很低。
func (w *Watcher) scanRoots(roots []watchRoot) {
	count := 0
	for _, root := range roots {
		filepath.WalkDir(root.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != root.Path && (w.isIgnoredDir(d.Name()) || isBuildTree(p)) {
					return filepath.SkipDir
				}
				return nil
			}
			if !matchesInclude(d.Name(), root.Include) {
				return nil
			}
			res, err := w.pipe.Ingest(Source{Path: p, Project: root.Project})
			if err != nil {
				log.Printf("扫描 %s 失败: %v", p, err)
				return nil
			}
			if res.Changed {
				count++
			}
			return nil
		})
	}
	if count > 0 {
		log.Printf("扫描收入 %d 篇文档", count)
	}
}
