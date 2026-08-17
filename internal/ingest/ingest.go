// Package ingest 是所有采集通道汇入的那条公共管线。
//
// 文件监听、命令行推送、HTTP 接口、Claude Code hook、MCP —— 入口只决定元数据
// 从哪来（项目、标签、标题、所属运行），从「归属判定」往后走的是同一条路径。
// 新增一条通道等于新增一个调用方，不碰管线本身。
package ingest

import (
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/render"
	"previeweverywhere/internal/store"
)

const (
	// 超过这个大小的「文档」基本不是给人读的，多半是日志或数据转储。
	MaxDocSize = 8 << 20
	// 单个附件上限。
	MaxAssetSize = 24 << 20
)

// assetExts 限定了会被复制进 blobs/ 的附件类型。
// 白名单而非黑名单：文档里的相对引用是不可信输入，不该由它决定我们读什么文件。
var assetExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".svg": true, ".avif": true, ".bmp": true, ".ico": true,
}

// IsAssetExt 判断某个扩展名是否属于允许收取的附件类型。
// 客户端跨机推送时要做同样的判断，共用这一份免得两边漂移。
func IsAssetExt(ext string) bool {
	return assetExts[strings.ToLower(ext)]
}

// Source 描述一次采集请求。Path 与 Content 二选一。
type Source struct {
	Path     string // 本机文件路径
	Content  []byte // 直接推送的内容
	Filename string // Content 模式下用于判断类型与兜底标题

	// 以下均为可选的显式覆盖，优先于自动判定与 front-matter。
	Project string
	// ProjectHint 是「兜底建议」而非覆盖：只在显式 Project 和 front-matter
	// 都没给出时才采用。hook 在远端推送时用它带上本地算出的项目名，
	// 同时不夺走文档里 front-matter 的话语权。
	ProjectHint string
	SourceKey   string
	Title       string
	Tags        []string
	Run         string
	RunLabel    string

	// Assets 是跨机推送时随正文一起送来的图片，键是文档里写的原始引用。
	// 服务端看不到对方的文件系统，只能靠这个把图收进来。
	Assets map[string][]byte
}

// Event 在文档成功入库后发出，供 SSE 推给在线的阅读端。
type Event struct {
	DocID   int64  `json:"docId"`
	Title   string `json:"title"`
	Project string `json:"project"`
	Seq     int    `json:"seq"`
	NewDoc  bool   `json:"newDoc"`
}

type Pipeline struct {
	st  *store.Store
	cfg *config.Config
	// cdnClient 用来抓取 agent HTML 引用的 CDN 库。提出来是为了能在测试里
	// 换成信任自签证书的客户端——生产路径要求 https，测试服务器也就必须是 TLS。
	cdnClient *http.Client

	mu       sync.Mutex
	handlers []func(Event)
}

func New(st *store.Store, cfg *config.Config) *Pipeline {
	return &Pipeline{st: st, cfg: cfg, cdnClient: &http.Client{Timeout: cdnTimeout}}
}

// OnIngest 注册一个入库回调。HTTP 层用它把新文档实时推给手机。
func (p *Pipeline) OnIngest(fn func(Event)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers = append(p.handlers, fn)
}

func (p *Pipeline) emit(e Event) {
	p.mu.Lock()
	handlers := append([]func(Event){}, p.handlers...)
	p.mu.Unlock()
	for _, fn := range handlers {
		fn(e)
	}
}

