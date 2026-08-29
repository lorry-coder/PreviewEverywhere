package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// extractBinary 必须从包里挑出 pe，而不是包里的第一个文件。
// 归档里还有 README.md / LICENSE / 使用手册，顺序不保证。
func TestExtractBinaryPicksPe(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{
		{"README.md", "readme"},
		{"docs/使用手册.md", "manual"},
		{"pe", "BINARY"},
		{"LICENSE", "mit"},
	} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(f.body))
	}
	tw.Close()
	gz.Close()

	got, err := extractBinary(buf.Bytes())
	if err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	if string(got) != "BINARY" {
		t.Fatalf("挑错了文件，拿到 %q", got)
	}
}

// 包里没有 pe 时要明确报错，而不是返回空内容——
// 后者会把一个 0 字节的文件原子替换到你正在用的二进制上。
func TestExtractBinaryMissing(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	if _, err := extractBinary(buf.Bytes()); err == nil {
		t.Fatal("包里没有 pe 却没报错")
	}
}

func TestWritable(t *testing.T) {
	dir := t.TempDir()
	if err := writable(dir); err != nil {
		t.Fatalf("临时目录应当可写: %v", err)
	}
	if err := writable(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("不存在的目录不该报可写")
	}
	// 探测文件不能留下
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("探测完留下了文件: %v", entries)
	}
}
