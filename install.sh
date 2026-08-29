#!/bin/sh
# PreviewEverywhere 安装脚本。
#
#   curl -fsSL https://raw.githubusercontent.com/lorry-coder/PreviewEverywhere/main/install.sh | sh
#
# 做四件事：认出你的机器 → 下载对应的包 → 核对校验和 → 放进 PATH。
# 装完提示你跑 `pe setup`（那一步才会问「盯哪个目录、要不要开机自启」）。
#
# 环境变量：
#   PE_VERSION       装指定版本，如 v1.2.0。默认最新。
#   PE_INSTALL_DIR   装到哪。默认 /usr/local/bin（写不了就退到 ~/.local/bin）。
#   PE_BASE_URL      从别处下载。内网镜像或离线安装时用。
#   PE_NO_SUDO=1     绝不使用 sudo。
#
# 刻意用 POSIX sh 而不是 bash：这个程序的目标机器包括 NAS 和路由器固件，
# 那些地方的 /bin/sh 往往是 dash 或 busybox ash，没有 bash。
set -eu

REPO="lorry-coder/PreviewEverywhere"
BIN="pe"

# ── 输出 ──────────────────────────────────────────────────────────
# 只在真的连着终端时上色。管道里的转义序列会污染日志。
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	C_DIM='\033[2m'; C_B='\033[1m'; C_OK='\033[32m'; C_ERR='\033[31m'; C_0='\033[0m'
else
	C_DIM=''; C_B=''; C_OK=''; C_ERR=''; C_0=''
fi
say()  { printf '%b\n' "$*"; }
step() { printf '%b\n' "  ${C_DIM}·${C_0} $*"; }
ok()   { printf '%b\n' "  ${C_OK}✓${C_0} $*"; }
die()  { printf '%b\n' "  ${C_ERR}✗${C_0} $*" >&2; exit 1; }

# ── 依赖 ──────────────────────────────────────────────────────────
if command -v curl >/dev/null 2>&1; then
	fetch()   { curl -fsSL "$1"; }
	download(){ curl -fsSL --progress-bar -o "$2" "$1"; }
	# -o /dev/null -w %{url_effective} 让我们拿到重定向后的最终地址，
	# 这是不碰 GitHub API（有匿名调用频率限制）就问出最新版本号的办法。
	final_url(){ curl -fsSL -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch()   { wget -qO- "$1"; }
	download(){ wget -q --show-progress -O "$2" "$1"; }
	final_url(){ wget -q -S -O /dev/null "$1" 2>&1 | awk '/^  Location: /{u=$2} END{print u}'; }
else
	die "需要 curl 或 wget，一个都没找到。"
fi

# ── 认机器 ────────────────────────────────────────────────────────
os=$(uname -s)
case "$os" in
	Linux)  os=linux ;;
	Darwin) os=darwin ;;
	*)      die "还没有 $os 的预编译包。可以从源码构建：https://github.com/$REPO#从源码构建" ;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64|amd64)   arch=amd64 ;;
	aarch64|arm64)  arch=arm64 ;;
	armv7l|armv7|armv6l) arch=armv7 ;;
	*)              die "还没有 $arch 的预编译包。可以从源码构建：https://github.com/$REPO#从源码构建" ;;
esac

# ── 版本 ──────────────────────────────────────────────────────────
version="${PE_VERSION:-}"
if [ -z "$version" ]; then
	step "查最新版本…"
	# /releases/latest 会 302 到 /releases/tag/vX.Y.Z，末段就是版本号。
	latest=$(final_url "https://github.com/$REPO/releases/latest" 2>/dev/null || true)
	version=${latest##*/}
	case "$version" in
		v*) ;;
		# 一个版本都还没发过时，GitHub 把 /releases/latest 转到 /releases。
		# 这不是解析失败，是「还没有可装的」，要分开说。
		releases) die "$REPO 还没有发布过任何版本。" ;;
		*)  die "没查到最新版本。可以指定一个：PE_VERSION=v1.0.0 sh install.sh" ;;
	esac