// Ingest 跑完整条管线：归属判定 → 去重 → 资源本地化 → 渲染 → 落库。
//
// 这里兜住 panic 并转成普通错误：管线处理的是 agent 生成的任意 md/html，
// 输入完全不可控，而它跑在监听器的后台 goroutine 里——
// 一篇畸形文档不该把整个服务带走，尤其是启动扫描时会一口气过成千上万个文件。
func (p *Pipeline) Ingest(src Source) (res store.SaveResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			name := src.Path
			if name == "" {
				name = src.Filename
			}
			log.Printf("采集 %s 时发生内部错误，已跳过这一篇: %v\n%s", name, r, debug.Stack())
			err = fmt.Errorf("处理 %s 时发生内部错误: %v", name, r)
		}
	}()

	raw, filename, err := readSource(src)
	if err != nil {
		return res, err
	}
	if len(raw) > MaxDocSize {
		return res, fmt.Errorf("%s 超过 %d MB，跳过", filename, MaxDocSize>>20)
	}

	kind := render.KindForPath(filename)

	// front-matter 要在归属判定之前解析：它可能指定 project。
	// 对 HTML 文档这一步会原样返回，无副作用。
	var fm render.FrontMatter
	if kind == "markdown" {
		fm, _ = render.SplitFrontMatter(raw)
	}

	ref := resolveProject(src, fm)
	projectID, err := p.st.EnsureProject(ref.Slug, ref.Name, ref.Root)
	if err != nil {
		return res, err
	}

	sourceKey := resolveSourceKey(src, ref, filename, raw, fm)
	contentSha := store.Sha256Hex(raw)

	// 在渲染之前就挡掉没有变化的内容。agent 反复写同一份文件是常态，
	// 这条快速路径省掉的是整条渲染管线，而不只是一次数据库写入。
	if head, err := p.st.HeadSha(projectID, sourceKey); err == nil && head == contentSha {
		return store.SaveResult{DocID: 0}, nil
	}

	baseDir := ""
	if src.Path != "" {
		baseDir = filepath.Dir(src.Path)
	}

	result, err := render.Render(raw, render.Options{
		Kind:          kind,
		FallbackTitle: titleFromFilename(filename),
		AssetResolver: p.assetResolver(baseDir, ref.Root, src.Assets),
	})
	if err != nil {
		return res, fmt.Errorf("渲染 %s 失败: %w", filename, err)
	}

	rawMime := "text/markdown; charset=utf-8"
	if kind == "html" {
		rawMime = "text/html; charset=utf-8"
	}
	rawBlob, err := p.st.PutBlob(raw, rawMime)
	if err != nil {
		return res, err
	}

	// 原样模式的 HTML 会被送进 sandbox iframe，那里发不出带 Cookie 的请求，
	// 也可能根本没网。把外部依赖内联进来，存成另一份专供渲染的 blob。
	serveBlob := ""
	if kind == "html" && result.RenderMode == "raw" && p.cfg.ShouldLocalizeCDN() {
		if localized, n := p.localizeHTML(raw, baseDir, ref.Root); n > 0 {
			if sha, err := p.st.PutBlob(localized, rawMime); err == nil {
				serveBlob = sha
				log.Printf("%s：内联了 %d 个外部依赖，离线也能看", sourceKey, n)
			}
		}
	}

	title := result.Title
	if strings.TrimSpace(src.Title) != "" {
		title = strings.TrimSpace(src.Title)
	}

	var runID int64
	if runExt := firstNonEmpty(src.Run, fm.Run); runExt != "" {
		if runID, err = p.st.EnsureRun(projectID, runExt, src.RunLabel); err != nil {
			return res, err
		}
	}

	tocJSON, _ := json.Marshal(result.TOC)
	blocksJSON, _ := json.Marshal(result.Blocks)

	res, err = p.st.SaveDoc(store.SaveDocInput{
		ProjectID:  projectID,
		RunID:      runID,
		SourceKey:  sourceKey,
		SourcePath: src.Path,
		Title:      title,
		Summary:    result.Summary,
		Kind:       result.Kind,
		RenderMode: result.RenderMode,
		ContentSha: contentSha,
		RawBlob:    rawBlob,
		ServeBlob:  serveBlob,
		HTML:       result.HTML,
		Plain:      result.Plain,
		TOC:        string(tocJSON),
		Blocks:     string(blocksJSON),
		Chars:      result.Chars,
		Bytes:      len(raw),
		Tags:       mergeTags(src.Tags, fm.Tags),
	})
	if err != nil {
		return res, err
	}

	if len(result.MissingAssets) > 0 {
		log.Printf("采集 %s：%d 个引用的图片没找到（%s）",
			sourceKey, len(result.MissingAssets), strings.Join(trunc(result.MissingAssets, 3), ", "))
	}
	if res.Changed {
		// 批注重定位是采集管线的一个阶段，不是打开文档时的客户端行为。
		// 一次计算，多端受益，而且手机上不必做模糊匹配。
		if !res.NewDoc {
			okN, movedN, orphanN, err := p.st.RelocateAnnotations(res.DocID, res.VersionID)
			if err != nil {
				log.Printf("重定位 %s 的批注失败: %v", sourceKey, err)
			} else if movedN+orphanN > 0 {
				log.Printf("%s 的批注：%d 条原位、%d 条已迁移、%d 条失联",
					sourceKey, okN, movedN, orphanN)
			}
		}
		p.emit(Event{DocID: res.DocID, Title: title, Project: ref.Name, Seq: res.Seq, NewDoc: res.NewDoc})
	}
	return res, nil
}

