package ingest

import (
	"os"
	"path/filepath"
	"testing"

	"previeweverywhere/internal/config"
)

// 构建树按标记文件识别，不靠目录名。真实案例：
// build_rerun/_deps/…/mimalloc_ep/docs/ 下几百页 doxygen HTML，
// 名字既不叫 build 也不叫 dist，一次扫描就往库里灌了 187 篇。
func TestBuildTreeDetectedByMarker(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "build_rerun")
	if err := os.MkdirAll(filepath.Join(tree, "_deps", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isBuildTree(tree) {
		t.Fatal("还没有标记文件，不该判定为构建树")
	}
	if err := os.WriteFile(filepath.Join(tree, "CMakeCache.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isBuildTree(tree) {
		t.Error("有 CMakeCache.txt 就该判定为构建树")
	}
	if isBuildTree(dir) {
		t.Error("父目录不该被误判")
	}
}

// 忽略列表要支持通配符，否则 cmake-build-debug / build-arm64 这类
// 名字千变万化的目录永远列不全。
func TestIgnoreSupportsGlob(t *testing.T) {
	w := &Watcher{cfg: &config.Config{Ignore: []string{"node_modules", "cmake-build-*"}}}
	cases := map[string]bool{
		"node_modules":        true,
		"cmake-build-debug":   true,
		"cmake-build-release": true,
		".git":                true, // 隐藏目录一律跳过
		"src":                 false,
		"cmake":               false,
	}
	for name, want := range cases {
		if got := w.isIgnoredDir(name); got != want {
			t.Errorf("isIgnoredDir(%q) = %v, 期望 %v", name, got, want)
		}
	}
}
