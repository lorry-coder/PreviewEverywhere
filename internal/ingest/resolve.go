package ingest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// genericDirNames 是那些「不足以作为项目名」的目录名。
//
// agent 的产出几乎总在这类目录里。如果归属判定退化到直接取父目录名，
// 三个不同仓库的 docs/ 会全部并成一个叫「docs」的项目——不只是名字难看，
// 是把互不相干的文档混进了同一个桶。
var genericDirNames = map[string]bool{
	"docs": true, "doc": true, "documents": true, "document": true,
	"notes": true, "note": true, "reports": true, "report": true,
	"output": true, "outputs": true, "out": true, "tmp": true, "temp": true,
	"source": true, "sources": true, "src": true, "content": true,
	"wiki": true, "spec": true, "specs": true, "design": true, "designs": true,
	"agent": true, "agents": true, ".agent": true, ".agents": true, ".claude": true,
	"public": true, "static": true, "assets": true, "md": true, "markdown": true,
}

// badProjectRoots 是永远不该被当作「项目根」的目录。
//
// 向上找 .git / .pe.toml 时，一个游离在上层的标记会劫持所有归属判定：
// /tmp 下随便留一个空的 .git，临时目录里的每篇文档就都归到「tmp」；
// 有人把家目录纳入 git 管理的话，所有项目都会归到家目录名下。
// 碰到这些目录就继续往上走，而不是就地认定。
func isBadProjectRoot(dir string) bool {
	switch filepath.Clean(dir) {
	case "/", ".":
		return true
	case "/tmp", "/var/tmp", "/usr", "/etc", "/opt", "/srv", "/mnt", "/media", "/home":
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(dir) == filepath.Clean(home) {
		return true
	}
	return false
}

// ProjectRef 是一次归属判定的结果。
type ProjectRef struct {
	Slug string
	Name string
	Root string // 项目根目录；source_key 是相对于它的路径
}

// peMarker 是可选的项目标记文件，放在项目根目录下即可覆盖自动判定。
type peMarker struct {
	Project string `toml:"project"`
}

// DetectProject 判定一份文档属于哪个项目，顺序是：
// 向上找 .pe.toml → 向上找 .git → 退化为父目录名。
//
// 之所以要向上找而不是只看父目录，是因为 agent 常常把报告写在
// <仓库>/docs/ 或 <仓库>/.agent/ 里，直接取父目录名会得到一堆重名的
// 「docs」项目，而不是仓库名。
func DetectProject(filePath string) ProjectRef {
	dir := filepath.Dir(filePath)
	if !filepath.IsAbs(dir) {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}

	for depth := 0; depth < 40; depth++ {
		if isBadProjectRoot(dir) {
			// 这一层的标记不作数，但还要继续往上：真正的项目根可能更高。
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
			continue
		}
		if marker := filepath.Join(dir, ".pe.toml"); fileExists(marker) {
			var m peMarker
			name := filepath.Base(dir)
			if _, err := toml.DecodeFile(marker, &m); err == nil && strings.TrimSpace(m.Project) != "" {
				name = strings.TrimSpace(m.Project)
			}
			return ProjectRef{Slug: Slugify(name), Name: name, Root: dir}
		}
		if pathExists(filepath.Join(dir, ".git")) {
			name := filepath.Base(dir)
			return ProjectRef{Slug: Slugify(name), Name: name, Root: dir}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// 既没有 .pe.toml 也没有 .git（不是每个项目都是 git 仓库）。
	// 退化取目录名时，要跳过 docs/source 这类通用名一直往上找，
	// 否则不同仓库的文档会被并进同一个「docs」项目。
	root := filepath.Dir(filePath)
	name := filepath.Base(root)
	for i := 0; i < 6 && genericDirNames[strings.ToLower(name)]; i++ {
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
		name = filepath.Base(root)
	}
	name = cleanProjectName(name)
	if name == "" {
		name = "未归类"
	}
	return ProjectRef{Slug: Slugify(name), Name: name, Root: root}
}

// cleanProjectName 去掉目录名里混进来的零宽字符。
// 真实目录名里出现过 U+200C，肉眼看不出来，但会让项目名无法匹配。
func cleanProjectName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff':
			return -1
		}
		return r
	}, name)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return ""
	}
	return cleaned
}

// Slugify 生成项目 slug。中文字符会被保留（URL 里做百分号编码即可），
// 这样中文项目名不会退化成一串连字符。
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "未归类"
	}
	return out
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// expandHome 把开头的 ~ 展开成用户主目录，方便配置里直接写 ~/Code/*/docs。
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

func statPath(p string) (os.FileInfo, error) { return os.Stat(p) }

func itoa(n int) string { return strconv.Itoa(n) }
