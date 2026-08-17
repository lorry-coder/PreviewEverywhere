// Package config 负责服务端配置（数据目录下的 pe.toml）与客户端配置
// （~/.config/pe/config.toml，供 pe push 使用）。
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Watch 是一条监听规则。Path 支持 glob（如 ~/Code/*/docs）。
type Watch struct {
	Path string `toml:"path"`
	// Project 为空时自动判定归属：向上找 .pe.toml → 向上找 .git → 父目录名。
	Project string   `toml:"project,omitempty"`
	Include []string `toml:"include,omitempty"`
}

type Config struct {
	Bind      string   `toml:"bind"`
	TokenHash string   `toml:"token_hash"`
	Ignore    []string `toml:"ignore"`
	Watch     []Watch  `toml:"watch"`
	// LocalizeCDN 决定要不要在入库时把 agent HTML 引用的 CDN 库内联进来。
	// 打开它，地铁上没网时图表才画得出来；关掉它，入库过程完全不联网。
	// 用指针是为了区分「没配」和「显式配成 false」。
	LocalizeCDN *bool `toml:"localize_cdn"`

	path string `toml:"-"`
}

// DefaultIgnore 是永远不该被采集的目录名。递归监听大仓库时，
// 这个列表同时也是 inotify 句柄的第一道保护。
//
// 里面大多是构建产物：它们目录极深、文件极多，而且偶尔真的含 .md / .html
// （Electron 打包出来的 LICENSES.chromium.html 就是一例），
// 不挡住的话既吃满 inotify 配额，又往库里灌一堆没人要读的东西。
var DefaultIgnore = []string{
	// 版本控制与编辑器
	".git", ".svn", ".hg", ".idea", ".vscode",
	// 依赖
	"node_modules", "vendor", "bower_components", ".yarn", ".pnpm-store",
	".venv", "venv", "site-packages", "Pods", ".cargo", ".stack-work",
	// 构建产物
	"dist", "build", "out", "target", "release", "Release", "Debug", "obj",
	".output", ".next", ".nuxt", ".svelte-kit", ".astro", ".docusaurus",
	"cmake-build-*", "build-*", "_deps", "_build",
	// 缓存与中间结果
	".cache", ".turbo", ".parcel-cache", ".sass-cache", ".gradle", ".tox",
	"__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".eggs",
	".ipynb_checkpoints", ".nyc_output", "coverage",
	// 部署与云平台
	".terraform", ".serverless", ".vercel", ".netlify", ".expo", ".dart_tool",
	// vendored 的第三方源码。这些目录里的 README 是别人项目的文档，
	// 混进来会把自己写的东西挤到后面去（实测 408 篇里有 56 篇来自这里）。
	"3rdparty", "3rd_party", "third_party", "thirdparty", "Thirdparty",
	"ThirdParty", "extern", "external", "submodules",
}

var DefaultInclude = []string{"*.md", "*.markdown", "*.html", "*.htm"}

// DefaultDataDir 遵循 XDG 约定。
func DefaultDataDir() string {
	if d := os.Getenv("PE_DATA_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "pe")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pe"
	}
	return filepath.Join(home, ".local", "share", "pe")
}

// Load 读取数据目录下的 pe.toml；不存在则返回一份带默认值的配置。
func Load(dataDir string) (*Config, error) {
	path := filepath.Join(dataDir, "pe.toml")
	cfg := &Config{
		Bind:   "0.0.0.0:8080",
		Ignore: mergeIgnore(nil),
		path:   path,
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	cfg.path = path
	// 内置规则始终并进来，而不是「配置里有就不管默认了」。
	// 否则第一次保存时的那份默认值会被永久冻在 pe.toml 里，
	// 以后程序新增的忽略规则对老用户一条都不生效。
	// 代价是没法删掉某条内置规则——但没人想监听 node_modules。
	cfg.Ignore = mergeIgnore(cfg.Ignore)
	if cfg.Bind == "" {
		cfg.Bind = "0.0.0.0:8080"
	}
	return cfg, nil
}

func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(
		"# PreviewEverywhere 服务端配置。\n" +
			"# token_hash 是访问口令的 sha256；口令本身不落盘，忘了就用 `pe token` 重发。\n\n"); err != nil {
		return err
	}
	return toml.NewEncoder(f).Encode(c)
}

// MatchIgnore 判断一个目录名是否命中忽略规则，支持 filepath.Match 通配符。
// 监听器和 hook 必须用同一套判断——两条采集通道对「什么该收」有分歧，
// 表现就是「同一个文件，watch 不收但 hook 收了」，极难排查。
func MatchIgnore(name string, ignore []string) bool {
	for _, ig := range ignore {
		if name == ig {
			return true
		}
		if strings.ContainsAny(ig, "*?[") {
			if ok, err := filepath.Match(ig, name); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// mergeIgnore 把内置规则与用户自己加的合并去重，顺序稳定。
//
// 以 ! 开头的条目是「反悔」：它会去掉同名的内置规则。
// 因为内置规则是强制并入的，没有这个后门，我判断错的那一条你就永远删不掉——
// 比如 external/ 在多数 C++ 仓库里是 vendored 依赖，但也可能真是你放文档的地方。
func mergeIgnore(user []string) []string {
	except := make(map[string]bool)
	for _, name := range user {
		if name = strings.TrimSpace(name); strings.HasPrefix(name, "!") {
			except[strings.TrimPrefix(name, "!")] = true
		}
	}

	seen := make(map[string]bool, len(DefaultIgnore)+len(user))
	out := make([]string, 0, len(DefaultIgnore)+len(user))
	for _, group := range [][]string{DefaultIgnore, user} {
		for _, name := range group {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] || except[name] || strings.HasPrefix(name, "!") {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// ShouldLocalizeCDN 默认开启：不开的话，带图表的 HTML 在离线时就是一片空白，
// 而那种文档走原样模式的唯一理由就是图表。
func (c *Config) ShouldLocalizeCDN() bool {
	return c.LocalizeCDN == nil || *c.LocalizeCDN
}

// AddWatch 追加一条监听规则，重复路径视为更新。
func (c *Config) AddWatch(w Watch) {
	if len(w.Include) == 0 {
		w.Include = DefaultInclude
	}
	for i, existing := range c.Watch {
		if existing.Path == w.Path {
			c.Watch[i] = w
			return
		}
	}
	c.Watch = append(c.Watch, w)
}

// ── 访问口令 ──────────────────────────────────────────────────────

// NewToken 生成一个新的访问口令并把它的哈希写进配置。返回明文，只此一次。
func (c *Config) NewToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	c.TokenHash = HashToken(token)
	return token, nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (c *Config) CheckToken(token string) bool {
	if c.TokenHash == "" || token == "" {
		return false
	}
	return HashToken(token) == c.TokenHash
}

// ── 客户端配置（pe push 用） ──────────────────────────────────────

type Client struct {
	Endpoint string `toml:"endpoint"`
	Token    string `toml:"token"`
}

func clientPath() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "pe", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pe-client.toml"
	}
	return filepath.Join(home, ".config", "pe", "config.toml")
}

// LoadClient 读取客户端配置，环境变量 PE_ENDPOINT / PE_TOKEN 优先。
func LoadClient() (*Client, error) {
	c := &Client{}
	if data, err := os.ReadFile(clientPath()); err == nil {
		if err := toml.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("解析客户端配置失败: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	if v := os.Getenv("PE_ENDPOINT"); v != "" {
		c.Endpoint = v
	}
	if v := os.Getenv("PE_TOKEN"); v != "" {
		c.Token = v
	}
	if c.Endpoint == "" {
		c.Endpoint = "http://127.0.0.1:8080"
	}
	c.Endpoint = strings.TrimSuffix(c.Endpoint, "/")
	return c, nil
}

func SaveClient(c *Client) error {
	path := clientPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}
