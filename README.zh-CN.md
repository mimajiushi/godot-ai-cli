# godot-ai-cli

[English](README.md) | **简体中文**

[![CI](https://github.com/mimajiushi/godot-ai-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/mimajiushi/godot-ai-cli/actions/workflows/ci.yml)
[![Release](https://github.com/mimajiushi/godot-ai-cli/actions/workflows/release.yml/badge.svg)](https://github.com/mimajiushi/godot-ai-cli/actions/workflows/release.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/mimajiushi/godot-ai-cli)](go.mod)
[![License: MIT](https://img.shields.io/github/license/mimajiushi/godot-ai-cli)](LICENSE)

从命令行驱动一个正在运行的 [Godot](https://godotengine.org/) 编辑器——无需 MCP。

`godot-ai-cli` 是一个单文件 Go CLI，通过内置的 `godot_ai` 编辑器插件
（fork 自 [hi-godot/godot-ai](https://github.com/hi-godot/godot-ai)，MIT）工作。
它会把插件安装进你的工程、自行启动编辑器（有界面或 headless），并把每一项编辑器操作——
场景、节点、脚本、信号、UI、材质、动画、粒子、相机、环境、TileMap、测试、截图等——
都暴露为打印 JSON 的普通子命令。任何能执行 shell 命令的 Agent 都能驱动 Godot。

- **支持的 Godot：** 4.5+（推荐 4.7+）
- **支持的平台：** Windows、macOS、Linux（amd64 与 arm64）

## 安装

### 下载发布产物（推荐）

每个 [GitHub release](https://github.com/mimajiushi/godot-ai-cli/releases) 都附带
Windows、macOS、Linux（amd64 与 arm64）的预编译二进制。下载对应平台的
`godot-ai-cli-<版本>-<系统>-<架构>.zip`，用 `godot-ai-cli-<版本>-checksums.txt`
校验（`sha256sum -c`），解压后把 `godot-ai-cli`（Windows 为 `godot-ai-cli.exe`）放入 PATH。

### Skill 安装脚本

随附的 Agent skill（`skill/` 目录，也以 `godot-ai-skill.zip` 附在每个 release 中）
自带各平台安装脚本：解析最新 release、下载并校验对应 zip、完成安装：

```bash
bash scripts/install.sh                                       # Linux / macOS / Git Bash
powershell -ExecutionPolicy Bypass -File scripts/install.ps1  # Windows
```

### 从源码构建

需要 Go（工具链版本见 `go.mod`）：

```bash
git clone https://github.com/mimajiushi/godot-ai-cli.git
cd godot-ai-cli
go build -o godot-ai-cli ./cmd/godot-ai-cli  # Windows 为 godot-ai-cli.exe
```

## 快速上手

```bash
# 把插件装进工程、启动编辑器并等待就绪
godot-ai-cli launch --project /path/to/project

# 驱动编辑器
godot-ai-cli status
godot-ai-cli scene get-hierarchy
godot-ai-cli node create --type Camera3D --name MainCamera --parent /Main

# 运行工程的 GDScript 测试套件
godot-ai-cli test run
```

每个命令与子命令都支持 `-h` / `--help`。`godot-ai-cli -v` 会打印 CLI 版本、
支持的 Godot 版本范围以及内置插件（godot-ai）版本。`godot-ai-cli update`
会检查 GitHub Releases 并提供原地更新。

## 工作原理

```
agent / shell
    │  godot-ai-cli <subcommand>  →  stdout 输出一个 JSON 对象
    ▼
┌──────────────┐   HTTP 127.0.0.1:8000    ┌─────────────────────────┐
│     CLI      │ ───────────────────────▶ │  daemon（`serve`，即     │
└──────────────┘                          │  `launch` 拉起的后端）    │
                                          └───────────┬─────────────┘
                                                      │ WebSocket 127.0.0.1:9500
                                                      ▼
                                          ┌─────────────────────────┐
                                          │ godot_ai 编辑器插件      │ ──▶ 运行中的 Godot 编辑器
                                          └─────────────────────────┘
```

一条 `godot-ai-cli launch` 即可完成：把内置插件装进工程、拉起 daemon、打开编辑器、
等待插件握手。此后每个子命令都是发给 daemon 的一次轻量 HTTP 调用，daemon 再经
WebSocket 桥转发给编辑器内的插件。全部通信仅限本机回环。拓扑、线协议信封与门控
规则详见 [docs/architecture.md](docs/architecture.md)。

## 命令面

26 个域共 147 个编辑器操作（scene、node、script、signal、UI、theme、animation、
material、resource、tilemap、粒子、相机、音频、输入、游戏、测试、截图等）。
直接问二进制要实时目录：

```bash
godot-ai-cli commands                    # 文本清单
godot-ai-cli commands --json --domain node
godot-ai-cli <domain> <op> -h            # 每个操作的参数、超时、写门控
```

带文字约定的同一份目录在
[skill/references/commands.md](skill/references/commands.md)。
没有具名子命令的操作可以走 `godot-ai-cli call <plugin_command>
--params '<json>'`；`batch execute --file ops.json` 可以原子执行多条插件命令。

## 更新

`godot-ai-cli update` 查询 GitHub Releases 的最新版本，与当前构建做语义化版本比较，
经确认提示后下载对应平台的 zip、按 release 校验和文件验证 SHA256，然后原地替换可执行文件
（Windows 上先改名留底；残留的 `.old` 会在下次启动时清理）。更新后需要重启。

## 开发

```bash
go build ./...                 # 构建
go vet ./...                   # vet
gofmt -l .                     # 格式检查（必须无输出）
go test ./... -count=1         # 单元测试
```

基于真实编辑器的测试使用 `../demo` 演示工程——它是开发工作区里的兄弟目录，
**不随本仓库发布**。其中包含 GDScript 测试套件与 fixture 场景，`script/` 下的脚本
（如 `script/smoke-e2e.sh`、`script/build-demo-scenes.sh`）会通过 CLI 本身驱动一个
真实的 headless 编辑器来跑测试。

发布完全自动化：推送 `vX.Y.Z` tag 即触发
[.github/workflows/release.yml](.github/workflows/release.yml)。本地演练发布流程：
先 `bash script/sync-skill.sh`，再 `VERSION=x.y.z bash
script/build-release.sh`（见 [CONTRIBUTING.md](CONTRIBUTING.md)）。

## 与 hi-godot/godot-ai 的关系

`plugin/godot_ai/` 下的编辑器插件 fork 自上游 v3.2.4，移除了遥测并禁用了
Python server 拉起逻辑（Go daemon 完全取代 Python 后端）。每一处分叉都在 GDScript
源码中以 `godot-ai-cli fork patch` 标注，并在
[docs/fork-patches.md](docs/fork-patches.md) 中逐条列出。上游许可证：MIT，
"Godot AI contributors"。见 `UPSTREAM-LICENSE.txt`。

## 许可证

MIT —— 见 `LICENSE`。
