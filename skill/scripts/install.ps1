# godot-ai-cli install script (Windows PowerShell 5.1+ / PowerShell 7+)
# Flow: detect arch -> query the latest GitHub release -> download zip + checksums
#       -> verify SHA256 -> install to %LOCALAPPDATA%\Programs\godot-ai-cli
#       -> append the user PATH -> verify with -v
# Asset naming matches install.sh and the CLI's built-in update command:
#   godot-ai-cli-<X.Y.Z>-windows-<amd64|arm64>.zip + godot-ai-cli-<X.Y.Z>-checksums.txt
#
# NOTE: keep this file pure ASCII. PowerShell 5.1 decodes BOM-less scripts
# with the system ANSI codepage (GBK on zh-CN Windows), and non-ASCII bytes
# in comments can swallow the following code line during parsing.
[CmdletBinding()]
param(
    # Install directory, defaults to %LOCALAPPDATA%\Programs\godot-ai-cli
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\godot-ai-cli')
)

$ErrorActionPreference = 'Stop'
$Repo = 'mimajiushi/godot-ai-cli'
# Note: the list endpoint, not /releases/latest - GitHub's latest excludes
# prereleases and would 404 during the beta-only phase; take the first
# non-draft entry of the list.
$ApiUrl = "https://api.github.com/repos/$Repo/releases?per_page=10"

function Die([string]$Message) { Write-Host "install.ps1: ERROR: $Message" -ForegroundColor Red; exit 1 }
function Info([string]$Message) { Write-Host "install.ps1: $Message" }

# 1. Detect the architecture (goarch naming)
$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
    'AMD64' { $goarch = 'amd64' }
    'ARM64' { $goarch = 'arm64' }
    default { Die "unsupported arch: $arch (supported: amd64/arm64)" }
}
Info "platform: windows/$goarch"

# 2. Query the latest release (the GitHub API requires a User-Agent header;
# list endpoint, first non-draft entry)
# PS 5.1 + older .NET do not enable TLS 1.2 by default; without it the
# handshake against api.github.com fails.
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch {}
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
$ver = $tag.TrimStart('v')  # asset names use the version without the leading v
Info "latest release: $tag"

# 3. Build the asset URLs and download into a temp directory
$asset = "godot-ai-cli-$ver-windows-$goarch.zip"
$sums = "godot-ai-cli-$ver-checksums.txt"
$baseUrl = "https://github.com/$Repo/releases/download/$tag"
$tmpdir = Join-Path ([IO.Path]::GetTempPath()) ("godot-ai-cli-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmpdir | Out-Null
try {
    Info "downloading $asset"
    try {
        Invoke-WebRequest -Uri "$baseUrl/$asset" -OutFile (Join-Path $tmpdir $asset) -UseBasicParsing
    } catch { Die "asset $asset not found in release $tag - this platform may not be published" }
    try {
        Invoke-WebRequest -Uri "$baseUrl/$sums" -OutFile (Join-Path $tmpdir $sums) -UseBasicParsing
    } catch { Die "checksums asset $sums missing - refusing to install unverified bits" }

    # 4. Read the expected hash of this asset from checksums
    #    (line format "<sha256>  <filename>")
    $expected = $null
    foreach ($line in (Get-Content (Join-Path $tmpdir $sums))) {
        $fields = $line -split '\s+'
        $name = $fields[-1].TrimStart('*')
        if ($name -eq $asset) { $expected = $fields[0]; break }
    }
    if (-not $expected) { Die "checksums file has no entry for $asset - refusing to install unverified bits" }

    # 5. Verify SHA256 (Get-FileHash ships with PowerShell on every platform)
    $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmpdir $asset)).Hash
    if ($actual.ToLower() -ne $expected.ToLower()) {
        Die "SHA256 mismatch for ${asset}: expected $expected, got $actual - install aborted, nothing was modified"
    }
    Info 'SHA256 verified'

    # 6. Unpack and install (overwriting an existing install is the
    #    installer's documented behavior)
    Expand-Archive -Force -LiteralPath (Join-Path $tmpdir $asset) -DestinationPath (Join-Path $tmpdir 'extract')
    $exe = Get-ChildItem -Recurse -Filter 'godot-ai-cli.exe' (Join-Path $tmpdir 'extract') | Select-Object -First 1
    if (-not $exe) { Die 'archive did not contain godot-ai-cli.exe' }
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -Force $exe.FullName (Join-Path $InstallDir 'godot-ai-cli.exe')
    Info "installed: $InstallDir\godot-ai-cli.exe"
} finally {
    Remove-Item -Recurse -Force $tmpdir -ErrorAction SilentlyContinue
}

# 7. Append the user PATH (only when missing; user scope, the system PATH
#    is left untouched)
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$pathParts = @($userPath -split ';' | Where-Object { $_ })
if ($pathParts -notcontains $InstallDir) {
    [Environment]::SetEnvironmentVariable('Path', (($pathParts + $InstallDir) -join ';'), 'User')
    Info "added $InstallDir to the user PATH (new terminals pick it up; this session keeps the old PATH)"
} else {
    Info 'user PATH already contains the install directory'
}

# 8. Verify the install (the version output is the proof of success); this
#    session invokes by full path
$installed = Join-Path $InstallDir 'godot-ai-cli.exe'
& $installed -v
if ($LASTEXITCODE -ne 0) { Die 'installed binary failed to run' }
Info "done. In a NEW terminal: godot-ai-cli -v - in THIS session: & '$installed'"
