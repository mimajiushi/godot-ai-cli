# godot-ai-cli 安装脚本（Windows PowerShell 5.1+ / PowerShell 7+）
# 流程：识别架构 → 查询 GitHub 最新 release → 下载 zip + checksums → 校验 SHA256
#       → 安装到 %LOCALAPPDATA%\Programs\godot-ai-cli → 追加用户 PATH → 用 -v 验证
# 资产命名约定与 install.sh / CLI 内置 update 命令一致：
#   godot-ai-cli-<X.Y.Z>-windows-<amd64|arm64>.zip + godot-ai-cli-<X.Y.Z>-checksums.txt
[CmdletBinding()]
param(
    # 安装目录，默认 %LOCALAPPDATA%\Programs\godot-ai-cli
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\godot-ai-cli')
)

$ErrorActionPreference = 'Stop'
$Repo = 'mimajiushi/godot-ai-cli'
# 注意：用列表端点而非 /releases/latest —— GitHub 的 latest 排除 prerelease，
# beta-only 阶段会 404；取列表中第一个非草稿条目
$ApiUrl = "https://api.github.com/repos/$Repo/releases?per_page=10"

function Die([string]$Message) { Write-Host "install.ps1: ERROR: $Message" -ForegroundColor Red; exit 1 }
function Info([string]$Message) { Write-Host "install.ps1: $Message" }

# 1. 识别架构（goarch 命名）
$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
    'AMD64' { $goarch = 'amd64' }
    'ARM64' { $goarch = 'arm64' }
    default { Die "unsupported arch: $arch (supported: amd64/arm64)" }
}
Info "platform: windows/$goarch"

# 2. 查询最新 release（GitHub API 要求 User-Agent 头；列表端点，取第一个非草稿条目）
try {
    $releases = Invoke-RestMethod -Uri $ApiUrl -Headers @{ 'User-Agent' = 'godot-ai-cli-install'; 'Accept' = 'application/vnd.github+json' }
} catch {
    Die @"
no release published yet for $Repo (or GitHub unreachable): $($_.Exception.Message)
Build from source instead: git clone https://github.com/$Repo && cd godot-ai-cli && go build ./cmd/godot-ai-cli
"@
}
$release = $releases | Where-Object { -not $_.draft } | Select-Object -First 1
$tag = if ($release) { $release.tag_name } else { $null }
if (-not $tag) { Die 'could not find a non-draft release in the releases list payload' }
$ver = $tag.TrimStart('v')  # 资产命名使用去掉前导 v 的版本号
Info "latest release: $tag"

# 3. 构造资产 URL 并下载到临时目录
$asset = "godot-ai-cli-$ver-windows-$goarch.zip"
$sums = "godot-ai-cli-$ver-checksums.txt"
$baseUrl = "https://github.com/$Repo/releases/download/$tag"
$tmpdir = Join-Path ([IO.Path]::GetTempPath()) ("godot-ai-cli-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmpdir | Out-Null
try {
    Info "downloading $asset"
    try {
        Invoke-WebRequest -Uri "$baseUrl/$asset" -OutFile (Join-Path $tmpdir $asset) -UseBasicParsing
    } catch { Die "asset $asset not found in release $tag — this platform may not be published" }
    try {
        Invoke-WebRequest -Uri "$baseUrl/$sums" -OutFile (Join-Path $tmpdir $sums) -UseBasicParsing
    } catch { Die "checksums asset $sums missing — refusing to install unverified bits" }

    # 4. 从 checksums 中取出本资产的期望哈希（行格式 "<sha256>  <文件名>"）
    $expected = $null
    foreach ($line in (Get-Content (Join-Path $tmpdir $sums))) {
        $fields = $line -split '\s+'
        $name = $fields[-1].TrimStart('*')
        if ($name -eq $asset) { $expected = $fields[0]; break }
    }
    if (-not $expected) { Die "checksums file has no entry for $asset — refusing to install unverified bits" }

    # 5. 校验 SHA256（Get-FileHash 全平台 PowerShell 自带）
    $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmpdir $asset)).Hash
    if ($actual.ToLower() -ne $expected.ToLower()) {
        Die "SHA256 mismatch for ${asset}: expected $expected, got $actual — install aborted, nothing was modified"
    }
    Info 'SHA256 verified'

    # 6. 解包并安装（覆盖已有安装是安装器的既定行为）
    Expand-Archive -Force -LiteralPath (Join-Path $tmpdir $asset) -DestinationPath (Join-Path $tmpdir 'extract')
    $exe = Get-ChildItem -Recurse -Filter 'godot-ai-cli.exe' (Join-Path $tmpdir 'extract') | Select-Object -First 1
    if (-not $exe) { Die 'archive did not contain godot-ai-cli.exe' }
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -Force $exe.FullName (Join-Path $InstallDir 'godot-ai-cli.exe')
    Info "installed: $InstallDir\godot-ai-cli.exe"
} finally {
    Remove-Item -Recurse -Force $tmpdir -ErrorAction SilentlyContinue
}

# 7. 追加用户 PATH（仅当缺失；写入用户作用域，不动系统 PATH）
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$pathParts = @($userPath -split ';' | Where-Object { $_ })
if ($pathParts -notcontains $InstallDir) {
    [Environment]::SetEnvironmentVariable('Path', (($pathParts + $InstallDir) -join ';'), 'User')
    Info "added $InstallDir to the user PATH (new terminals pick it up; this session keeps the old PATH)"
} else {
    Info 'user PATH already contains the install directory'
}

# 8. 验证安装（版本输出即成功证明）；当前会话用全路径调用
$installed = Join-Path $InstallDir 'godot-ai-cli.exe'
& $installed -v
if ($LASTEXITCODE -ne 0) { Die 'installed binary failed to run' }
Info "done. In a NEW terminal: godot-ai-cli -v — in THIS session: & '$installed'"
