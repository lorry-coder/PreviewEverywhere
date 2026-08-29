package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// `pe upgrade` 原地换掉这个二进制。
//
// 一条贯穿始终的规矩：**除非你敲了这条命令，程序不会主动联网。**
// 所以这里没有「启动时检查新版本」——一个跑在你自己机器上、
// 只服务局域网的东西，不该在你没要求的时候去连外网。
// 想知道有没有新版就 `pe upgrade --check`。
//
// 换二进制这件事在 Linux/macOS 上是安全的：正在运行的进程持有的是 inode，
// rename 换掉目录项不影响它。所以升级不会打断正在跑的服务，
// 但服务也不会自动换到新版——`pe status` 会把这件事说出来。

const (
	repoOwner = "lorry-coder"
	repoName  = "PreviewEverywhere"
)

func cmdUpgrade(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	checkOnly := fs.Bool("check", false, "只看有没有新版本，不动任何东西")
	want := fs.String("to", "", "升到指定版本，如 v1.2.0。默认最新")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}

	latest := *want
	if latest == "" {
		fmt.Println("  · 查最新版本…")
		var err error
		if latest, err = latestRelease(); err != nil {
			return err
		}
	}

	fmt.Printf("  当前 %s，最新 %s\n", version, latest)
	switch {
	case version == "dev":
		fmt.Println("  · 这是个开发构建（不是从 tag 发出来的），升级会把它换成正式版。")
	case strings.TrimPrefix(version, "v") == strings.TrimPrefix(latest, "v"):
		fmt.Println("  ✓ 已经是最新的了。")
		return nil
	}
	if *checkOnly {
		fmt.Printf("  升上去：pe upgrade\n")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("找不到自己在哪: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	// 先确认写得进去，再去下载。反过来的话，人要等下载完才被告知
	// 「你没权限」——而这件事一开始就知道。
	if err := writable(filepath.Dir(exe)); err != nil {
		return fmt.Errorf("写不进 %s：%w\n用 sudo 跑，或者重装到别处：\n  curl -fsSL https://raw.githubusercontent.com/%s/%s/main/install.sh | sh",
			filepath.Dir(exe), err, repoOwner, repoName)
	}

	fmt.Printf("  · 下载 %s/%s…\n", runtime.GOOS, runtime.GOARCH)
	bin, err := downloadRelease(latest)
	if err != nil {
		return err
	}

	// 落在同一个目录里再 rename：同一文件系统上的 rename 是原子的，
	// 所以任何时刻别人看到的要么是完整的旧版，要么是完整的新版，
	// 不会是一个写到一半的文件。
	tmp := filepath.Join(filepath.Dir(exe), ".pe.upgrade")
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("换不上去: %w", err)
	}

	fmt.Printf("  ✓ 已升到 %s\n", latest)
	fmt.Println()
	// 正在跑的服务持有的是旧 inode，不会自己换过去。这一点必须说，
	// 否则人会以为升级完就生效了，然后对着一个没变的界面纳闷。
	fmt.Println("  正在跑的服务还是旧版本，重启它才会换过去：")
	fmt.Println("    pe service restart      （或者你自己起的那个进程重启一下）")
	fmt.Println()
	return nil
}

func writable(dir string) error {
	probe := filepath.Join(dir, ".pe-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(probe)
}

// latestRelease 问出最新的 tag。
//
// 走 /releases/latest 的重定向而不是 GitHub API：API 对匿名调用有频率限制，
// 而这个重定向没有。终点形如 …/releases/tag/vX.Y.Z，末段就是版本号。
func latestRelease() (string, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	url := fmt.Sprintf("https://github.com/%s/%s/releases/latest", repoOwner, repoName)
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("连不上 GitHub: %w", err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", errors.New("没查到最新版本。指定一个：pe upgrade --to v1.0.0")
	}
	tag := loc[strings.LastIndex(loc, "/")+1:]
	// 一个版本都还没发过时，GitHub 会把 /releases/latest 转到 /releases，
	// 于是末段是 "releases" 而不是版本号。这不是解析失败，是「还没有可升的」。
	if tag == "releases" {
		return "", fmt.Errorf("%s/%s 还没有发布过任何版本", repoOwner, repoName)
	}
	if !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("查到的版本号看不懂: %q（GitHub 转到了 %s）", tag, loc)
	}
	return tag, nil
}

// downloadRelease 取回对应平台的二进制，并核对校验和。
func downloadRelease(tag string) ([]byte, error) {
	arch := runtime.GOARCH
	if arch == "arm" {
		// goreleaser 产出的 32 位 arm 包带 v7 后缀，和 arm64 区分开。
		arch = "armv7"
	}
	name := fmt.Sprintf("pe_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), runtime.GOOS, arch)
	base := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s", repoOwner, repoName, tag)

	pkg, err := fetchAll(base + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("下载 %s 失败: %w", name, err)
	}

	// 校验和这里是**必需**的，和 install.sh 不同：那边允许离线镜像缺文件，
	// 而这边是从 GitHub 直接下载并且要覆盖掉正在用的二进制，没有妥协的余地。
	sums, err := fetchAll(base + "/checksums.txt")
	if err != nil {
		return nil, fmt.Errorf("取不到 checksums.txt，不敢往下走: %w", err)
	}
	sum := sha256.Sum256(pkg)
	got := hex.EncodeToString(sum[:])
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = f[0]
			break
		}
	}
	if want == "" {
		return nil, fmt.Errorf("checksums.txt 里没有 %s 这一项", name)
	}
	if got != want {
		return nil, fmt.Errorf("校验和不对。期望 %s，实得 %s", want, got)
	}

	return extractBinary(pkg)
}

func fetchAll(url string) ([]byte, error) {
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s 返回 %s", url, resp.Status)
	}
	// 限一下大小，免得下载源出问题时把内存吃光。
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

func extractBinary(pkg []byte) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(pkg)))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("包里没有 pe")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "pe" {
			return io.ReadAll(io.LimitReader(tr, 256<<20))
		}
	}
}
