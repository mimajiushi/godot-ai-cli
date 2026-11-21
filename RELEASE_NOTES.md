# godot-ai-cli v0.2.0-beta.3

预发布版（prerelease）。这一版把随包插件从上游 **godot-ai v4.3.0** 一路同步到 **v4.4.2**，补齐对应的 Go CLI/daemon 行为，并把实机回归中发现的问题修到通过。随包 fork 插件版本为 **4.4.3**，编辑器操作面从 186 增至 **217 个操作、29 个域**。

## 这是什么

`godot-ai-cli` 是一个 Go 单文件 CLI，与随包携带的 `godot_ai` 编辑器插件配套。一条 `launch` 命令完成插件安装、本地 daemon 启动和编辑器打开；之后的场景、节点、脚本、动画、资源、测试、运行中游戏输入和截图等操作，都是打印 JSON 的子命令，不需要另设 MCP server。

- **支持的 Godot**：4.7+（仅 4.x 线；4.5/4.6 与 5.x 会拒绝启动），标准版或 .NET（Mono）构建。
- **平台**：Windows / macOS / Linux，amd64 与 arm64。
- **本版插件**：fork **4.4.3**，内容为上游 **v4.4.2** 加 fork 补丁；认证传输仍为 protocol v2。
- **命令面**：`godot-ai-cli -v` 报告 `plugin command coverage: 217 ops`；`commands --format md` 生成同源的完整目录。

## 安装与升级

**新用户**：下载本页对应平台的 `godot-ai-cli-0.2.0-beta.3-<os>-<arch>.zip`，用 `godot-ai-cli-0.2.0-beta.3-checksums.txt` 校验后解压；也可以使用 `godot-ai-skill.zip` 中经过校验和验证的安装脚本。

**已有安装的用户**：

1. 执行 `godot-ai-cli update` 升到 v0.2.0-beta.3。
2. 对每个 Godot 工程执行：
   ```bash
   godot-ai-cli plugin install --project <你的工程目录>
   ```
   将 `addons/godot_ai` 对齐到 **4.4.3**。
3. **完全退出并重新打开 Godot 编辑器**。磁盘文件更新不会替换编辑器内存里已经加载的 GDScript；只重载 CLI 或只重启 daemon 不够。

从 **4.3.x** 升到本版会跨越 major.minor 握手边界，旧 4.3 插件会被 4.4 daemon 拒绝，必须完成插件安装和编辑器重启。4.4 系列内的 patch 差异可以握手并标记 `plugin_stale`，但新操作和修复仍要安装插件并重启后才会出现。

## 用户可感的变化

### 1. 场景、节点和资源编辑更完整

- `scene close` 可以关闭场景 tab；有未保存修改时默认拒绝，只有显式 `--discard-changes` 才会放弃，关闭操作本身不可撤销。
- `node set-editable-children` 显式控制实例的 Editable Children。真实 locked 实例的内部写入会先拒绝，避免保存时静默丢失；普通本地 owner 链不会被误判成实例。
- `filesystem import-get` / `import-set` 读取和修改顶层 import 选项，支持 revision 防陈旧写入和真实 native reimport。`import-set` 与 `reimport` 的等待预算为 120 秒；客户端超时不会取消 Godot 已经开始的导入。
- `resource set-properties` 可以修改既有 `.tres` / `.res` 而不重置未提供属性；新增 SpriteFrames 和 TileSet atlas 构建器，并保留 UID、处理已加载资源引用和 staged recovery 信息。
- 支持有界 PackedByteArray 属性写入、typed node arrays，以及更严格的嵌套资源和实例写入校验。

### 2. 动画与音频 authoring 大幅扩展

- AnimationTree 支持状态机、BlendTree、1D/2D BlendSpace、图节点、连接、transition 和 blend point；AnimationLibrary 可以挂载、列出和移除。
- 新增音频总线列表、创建、修改、删除、effect 增删改和 AudioBusLayout 导出。
- 这些能力全部进入 typed CLI，并纳入插件命令一致性检查；没有只存在于上游 Python 工具描述、却无法从本 CLI 到达的“纸面操作”。

### 3. 截图、运行中游戏和输入更可靠

- cinematic 截图支持成对的 `--width` / `--height`、`--save-to` 与 `--overwrite`，可以把完整 PNG 保存到工程并只返回文件元数据。
- `--full-res` / `max_resolution=0` 现在明确只取消请求的最长边上限；内联 PNG 仍可能受 MCP/SSE 字节预算缩小。需要原尺寸文件时使用 cinematic `save_to`。
- `game input-mouse` 会保持 left/right/middle 的按住状态，motion 支持一次性 `buttons` 覆盖，wheel 不进入 held 集合。
- 重复执行 `project run` 在游戏已经运行时返回 `was_already_running:true`，不再误报 `EDITOR_PLAYING`。其它 playing 状态的写操作仍然拒绝，并带新鲜 `game_status`。
- 修复了内部 readiness probe 发送 `params:null`、被真实插件拒绝的问题；probe 现在发送空 Dictionary。

### 4. Windows 进程与端口诊断更稳妥

- 当 netstat 有其它 TCP 行、却漏掉目标 listener 时，不再直接把端口当空闲：目标缺席先经过 bind gate，bind 失败才调用 PowerShell 的 Get-NetTCPConnection fallback。
- 真正空闲的端口不启动 PowerShell；已可见 listener（包括 wildcard）不会重复 bind；UNKNOWN 继续失败关闭，不会伪装成 FREE。
- 同步上游 #1249：Windows 父 PID 已被更晚创建的进程复用时，ancestry 会截断该链接，避免错误循环或品牌继承；same-age cycle 仍会被拒绝。

