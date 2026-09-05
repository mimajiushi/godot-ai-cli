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

- **支持的 Godot：** 4.5+（推荐 4.7+），标准版或 .NET（Mono）版均可
- **支持的平台：** Windows、macOS、Linux（amd64 与 arm64）

## 安装

### 下载发布产物（推荐）

每个 [GitHub release](https://github.com/mimajiushi/godot-ai-cli/releases) 都附带
Windows、macOS、Linux（amd64 与 arm64）的预编译二进制。下载对应平台的
`godot-ai-cli-<版本>-<系统>-<架构>.zip`，用 `godot-ai-cli-<版本>-checksums.txt`
校验（`sha256sum -c`；只下载了单个 zip 时用 `grep <系统>-<架构>
godot-ai-cli-<版本>-checksums.txt | sha256sum -c -`），解压后把
`godot-ai-cli`（Windows 为 `godot-ai-cli.exe`）放入 PATH；也可以不安装，直接用
完整路径调用解压出的二进制。

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
godot-ai-cli scene create --path res://main.tscn --root-type Node2D --root-name Main
#（或打开已有场景：godot-ai-cli scene open --path res://your_scene.tscn）
godot-ai-cli scene get-hierarchy
godot-ai-cli node create --type Camera2D --name MainCamera --parent-path /Main

# 运行工程的 GDScript 测试套件
godot-ai-cli test run
```

Git Bash 注意：MSYS 会把 `/Main` 这类绝对节点路径改写成 Windows 路径——涉及
此类参数的命令请加 `MSYS_NO_PATHCONV=1` 前缀。

每个命令与子命令都支持 `-h` / `--help`。`godot-ai-cli -v` 会打印 CLI 版本、
支持的 Godot 版本范围以及内置插件（godot-ai）版本。`godot-ai-cli update`
会检查 GitHub Releases 并提供原地更新。

## 接入你自己的工程

- **前提：** 一个 Godot **4.5+** 工程（包含 `project.godot` 的目录）。动手前先跑
  `godot-ai-cli -v`——它会打印支持的 Godot 版本范围，以及工程将被对齐到的内置插件版本。
- **Godot 二进制：** `launch` 依次从 `--godot` 参数、`GODOT_BIN` 环境变量、
  `godot use` 保存的默认路径、PATH、各系统常见安装位置查找编辑器。Godot 不在这些
  位置时，用 `godot-ai-cli godot use /path/to/godot` 保存一次（写入用户配置目录，
  长期生效），或每次显式指定：`launch --project . --godot /path/to/godot`。
- **启动前先检查：** `godot-ai-cli godot detect` 会从同样的来源探测并打印每个候选
  二进制的版本与兼容性（Godot 不在 PATH 时用
  `GODOT_BIN=/path/to/godot godot-ai-cli godot detect`）。
- **`launch` 会改动你的工程：** 内置插件会被复制到 `addons/godot_ai/`、在
  `project.godot` 中启用，并注册一个小型运行时 autoload（`_mcp_game_helper`，
  用于驱动运行中的游戏）。这些改动都可以安全提交；导出插件会在导出包中自动剥离
  该 helper，不会随构建发布。如果工程里已装过上游 hi-godot/godot-ai 插件，
  `launch` 会原地升级为内置的 fork 版本（不再使用 Python 后端）。只想对齐插件版本、
  不启动编辑器时，单独运行 `plugin install --project <dir>`。
- **CI / 无显示环境：** 加 `--headless`。依赖视口的操作（截图）需要有界面的编辑器。
- **同时开多个工程：** 让每个工程共用同一个 daemon（相同端口）。每次 launch 都会把
  该工程的编辑器作为一个新会话打开并置为活动；操作默认落在活动会话——用
  `session activate <id>` 或操作的 `--session` 标志切换目标工程，用
  `stop --session <id>` 只结束一个工程（裸 `stop` 会退出所有已连接的编辑器）。
  自定义端口（`--http-port`/`--ws-port`）会拉起独立的第二个 daemon；但端口覆盖写在
  全局共享的 EditorSettings 里，所以同一时间只能存活一套自定义端口覆盖——在一套
  激活期间用不同端口 launch 会以 `SETTINGS_OVERRIDE_ACTIVE` 失败；而多个工程可以像
  默认端口一样共用同一个自定义端口 daemon。
- **关闭：** `godot-ai-cli stop` 会让编辑器退出并停止 daemon。

### 给 Agent 装 skill

每个 release 还附带 `godot-ai-skill.zip`——一个开箱即用的 Agent skill，内含完整命令
清单、端到端操作手册、错误恢复指南，以及 CLI 安装脚本。把 zip 解压到 Agent 的 skills
目录下的新文件夹中，让 `SKILL.md` 位于 `<skills目录>/godot-ai-skill/SKILL.md`
（例如 `~/.claude/skills/godot-ai-skill/SKILL.md`），Agent 即可获得这些指引。

## 工作原理

```
agent / shell
    |  godot-ai-cli <subcommand>  ->  stdout 输出一个 JSON 对象
    v
+--------------+   HTTP 127.0.0.1:8000    +-------------------------+
|     CLI      | -----------------------> |  daemon（`serve`，即    |
+--------------+                          |  `launch` 拉起的后端）  |
                                          +-----------+-------------+
                                                      | WebSocket 127.0.0.1:9500
                                                      v
                                          +-------------------------+
                                          | godot_ai 编辑器插件     | --> 运行中的 Godot 编辑器
                                          +-------------------------+
```

一条 `godot-ai-cli launch` 即可完成：把内置插件装进工程、拉起 daemon、打开编辑器、
等待插件握手。此后每个子命令都是发给 daemon 的一次轻量 HTTP 调用，daemon 再经
WebSocket 桥转发给编辑器内的插件。全部通信仅限本机回环。拓扑、报文信封与门控
规则详见 [docs/architecture.md](docs/architecture.md)。

## 命令面

26 个域共 148 个编辑器操作（scene、node、script、signal、ui、theme、animation、
material、resource、tilemap、particle、camera、audio、input、game、test、
截图等）。直接问二进制要实时清单：

```bash
godot-ai-cli commands                    # 文本清单
godot-ai-cli commands --json --domain node
godot-ai-cli <domain> <op> -h            # 每个操作的参数、超时、写门控
```

带文字约定的同一份清单在
[skill/references/commands.md](skill/references/commands.md)。
没有具名子命令的操作可以走 `godot-ai-cli call <plugin_command>
--params '<json>'`；`batch execute --file ops.json` 可以原子执行多条插件命令。

另有无需编辑器的本地辅助命令：`image palette` 提取贴图主色调（`--grid WxH`
可按瓦片网格逐格统计，适合 TileSet 图集分析），`image probe` 采样已保存
PNG/JPEG 的指定坐标像素——贴图配色分析与截图验证都不再需要 Python 等第三方运行时。

## 更新

`godot-ai-cli update` 查询 GitHub Releases 的最新版本（stable 与 prerelease 标签都计入），
与当前构建做语义化版本比较，经确认提示后下载对应平台的 zip、按 release 校验和文件验证 SHA256，然后原地替换可执行文件
（Windows 上先改名留底；残留的 `.old` 会在下次启动时清理）。更新后需要重启。
`--yes` 跳过确认提示；在无终端环境（脚本、CI、agent 管道）下不会静默执行更新——
结果为 `"status":"cancelled"` 并附带 release 详情与提示，调用方据此以 `--yes` 重跑即可应用更新。

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

`plugin/godot_ai/` 下的编辑器插件 fork 自上游 v3.2.5，移除了遥测并禁用了
Python server 拉起逻辑（Go daemon 完全取代 Python 后端）。每一处分叉都在 GDScript
源码中以 `godot-ai-cli fork patch` 标注，并在
[docs/fork-patches.md](docs/fork-patches.md) 中逐条列出。上游许可证：MIT，
"Godot AI contributors"。见 `UPSTREAM-LICENSE.txt`。

## 许可证

MIT —— 见 `LICENSE`。