fi
plain=${version#v}

base="${PE_BASE_URL:-https://github.com/$REPO/releases/download/$version}"
pkg="${BIN}_${plain}_${os}_${arch}.tar.gz"

# ── 下载 ──────────────────────────────────────────────────────────
tmp=$(mktemp -d)
# trap 里用单引号，$tmp 要在退出那一刻展开——中途换了值也删得对。
trap 'rm -rf "$tmp"' EXIT INT TERM

say ""
say "  ${C_B}PreviewEverywhere${C_0} $version  ($os/$arch)"
say ""
step "下载 $pkg"
download "$base/$pkg" "$tmp/$pkg" || die "下载失败：$base/$pkg"

# ── 校验 ──────────────────────────────────────────────────────────
# 校验和缺失时警告但继续。理由：内网镜像、离线安装这些场景下人可能只放了包，
# 直接失败会把安装脚本变成一个更麻烦的下载器；而静默跳过又太不负责任。
if fetch "$base/checksums.txt" > "$tmp/checksums.txt" 2>/dev/null; then
	if command -v sha256sum >/dev/null 2>&1; then
		sum=$(sha256sum "$tmp/$pkg" | cut -d' ' -f1)
	elif command -v shasum >/dev/null 2>&1; then
		sum=$(shasum -a 256 "$tmp/$pkg" | cut -d' ' -f1)
	else
		sum=""
	fi
	if [ -n "$sum" ]; then
		want=$(awk -v f="$pkg" '$2 == f || $2 == "*"f {print $1}' "$tmp/checksums.txt")
		[ -n "$want" ] || die "checksums.txt 里没有 $pkg 这一项。"
		[ "$sum" = "$want" ] || die "校验和不对。期望 $want，实得 $sum。"
		ok "校验和一致"
	else
		step "跳过校验（本机没有 sha256sum / shasum）"
	fi
else
	step "跳过校验（取不到 checksums.txt）"
fi

tar -xzf "$tmp/$pkg" -C "$tmp" || die "解包失败。"
[ -f "$tmp/$BIN" ] || die "包里没有 $BIN。"
chmod +x "$tmp/$BIN"

# ── 安装 ──────────────────────────────────────────────────────────
# 挑目录的顺序：你指定的 → /usr/local/bin（能写或能 sudo）→ ~/.local/bin。
# 最后那条是兜底，因为它一定写得进去，代价是可能不在 PATH 里——真到那一步会明说。
sudo=""
dir="${PE_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ] 2>/dev/null; then
		dir=/usr/local/bin
	elif [ -z "${PE_NO_SUDO:-}" ] && command -v sudo >/dev/null 2>&1 && [ -d /usr/local/bin ]; then
		dir=/usr/local/bin
		sudo="sudo"
		step "装到 $dir 需要 sudo"
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir" 2>/dev/null || $sudo mkdir -p "$dir" || die "建不了目录 $dir。"

# 先写到同目录下的临时名再 mv：mv 在同一文件系统上是原子的，
# 所以正在运行的旧 pe 不会被写到一半的新文件替掉。
$sudo cp "$tmp/$BIN" "$dir/.$BIN.new" || die "写不进 $dir。换个地方：PE_INSTALL_DIR=~/.local/bin"
$sudo mv "$dir/.$BIN.new" "$dir/$BIN" || die "装不进 $dir。"
ok "已安装到 $dir/$BIN"

# ── 装完之后 ──────────────────────────────────────────────────────
say ""
case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		say "  ${C_B}$dir 不在 PATH 里${C_0}，把这行加进你的 shell 配置："
		say ""
		say "    export PATH=\"\$PATH:$dir\""
		say ""
		;;
esac

"$dir/$BIN" version 2>/dev/null | head -1 | sed 's/^/  /'
say ""
say "  下一步——向导会问三个问题，然后打印二维码："
say ""
say "    ${C_B}$BIN setup${C_0}"
say ""