### 5. 上游客户端与启动策略修复也随插件保留

- OpenCode 的 `.jsonc` 可以带注释读取；若重写会丢掉 Godot AI 所在 tier 的注释，则 Configure/Remove 会拒绝而不是破坏文件。
- 上游 uv 启动策略显式请求 `>=3.11,<3.15`，并识别过旧 uv；`plugin.cfg` 带有新的 dependency cutoff 和 `python_request`。
- 这些上游 Python/uv 策略保留在 dormant 插件代码中，**不会改变普通 godot-ai-cli Go daemon 路径的运行依赖**。fork 的遥测移除、客户端配置 UI/worker 关闭和外部 daemon 门控继续保留。

## 实测与发布验证

以下不是静态预测，而是 t23 在 upstream v4.4.2 / fork 4.4.3 上的统一终验；原始证据位于 `.replay-lanes/lane-1/out/t23-final-v442/`。

### Headless pristine 全量

在隔离 lane 1、Godot 4.7.2 headless、打开 `res://main.tscn` 且无游戏运行的前提下：

- **102 suites / 3186 tests**，与静态文件及 `func test_` 重数一致。
- **3115 passed / 0 failed / 71 skipped**。
- **20263 assertions，35587 ms，load_errors=[]**；verbose results 完整覆盖 3186 条，没有缺失或额外 suite。
- `client_repair` 为 31 total / 26 passed / **5 skipped** / 0 failed / 645 assertions：4 个 worker 条件 skip + 1 个 UI 条件 skip，不是把失败改成跳过。
- 原 node 9 条失败全部通过，node 总计 230/230；真实实例套件 20/20。handoff 连跑 3 次通过，目标测试每次实际执行 17 条 assertions。
- Windows 原 4 个 listener 失败均 3/3 转绿；free-port 不 fallback、wildcard 和 UNKNOWN 路径同时通过。
- v4.4.2 ancestry 控件为 2 passed / 2 deselected；uv policy 11/11、client attach 57/57。

### Windowed 实机

- 已运行游戏的第二次 `project run` 成功 no-op，run_token 不变；普通 `scene save` 仍被 EDITOR_PLAYING 拒绝并返回同一 run 的新鲜状态。
- cinematic `save_to` 实物 320×180；full-res out 640×360；crop 160×90。viewport 原始 2980×1432，内联结果 1338×642，实际证明字节预算与 full-res 文档语义一致。
- game capture 为 1920×1080；鼠标 held/override/wheel/release 与坐标不变断言通过。
- 窗口化 editor、camera、capture、vision、play metadata、game helper 和 screenshot coordinate suites 均通过。

### 更新基线后的 Smoke 复跑

t23 时 smoke 的唯一失败是尚未更新的 88/2877 旧钉；t15 更新为 102/3186 后，t34 已在隔离 lane 1 完整复跑：

- `exit_code=0`，**PASS=11 / FAIL=0**。
- full 为 102/3186、3116 passed、70 skipped、34858 ms，failed=0 且 load_errors 为空。
- headless screenshot 按预期 SKIP，stop=stopped，EditorSettings byte-identical。
- t34 比 t23 pristine 少 1 个 skip（70 vs 71），属于环境条件漂移；suite/total/failed/load_errors 没有变化，不把不同运行的 skip 数写成固定常数。

证据：`.replay-lanes/lane-1/out/t34-smoke-baseline/t34-report.md` 与同目录 `smoke-e2e.log`。

Go 全量测试、gofmt、三平台 vet、文档覆盖和 source/snapshot ops-count 在 t23 均通过；最终发布前仍会再跑文档与双 ops-count 闸门。

## 已知限制与未纳入内容

- **Flatpak**：插件与 Go daemon 对 `HOST_XDG_CONFIG_HOME` 的处理仍有已知分歧，RS-068 继续登记；本版没有宣称解决。
- **历史 marker 归属**：部分旧 fork 文件的 marker 覆盖仍不均匀，RS-069 要求按文件和 hunk 审计，不能只靠 marker 数量判断同步完整性。
- **Windows 边界**：bind-gated fallback 不扩大宣称到“netstat 完全不可见的 wildcard listener 一定不存在”；资源 staged replacement 在 Windows 上也不是 crash-atomic。
- **上游 main #1260 未纳入**：插件 reload 后通过 activity beacon 重新接管已运行游戏的修复不在 v4.4.2 release，也不在本版。
- 导入器切换、imported subresource 写入、Animation IK 等上游已登记边界仍需按各命令的错误与文档处理。

## 文档与审计

- `references/commands.md` 由 fresh binary 机械生成，217 ops 与 SKILL、二进制和命令目录一致。
- `docs/fork-patches.md` 记录 v4.4.1、v4.4.2 的逐文件三方合并、fork 补丁保留和最终实测；`tools/regression-scenarios.md` 的 RS-070～RS-080 记录 headless/windowed 重放方法与证据。
- 本版仍遵循无遥测、只绑定 loopback、不触碰其它项目 daemon 的边界。发布正文不是 commit 列表替代品；完整审计细节见随仓库文档与上游 release 对照。
