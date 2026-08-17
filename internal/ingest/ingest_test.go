package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"previeweverywhere/internal/config"
	"previeweverywhere/internal/store"
)

func newPipeline(t *testing.T) (*Pipeline, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开 store 失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Ignore: config.DefaultIgnore}
	return New(st, cfg), st
}

// fakeRepo 造一个带 .git 的目录，模拟真实的项目布局。
func fakeRepo(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// agent 通常把报告写在 <仓库>/docs/ 下。归属应该落到仓库，而不是「docs」。
func TestProjectAttributionWalksUpToRepo(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "auth-refactor")
	doc := filepath.Join(root, "docs", "risk.md")
	write(t, doc, "# 迁移风险评估\n\n正文。\n")

	if _, err := p.Ingest(Source{Path: doc}); err != nil {
		t.Fatalf("采集失败: %v", err)
	}

	projects, err := st.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "auth-refactor" {
		t.Fatalf("项目应归到仓库名，实得 %+v", projects)
	}

	docs, err := st.ListDocs(store.DocFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("应有 1 篇文档，实得 %d", len(docs))
	}
	if docs[0].SourceKey != "docs/risk.md" {
		t.Errorf("source_key 应是仓库内相对路径，实得 %q", docs[0].SourceKey)
	}
	if docs[0].Title != "迁移风险评估" {
		t.Errorf("标题应取自 H1，实得 %q", docs[0].Title)
	}
}

// agent 反复写同一份文件是常态，内容没变就必须一路挡掉。
func TestUnchangedContentIsSkipped(t *testing.T) {
	p, _ := newPipeline(t)
	root := fakeRepo(t, "ci")
	doc := filepath.Join(root, "docs", "build.md")
	write(t, doc, "# 构建\n\n第一版。\n")

	first, err := p.Ingest(Source{Path: doc})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || !first.NewDoc || first.Seq != 1 {
		t.Fatalf("首次采集应产生 v1，实得 %+v", first)
	}

	again, err := p.Ingest(Source{Path: doc})
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Error("内容未变时不应产生新版本")
	}

	write(t, doc, "# 构建\n\n第二版，内容改了。\n")
	third, err := p.Ingest(Source{Path: doc})
	if err != nil {
		t.Fatal(err)
	}
	if !third.Changed || third.NewDoc || third.Seq != 2 {
		t.Fatalf("内容变化应产生 v2 且不算新文档，实得 %+v", third)
	}
}

func TestRelativeImageIsCopiedIntoBlobs(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "infra")
	write(t, filepath.Join(root, "docs", "img", "arch.png"), "\x89PNG\r\n\x1a\nfake")
	doc := filepath.Join(root, "docs", "plan.md")
	write(t, doc, "# 方案\n\n![架构图](./img/arch.png)\n")

	res, err := p.Ingest(Source{Path: doc})
	if err != nil {
		t.Fatalf("采集失败: %v", err)
	}

	detail, err := st.GetDoc(res.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.HTML, "/api/v1/asset/") {
		t.Fatalf("图片引用未被改写成平台内 URL:\n%s", detail.HTML)
	}
	// 取出 sha 验证 blob 真的落盘了——手机上能不能看到图全靠这一步。
	i := strings.Index(detail.HTML, "/api/v1/asset/")
	rest := detail.HTML[i+len("/api/v1/asset/"):]
	sha := rest[:strings.IndexAny(rest, `"'`)]
	if _, err := st.ReadBlob(sha); err != nil {
		t.Errorf("blob %s 未落盘: %v", sha, err)
	}
}