// assetResolver 把文档里的相对图片引用换成平台内的 URL，并把图片本身
// 复制进 blobs/。这一步容易漏，但漏了移动端阅读体验就废了一半——
// agent 写的 ![](./img/arch.png) 在手机上只会是一张裂图。
func (p *Pipeline) assetResolver(baseDir, projectRoot string, uploaded map[string][]byte) func(string) (string, bool) {
	if baseDir == "" && len(uploaded) == 0 {
		return nil
	}
	return func(ref string) (string, bool) {
		// 跨机推送随正文带来的图片优先：服务端本来就看不到对方的磁盘。
		if data, ok := uploaded[ref]; ok {
			mimeType := mime.TypeByExtension(strings.ToLower(path.Ext(ref)))
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			if sha, err := p.st.PutBlob(data, mimeType); err == nil {
				return "/api/v1/asset/" + sha, true
			}
		}
		if baseDir == "" {
			return "", false
		}
		clean := ref
		if i := strings.IndexAny(clean, "?#"); i >= 0 {
			clean = clean[:i]
		}
		if unescaped, err := url.PathUnescape(clean); err == nil {
			clean = unescaped
		}
		if clean == "" {
			return "", false
		}
		if !assetExts[strings.ToLower(path.Ext(clean))] {
			return "", false
		}

		abs := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(clean)))
		// 文档内容是不可信输入：不能让一句 ![](../../../../etc/shadow)
		// 把任意文件复制进 blobs/ 再通过 HTTP 发出去。
		if !withinRoot(abs, projectRoot, baseDir) {
			log.Printf("拒绝越界的资源引用: %s", ref)
			return "", false
		}

		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > MaxAssetSize {
			return "", false
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", false
		}
		mimeType := mime.TypeByExtension(strings.ToLower(path.Ext(clean)))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		sha, err := p.st.PutBlob(data, mimeType)
		if err != nil {
			return "", false
		}
		return "/api/v1/asset/" + sha, true
	}
}

// withinRoot 要求目标落在项目根目录内；项目根不明时退化为文档所在目录。
func withinRoot(target, projectRoot, baseDir string) bool {
	root := projectRoot
	if root == "" {
		root = baseDir
	}
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ── 各种判定 ──────────────────────────────────────────────────────

func readSource(src Source) ([]byte, string, error) {
	if src.Content != nil {
		return src.Content, src.Filename, nil
	}
	if src.Path == "" {
		return nil, "", fmt.Errorf("采集请求既没有文件路径也没有内容")
	}
	data, err := os.ReadFile(src.Path)
	if err != nil {
		return nil, "", fmt.Errorf("读取 %s 失败: %w", src.Path, err)
	}
	return data, src.Path, nil
}

func resolveProject(src Source, fm render.FrontMatter) ProjectRef {
	if name := firstNonEmpty(src.Project, fm.Project); name != "" {
		ref := ProjectRef{Slug: Slugify(name), Name: name}
		// 显式指定项目名时仍然沿用自动判定出的根目录，
		// 这样 source_key 依旧是干净的仓库内相对路径。
		if src.Path != "" {
			ref.Root = DetectProject(src.Path).Root
		}
		return ref
	}
	if src.Path != "" {
		return DetectProject(src.Path)
	}
	if hint := strings.TrimSpace(src.ProjectHint); hint != "" {
		return ProjectRef{Slug: Slugify(hint), Name: hint}
	}
	return ProjectRef{Slug: "推送", Name: "推送"}
}

// resolveSourceKey 决定文档的稳定身份，兜底顺序是：
// 显式 key → 仓库内相对路径 → 文件名 → 标题 → 内容哈希。
//
// 最后两级是给管道推送准备的：`cat a.md | pe push -` 没有文件名，
// 若统一兜底成同一个名字，连推两篇会让第二篇把第一篇覆盖成新版本。
// 用标题当身份也让重复推送同一份内容仍然是「更新」而不是「新增」。
func resolveSourceKey(src Source, ref ProjectRef, filename string, raw []byte, fm render.FrontMatter) string {
	if src.SourceKey != "" {
		return src.SourceKey
	}
	if src.Path != "" && ref.Root != "" {
		if rel, err := filepath.Rel(ref.Root, src.Path); err == nil &&
			!strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	if base := filepath.Base(filename); filename != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	if title := firstNonEmpty(src.Title, fm.Title, firstHeading(raw)); title != "" {
		return Slugify(title)
	}
	return store.Sha256Hex(raw)[:16]
}

// firstHeading 从原始 Markdown 里抓第一个 # 标题。
// 这一步发生在渲染之前，因为 source_key 要在去重判断时就定下来。
func firstHeading(raw []byte) string {
	lines := strings.SplitN(string(raw), "\n", 40)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "#"))
		}
	}
	return ""
}

func titleFromFilename(name string) string {
	base := filepath.Base(name)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// mergeTags 合并显式标签与 front-matter 标签，去重且保持顺序。
func mergeTags(explicit []string, fromFM []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, group := range [][]string{explicit, fromFM} {
		for _, t := range group {
			t = strings.TrimSpace(t)
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func trunc(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return in[:n]
}
