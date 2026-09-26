# godot-ai-cli v0.1.0

首个正式版。经过 27 个 beta 的全量回归打磨（每一条用户报出的缺陷都有对应的回归场景钉住），0.1.0 宣告核心契约稳定：单二进制驱动活 Godot 编辑器，任何能跑 shell 的 agent 都能操作 Godot。

## 这是什么

`godot-ai-cli` 是一个 Go 单文件 CLI，与随包携带的 `godot_ai` 编辑器插件（fork 自 hi-godot/godot-ai，MIT）配套：一条 `launch` 命令完成插件安装 + 本地 daemon 启动 + 打开编辑器，之后约 **182 个编辑器操作**（场景/节点/脚本/信号/UI/主题/动画/材质/资源/TileMap/粒子/相机/环境/测试/截图……）全部是打印 JSON 的子命令，无需 MCP。

- **支持的 Godot**：4.7+（仅 4.x 线；4.5/4.6 与 5.x 在启动时拒绝），标准版或 .NET（Mono）构建
- **平台**：Windows / macOS / Linux（amd64 与 arm64）

## 安装与升级

下载本页对应平台的 `godot-ai-cli-0.1.0-<os>-<arch>.zip`，用 `godot-ai-cli-0.1.0-checksums.txt` 校验（`sha256sum -c`），解压即用，无需安装步骤。也可以用 `godot-ai-skill.zip` 里的安装脚本自动解析最新版 + 校验 + 安装。已在用旧版的用户：`godot-ai-cli update`（支持 `--proxy`，TUN/代理环境可用）。

## 核心能力一览

- **编辑器生命周期**：`launch`（headed / `--headless` / `--attach` 接入已打开编辑器 / `--upgrade-daemon` 热升级 daemon）、`status` 全景（多 daemon 发现、版本新旧标注、死记录 `--prune`）、`stop`
- **编辑器操作**：约 182 个子命令，覆盖场景树、节点属性、脚本与信号、UI 与主题、动画、材质、粒子、相机、环境、TileMap、游戏控制（`project run/stop`、`game input-*`）、截图与录屏
- **测试**：`test run` 在编辑器内跑 GDScript 套件并回传结构化结果（pass/fail/assertions）
- **版本治理**：daemon/插件 capability 握手与 minor 兼容门禁，版本错配时插件自阻并自报原因，`status` 与错误数据里能看到「谁被拒、为什么」
- **自更新**：`update` 带代理诊断、截断重试、失败不留半成品

## 自 beta 线收敛的重要修复（用户可感）

- **握手拒绝全程可见**：插件版本与 daemon 不匹配被拒时，CLI 侧 `status` / 操作报错里直接给出对端版本、原因与修法（含 v4 插件探针自阻后主动向 daemon 自报的通道）
- **`launch --upgrade-daemon` 不再「报错但已生效」**：升级前已连接的编辑器未重连时明确报 `daemon_upgraded:true` + 重启指引；从未连接的编辑器仍 fail-closed 且错误数据自解释
- **双开守卫可靠**：`status --project` 支持相对路径（与 `launch` 对齐），未连接编辑器不再静默漏报；`--prune` 不再对 last-daemon 幻影记录重复虚报
- **测试通道密封**：`test run` 不受同会话游戏运行/编译错误日志残留污染；修复 Godot 4.7 下调试器错误树 freed-root 守卫失效的真实缺陷
- **跨平台编辑器进程扫描**：WMI 引号参数、Windows 盘符路径在非 Windows 的解析均已钉死

完整的开发与测试约定见 [README](https://github.com/mimajiushi/godot-ai-cli/blob/main/README.md) 与随附的 `godot-ai-skill.zip`（agent 技能包，含 SKILL.md 与安装脚本）。
