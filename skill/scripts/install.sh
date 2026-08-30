#!/usr/bin/env bash
# godot-ai-cli 安装脚本（linux / darwin / Windows Git Bash）
# 流程：识别平台 → 查询 GitHub 最新 release → 下载 zip + checksums → 校验 SHA256
#       → 安装到 ~/.local/bin → 用 -v 验证 → 必要时给出 PATH 提示
# 约定（与 godot-ai-cli 自身 update 命令一致）：
#   tag 为 vX.Y.Z；资产名 godot-ai-cli-<X.Y.Z>-<goos>-<goarch>.zip；
#   校验文件名 godot-ai-cli-<X.Y.Z>-checksums.txt，内容为 "<sha256>  <文件名>" 行
set -euo pipefail

REPO="mimajiushi/godot-ai-cli"
INSTALL_DIR="${GODOT_AI_CLI_INSTALL_DIR:-$HOME/.local/bin}"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"

die() { echo "install.sh: ERROR: $*" >&2; exit 1; }
info() { echo "install.sh: $*"; }

# 依赖检查：curl 必需；unzip 缺失时回退 python3
command -v curl >/dev/null 2>&1 || die "curl not found — install curl first"

# 1. 识别 OS / 架构（goos/goarch 命名）
uname_s="$(uname -s)"
case "$uname_s" in
  Linux*)               GOOS="linux" ;;
  Darwin*)              GOOS="darwin" ;;
  MINGW*|MSYS*|CYGWIN*) GOOS="windows" ;;  # Git Bash / MSYS2 / Cygwin
  *) die "unsupported OS: $uname_s (supported: windows/linux/darwin)" ;;
esac
uname_m="$(uname -m)"
case "$uname_m" in
  x86_64|amd64)  GOARCH="amd64" ;;
  arm64|aarch64) GOARCH="arm64" ;;
  *) die "unsupported arch: $uname_m (supported: amd64/arm64)" ;;
esac
info "platform: ${GOOS}/${GOARCH}"

# 2. 查询最新 release；仓库尚未发布任何 release 时 GitHub 返回 404，明确告知并退出
release_json="$(curl -fsSL -H "Accept: application/vnd.github+json" "$API_URL")" \
  || die "no release published yet for ${REPO} (or GitHub unreachable).
       Build from source instead: git clone https://github.com/${REPO} && cd godot-ai-cli && go build ./cmd/godot-ai-cli"
tag="$(printf '%s' "$release_json" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*: *"//; s/"//')"
[ -n "$tag" ] || die "could not parse tag_name from the latest release payload"
ver="${tag#v}"  # 资产命名使用去掉前导 v 的版本号
info "latest release: ${tag}"

# 3. 构造资产 URL 并下载（命名约定与 CLI 内置 update 命令一致）
asset="godot-ai-cli-${ver}-${GOOS}-${GOARCH}.zip"
sums="godot-ai-cli-${ver}-checksums.txt"
base_url="https://github.com/${REPO}/releases/download/${tag}"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
info "downloading ${asset}"
curl -fsSL -o "$tmpdir/$asset" "$base_url/$asset" \
  || die "asset ${asset} not found in release ${tag} — this platform may not be published"
curl -fsSL -o "$tmpdir/$sums" "$base_url/$sums" \
  || die "checksums asset ${sums} missing — refusing to install unverified bits"

# 4. 从 checksums 中取出本资产的期望哈希
expected="$(awk -v name="$asset" '{n=$NF; sub(/^\*/, "", n); if (n == name) print $1}' "$tmpdir/$sums")"
[ -n "$expected" ] || die "checksums file has no entry for ${asset} — refusing to install unverified bits"

# 5. 计算实际 SHA256：优先 sha256sum；macOS 回退 shasum；Windows Git Bash 回退 certutil
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmpdir/$asset" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmpdir/$asset" | awk '{print $1}')"
elif [ "$GOOS" = "windows" ] && command -v certutil >/dev/null 2>&1; then
  # certutil 输出第二行为哈希（可能带空格），去掉空白字符
  actual="$(certutil -hashfile "$(cygpath -w "$tmpdir/$asset")" SHA256 | sed -n '2p' | tr -d ' \r')"
else
  die "no SHA256 tool available (tried sha256sum, shasum, certutil)"
fi
[ "$(printf '%s' "$actual" | tr 'A-F' 'a-f')" = "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" ] \
  || die "SHA256 mismatch for ${asset}: expected ${expected}, got ${actual} — install aborted, nothing was modified"
info "SHA256 verified"

# 6. 解包并安装（覆盖已有安装是安装器的既定行为）
if command -v unzip >/dev/null 2>&1; then
  unzip -q -o "$tmpdir/$asset" -d "$tmpdir/extract"
elif command -v python3 >/dev/null 2>&1; then
  python3 -m zipfile -e "$tmpdir/$asset" "$tmpdir/extract"
elif [ "$GOOS" = "windows" ] && command -v powershell >/dev/null 2>&1; then
  powershell -NoProfile -Command "Expand-Archive -Force -LiteralPath '$(cygpath -w "$tmpdir/$asset")' -DestinationPath '$(cygpath -w "$tmpdir/extract")'"
else
  die "no unzip tool available (tried unzip, python3, powershell Expand-Archive)"
fi

bin_name="godot-ai-cli"
[ "$GOOS" = "windows" ] && bin_name="godot-ai-cli.exe"
src="$tmpdir/extract/$bin_name"
[ -f "$src" ] || src="$(find "$tmpdir/extract" -name "$bin_name" -type f | head -1)"
[ -n "$src" ] && [ -f "$src" ] || die "archive did not contain ${bin_name}"

mkdir -p "$INSTALL_DIR"
cp "$src" "$INSTALL_DIR/$bin_name"
chmod +x "$INSTALL_DIR/$bin_name" 2>/dev/null || true
info "installed: $INSTALL_DIR/$bin_name"

# 7. 验证安装（版本输出即成功证明）
"$INSTALL_DIR/$bin_name" -v || die "installed binary failed to run"

# 8. PATH 提示：安装目录不在 PATH 时告知用户（不擅自修改 shell 配置）
case ":$PATH:" in
  *":$INSTALL_DIR:"*) info "PATH already contains $INSTALL_DIR — run: godot-ai-cli -v" ;;
  *)
    info "NOTE: $INSTALL_DIR is not on your PATH."
    info "  bash/zsh: export PATH=\"$INSTALL_DIR:\$PATH\"  (add to ~/.bashrc or ~/.zshrc to persist)"
    [ "$GOOS" = "windows" ] && info "  Windows 推荐改用 scripts/install.ps1，它会自动写入用户 PATH"
    info "  until then, invoke by full path: $INSTALL_DIR/$bin_name"
    ;;
esac