// 文档内容是不可信输入：不能让一句 ![](../../../etc/passwd) 把任意文件
// 复制进 blobs/ 再通过 HTTP 发出去。
func TestAssetPathTraversalIsRejected(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "proj")
	outside := filepath.Join(filepath.Dir(root), "secret.png")
	write(t, outside, "绝密")

	doc := filepath.Join(root, "docs", "evil.md")
	write(t, doc, "# 标题\n\n![偷](../../secret.png)\n")

	res, err := p.Ingest(Source{Path: doc})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetDoc(res.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail.HTML, "/api/v1/asset/") {
		t.Errorf("越界引用不应被本地化:\n%s", detail.HTML)
	}
	if !strings.Contains(detail.HTML, "data-pe-missing") {
		t.Error("被拒绝的引用应打上缺失标记")
	}
}

func TestFrontMatterOverridesProjectAndTags(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "monorepo")
	doc := filepath.Join(root, "docs", "note.md")
	write(t, doc, "---\nproject: 支付重构\ntags: [风险, 待复核]\n---\n\n# 备注\n\n正文。\n")

	if _, err := p.Ingest(Source{Path: doc}); err != nil {
		t.Fatal(err)
	}
	docs, err := st.ListDocs(store.DocFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ProjectName != "支付重构" {
		t.Fatalf("front-matter 的 project 应覆盖自动判定，实得 %+v", docs)
	}
	if len(docs[0].Tags) != 2 {
		t.Errorf("front-matter 标签应入库，实得 %v", docs[0].Tags)
	}

	// 按标签过滤应该能筛出来。
	filtered, err := st.ListDocs(store.DocFilter{Tag: "待复核"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 {
		t.Errorf("按标签检索应命中 1 篇，实得 %d", len(filtered))
	}
}

// 手动打的标签不能被 agent 的下一次生成冲掉。
func TestManualTagsSurviveRegeneration(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "proj")
	doc := filepath.Join(root, "docs", "a.md")
	write(t, doc, "---\ntags: [自动]\n---\n\n# 标题\n\n第一版。\n")

	res, err := p.Ingest(Source{Path: doc})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟用户手动打一个标签
	if _, err := st.DB.Exec(`INSERT INTO tag (name) VALUES ('待复核')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(
		`INSERT INTO doc_tag (doc_id, tag_id, source) SELECT ?, id, 'manual' FROM tag WHERE name='待复核'`,
		res.DocID); err != nil {
		t.Fatal(err)
	}

	write(t, doc, "---\ntags: [自动]\n---\n\n# 标题\n\n第二版，agent 重写了。\n")
	if _, err := p.Ingest(Source{Path: doc}); err != nil {
		t.Fatal(err)
	}

	detail, err := st.GetDoc(res.DocID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tag := range detail.Tags {
		if tag == "待复核" {
			found = true
		}
	}
	if !found {
		t.Errorf("手动标签应在重新生成后存活，实得 %v", detail.Tags)
	}
}

func TestPushedContentWithoutPath(t *testing.T) {
	p, st := newPipeline(t)
	res, err := p.Ingest(Source{
		Content:  []byte("# 推送的报告\n\n正文内容。\n"),
		Filename: "report.md",
		Project:  "远程",
		Tags:     []string{"推送"},
	})
	if err != nil {
		t.Fatalf("推送采集失败: %v", err)
	}
	detail, err := st.GetDoc(res.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ProjectName != "远程" || detail.Title != "推送的报告" {
		t.Errorf("推送文档元数据错误: %+v", detail.Doc)
	}
	if detail.SourceKey != "report.md" {
		t.Errorf("无路径时 source_key 应取文件名，实得 %q", detail.SourceKey)
	}
}

// 新版本入库意味着有新东西可读，未读状态要被重置。
func TestNewVersionResetsReadState(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "proj")
	doc := filepath.Join(root, "docs", "a.md")
	write(t, doc, "# 标题\n\n第一版。\n")

	res, _ := p.Ingest(Source{Path: doc})
	if err := st.MarkRead(res.DocID, 1.0, true); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.GetDoc(res.DocID); !d.Read {
		t.Fatal("标记已读没生效")
	}

	write(t, doc, "# 标题\n\n第二版。\n")
	if _, err := p.Ingest(Source{Path: doc}); err != nil {
		t.Fatal(err)
	}
	d, _ := st.GetDoc(res.DocID)
	if d.Read {
		t.Error("产生新版本后应重置为未读")
	}
}

// 用户手动删掉一个来自 front-matter 的标签之后，agent 重新生成同一份文档
// 不该把它又加回来。没有墓碑机制的话，这个标签会每跑一次复活一次。
func TestRemovedFrontMatterTagStaysRemoved(t *testing.T) {
	p, st := newPipeline(t)
	root := fakeRepo(t, "proj")
	doc := filepath.Join(root, "docs", "a.md")
	write(t, doc, "---\ntags: [风险, 待复核]\n---\n\n# 标题\n\n第一版。\n")

	res, err := p.Ingest(Source{Path: doc})
	if err != nil {
		t.Fatal(err)
	}
	// 用户在界面上把「待复核」去掉，只留「风险」
	if _, err := st.SetDocTags(res.DocID, []string{"风险"}); err != nil {
		t.Fatal(err)
	}

	// agent 重新生成，front-matter 一字未改
	write(t, doc, "---\ntags: [风险, 待复核]\n---\n\n# 标题\n\n第二版，agent 重写了。\n")
	if _, err := p.Ingest(Source{Path: doc}); err != nil {
		t.Fatal(err)
	}

	detail, err := st.GetDoc(res.DocID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range detail.Tags {
		if tag == "待复核" {
			t.Fatalf("被手动删掉的 front-matter 标签复活了：%v", detail.Tags)
		}
	}
	if len(detail.Tags) != 1 || detail.Tags[0] != "风险" {
		t.Errorf("保留的标签应只有「风险」，实得 %v", detail.Tags)
	}
}

// 管道推送没有文件名。若统一兜底成同一个名字，连推两篇会让第二篇
// 把第一篇覆盖成新版本——两份不同的报告变成一份的两个版本。
func TestPipedPushesWithoutFilenameStaySeparate(t *testing.T) {
	p, st := newPipeline(t)

	first, err := p.Ingest(Source{Content: []byte("# 夜间巡检结论\n\n磁盘水位正常。\n"), Project: "infra"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Ingest(Source{Content: []byte("# 夜间巡检明细\n\n逐盘数据见附表。\n"), Project: "infra"})
	if err != nil {
		t.Fatal(err)
	}

	if first.DocID == second.DocID {
		t.Fatal("两篇标题不同的管道推送被并成了同一篇文档")
	}
	if !second.NewDoc {
		t.Error("第二篇应当是新文档，而不是第一篇的新版本")
	}
	docs, err := st.ListDocs(store.DocFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("应有 2 篇文档，实得 %d", len(docs))
	}

	// 反过来：同一份内容重复推送仍应是「更新」，不能每次都新增一篇。
	again, err := p.Ingest(Source{Content: []byte("# 夜间巡检结论\n\n磁盘水位已回落。\n"), Project: "infra"})
	if err != nil {
		t.Fatal(err)
	}
	if again.DocID != first.DocID {
		t.Error("同标题的重复推送应当更新原文档，而不是新增")
	}
	if again.Seq != 2 {
		t.Errorf("应产生 v2，实得 v%d", again.Seq)
	}
}

// 不是每个项目都是 git 仓库。归属退化到取目录名时，必须跳过 docs/source
// 这类通用名一直往上找，否则三个不同仓库的 docs/ 会被并成同一个「docs」项目。
func TestGenericDirNamesAreNotUsedAsProjectName(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		rel  string
		want string
	}{
		{"OriannaCore/docs/spec.md", "OriannaCore"},
		{"PaperForge/docs/ARCH.md", "PaperForge"},
		{"3dgs/docs/source/lesson.md", "3dgs"},
		{"MyTool/.agent/run.md", "MyTool"},
		{"PlainProject/report.md", "PlainProject"},
	}
	for _, c := range cases {
		full := filepath.Join(base, c.rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := DetectProject(full).Name; got != c.want {
			t.Errorf("%s 应归到 %q，实得 %q", c.rel, c.want, got)
		}
	}
}

// 目录名里混进零宽字符时（真实碰到过 U+200C），项目名要清理掉。
func TestZeroWidthCharsStrippedFromProjectName(t *testing.T) {
	base := t.TempDir()
	full := filepath.Join(base, "Orianna‌Core", "docs", "a.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectProject(full).Name; got != "OriannaCore" {
		t.Errorf("零宽字符应被剔除，实得 %q", got)
	}
}

// 上层游离的 .git 不该劫持归属判定。
// 真实碰到过：/tmp 下留了个空 .git，临时目录里的每篇文档就都归到「tmp」。
// 有人把家目录纳入 git 管理时，后果是所有项目都归到家目录名下。
func TestStrayGitAboveDoesNotHijackAttribution(t *testing.T) {
	// 直接在 /tmp 下造：t.TempDir() 本身就在 /tmp 里，
	// 而 /tmp 正是最典型的「上层有游离 .git」的地方。
	base := t.TempDir()
	full := filepath.Join(base, "RealProject", "docs", "a.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}

	got := DetectProject(full).Name
	if got == "tmp" || got == filepath.Base(os.TempDir()) {
		t.Fatalf("归属被上层游离标记劫持了，实得 %q", got)
	}
	if got != "RealProject" {
		t.Errorf("应归到 RealProject，实得 %q", got)
	}
}

// 家目录本身不能当项目根。
func TestHomeIsNotAProjectRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("拿不到家目录")
	}
	if !isBadProjectRoot(home) {
		t.Error("家目录不该被当作项目根")
	}
	for _, d := range []string{"/", "/tmp", "/home", "/usr"} {
		if !isBadProjectRoot(d) {
			t.Errorf("%s 不该被当作项目根", d)
		}
	}
	if isBadProjectRoot("/home/someone/Code/myproj") {
		t.Error("正常的项目目录被误判成系统目录了")
	}
}

// 跨机推送时服务端看不到对方的磁盘，被引用的图片只能随正文一起送过来。
// 少了这条路，远程部署的每篇带图文档都是一张无声的坏图。
func TestUploadedAssetsAreUsedWhenNoLocalPath(t *testing.T) {
	p, st := newPipeline(t)
	res, err := p.Ingest(Source{
		Content:     []byte("# 远程报告\n\n![图表](./img/chart.png)\n"),
		Filename:    "b.md",
		ProjectHint: "auth-refactor",
		Assets:      map[string][]byte{"./img/chart.png": []byte("\x89PNG\r\n\x1a\nfake")},
	})
	if err != nil {
		t.Fatal(err)
	}

	detail, err := st.GetDoc(res.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ProjectName != "auth-refactor" {
		t.Errorf("projectHint 应决定归属，实得 %q", detail.ProjectName)
	}
	if !strings.Contains(detail.HTML, "/api/v1/asset/") {
		t.Fatalf("随附的图片应被收进平台:\n%s", detail.HTML)
	}
	i := strings.Index(detail.HTML, "/api/v1/asset/")
	rest := detail.HTML[i+len("/api/v1/asset/"):]
	sha := rest[:strings.IndexAny(rest, `"'`)]
	if _, err := st.ReadBlob(sha); err != nil {
		t.Errorf("blob 没落盘: %v", err)
	}
}

// 既拿不到磁盘、也没随附图片时，要显式标成缺失。
// 早先这种情况直接原样保留引用，页面上就是一张没有任何解释的坏图。
func TestUnresolvableImageIsMarkedMissing(t *testing.T) {
	p, st := newPipeline(t)
	res, err := p.Ingest(Source{
		Content:  []byte("# 报告\n\n![图表](./img/gone.png)\n"),
		Filename: "b.md",
		Project:  "远程",
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetDoc(res.DocID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.HTML, "data-pe-missing") {
		t.Errorf("找不到的图片应被标成缺失:\n%s", detail.HTML)
	}
}
