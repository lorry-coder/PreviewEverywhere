package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 内置忽略规则必须并进已有配置，而不是「配置里有就不管默认了」。
// 否则第一次保存时的那份默认值被永久冻在 pe.toml 里，
// 以后新增的规则对老用户一条都不生效——这正是 release/ 没被挡住的原因。
func TestDefaultIgnoreMergesIntoExistingConfig(t *testing.T) {
	dir := t.TempDir()
	// 模拟一份「老版本写出来的」配置：忽略列表是当时的默认值，还少了 release
	old := `bind = "0.0.0.0:8080"
ignore = ["node_modules", ".git", "我自己加的"]
`
	if err := os.WriteFile(filepath.Join(dir, "pe.toml"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	has := func(name string) bool {
		for _, v := range cfg.Ignore {
			if v == name {
				return true
			}
		}
		return false
	}
	if !has("release") {
		t.Error("新增的内置规则应当并进老配置")
	}
	if !has("我自己加的") {
		t.Error("用户自己加的规则必须保留")
	}
	if !has("node_modules") {
		t.Error("原有规则不该丢")
	}
	// 不能有重复
	seen := map[string]int{}
	for _, v := range cfg.Ignore {
		seen[v]++
		if seen[v] > 1 {
			t.Errorf("忽略列表里出现重复项: %s", v)
		}
	}
}

// 构建产物目录必须挡住：它们目录极深、文件极多，
// 而且偶尔真的含 .html（Electron 的 LICENSES.chromium.html）。
func TestBuildOutputDirsAreIgnored(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	joined := " " + strings.Join(cfg.Ignore, " ") + " "
	for _, name := range []string{"release", "Release", "Debug", "obj", "node_modules", ".gradle", "Pods"} {
		if !strings.Contains(joined, " "+name+" ") {
			t.Errorf("%s 应当在默认忽略列表里", name)
		}
	}
}

// 内置规则是强制并入的，所以必须有「反悔」的办法：
// 以 ! 开头的条目去掉同名内置规则。否则一条判断错的内置规则用户永远删不掉。
func TestIgnoreNegation(t *testing.T) {
	dir := t.TempDir()
	conf := "ignore = [\"!external\", \"我自己加的\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "pe.toml"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range cfg.Ignore {
		if v == "external" {
			t.Error("!external 应当去掉内置的 external 规则")
		}
		if strings.HasPrefix(v, "!") {
			t.Errorf("! 条目本身不该留在最终列表里: %s", v)
		}
	}
	if !strings.Contains(strings.Join(cfg.Ignore, " "), "node_modules") {
		t.Error("其它内置规则不该被连累")
	}
}
