# godot-ai-cli v0.2.0-beta.2

预发布版（prerelease）。这一版把上轮确认的九条需求（R-1…R-9）落进 CLI 与随包插件，并把随包插件全量对齐到上游 **godot-ai v4.3.0**（17 个上游提交；插件版本 4.3.0 → **4.3.2**）：新增 2 条编辑器操作与 4 条不需要编辑器的本地命令，给多行中文源码开一条不被 shell 撕掉的传参通道，主题覆写可以选择不从主题继承，`update` 与 `status` 的失败路径更可靠。

## 这是什么

`godot-ai-cli` 是一个 Go 单文件 CLI，与随包携带的 `godot_ai` 编辑器插件（fork 自 hi-godot/godot-ai，MIT）配套：一条 `launch` 命令完成插件安装 + 本地 daemon 启动 + 打开编辑器，之后约 **186 个编辑器操作**（场景/节点/脚本/信号/UI/主题/动画/材质/资源/TileMap/粒子/相机/环境/测试/截图……）全部是打印 JSON 的子命令，无需 MCP。

- **支持的 Godot**：4.7+（仅 4.x 线；4.5/4.6 与 5.x 在启动时拒绝），标准版或 .NET（Mono）构建
- **平台**：Windows / macOS / Linux（amd64 与 arm64）
- **本次插件版本**：godot-ai **4.3.2**（随 CLI 一起打包；4.3.0 / 4.3.1 的老项目需要重新安装插件，见下）

## 安装与升级

**新用户**：下载本页对应平台的 `godot-ai-cli-0.2.0-beta.2-<os>-<arch>.zip`，用 `godot-ai-cli-0.2.0-beta.2-checksums.txt` 校验（`sha256sum -c`）后解压即用；也可以用 `godot-ai-skill.zip` 里的安装脚本自动解析最新版、校验并安装。

**已在用 v0.2.0-beta.1 / 0.1.0 的用户，三步走**：

1. `godot-ai-cli update` —— 升到 v0.2.0-beta.2（GitHub API 限流时改用 `--from-atom`，已经下好包时用 `--zip … --checksums …`）；
2. `godot-ai-cli plugin install --project <你的工程目录>` —— 把工程里的 `addons/godot_ai` 对齐到 **4.3.2**（本次是 patch 级：4.3.0 / 4.3.1 → 4.3.2）；
3. **完全退出并重新打开 Godot 编辑器** —— 插件活在编辑器进程内存里，不重开不生效。

> 忘了第 2、3 步也不至于白跑：4.3.0 / 4.3.1 的插件仍能与本版 CLI 握手（patch 差异只标记 `plugin_stale` 并给出 `suggested_commands`），但插件侧的上游修复要等 `plugin install` + 重启才生效；4.2.x / 3.2.x 仍按 minor / major 不兼容拒绝。

## 用户可感的变化（按主题）

### 1. 新增 2 条编辑器操作

- `node reset-property --path /Root/Panel --property modulate`（R-3）—— 把属性**撤回序列化默认值**并丢掉场景里的 override，是 `node set-property` 的反向操作（此前只能把值改成别的，`null` 并不等于「恢复默认」）。回包给出 `old_value` / `is_default` / `removed_override` / `default_source`，`undoable` 说明能否靠编辑器撤销。
- `game ui-activate --node-path /root/Main/StartButton`（R-4）—— 触发运行中游戏里 Button 自己的 `pressed` 信号（`toggle_mode` 的按钮同时翻转 `button_pressed` 并发 `toggled`）。它是 `game input-action` 的 UI 侧对位：不合成鼠标事件，所以不依赖坐标与焦点；回包给出真正发出的 `signal` 与 `toggled`。

### 2. 新增 4 条不需要编辑器的本地命令

- `image find-color`（R-5）—— 纯 Go 读 PNG/JPEG 反查颜色：`--tolerance` 必填，算法固定为 8 位通道上的欧氏距离 `sqrt(dr²+dg²+db²)`（距离 ≤ tolerance 命中，alpha=0 的像素跳过）；`--region-size WxH` 按图集网格把命中归类，`--pixels` 给出逐个命中像素的坐标。
- `tilemap find-by-color`（R-5）—— 本地解析 `.tscn`，用同一距离规则反查命中格子，返回 `cells[]` 与平行的 `world_center[]`；同族的 `tilemap render` 回包补上 `origin` / `cell_px_size` / `layer_position`，像素坐标可以直接反推回格子坐标。
- `filesystem snapshot --tag <名字>` / `filesystem changed --tag <名字>`（R-6）—— 给工程拍一张本地快照（写进 `.godot/godot_ai_snapshots/<tag>.json`，每个文件记相对路径 + sha256 + size + mtime），再把「变了什么」列成 `changed[]`（内容变化，带 `sha_before` / `sha_after`）与 `added[]` / `removed[]`，每个文件只落在一组里；`.godot/`、`.git/` 及根级 `.mono/`、`bin/`、`obj/`、`.vs/`、`.idea/` 一律跳过并计入 `ignored_count`，差异只看 sha256。这条回答的是「刚才那次保存有没有顺手改掉我没打算改的文件」。

### 3. 两条内容通道：多行源码与中文不再被 shell 撕掉（R-1）

- `script create --content-file <文件>` 与 `filesystem write-text --content-file <文件>` —— 从 UTF-8 文件**原样**读取内容（开头的 BOM 会被剥掉），`--content-file` 与 `--content` 不能同时给。
- Windows PowerShell 5.1 会把参数里的 ASCII 双引号吃掉、再按控制台代码页重编码，多行 GDScript 里的中文注释与中文字符串就是这么坏的；把源码写进文件再交给 `--content-file`，这条路上不再有引号与代码页问题。

### 4. 主题覆写可以选择不从主题继承（R-2）

- `theme stylebox-override --fresh`（R-2）—— 默认行为逐字节不变：以节点为该槽位解析出的 stylebox 为**基底**、把 `--patch` 叠上去，于是 patch 没提到的视觉参数（典型是 ProgressBar 的 `corner_radius_*` 与 `content_margin_*`）会被静默继承。
- 传 `--fresh` 则从空的 `StyleBoxFlat` 起手、完全不查主题 —— 像素风 UI 要的就是这个（48×7 的血条不该继承 6 px 圆角）。回包新增 `overrode_existing` / `fresh`，便于区分两条路径；槽位名写错两种情况都不报错（回落到引擎兜底 stylebox），需要确认时看 `has_theme_stylebox()`。

### 5. 更新失败不再被一个残留文件卡死（R-7）

- `godot-ai-cli update`：Windows 上「删不掉的旧二进制」不再阻断后续更新 —— 旧文件仍被映射（通常是上一次以 `<exe>.old` 跑起来的进程）时改用 `<目标名>.old.<unix 时间戳>` 回落重命名，最后失败时把 `.old` 路径写进报错信息。
- 定位锁持有者也更实在：`UPDATE_REPLACE_FAILED` 带上 `data.locked_by` 与 `data.next_steps`，不再只丢一句「换不动」；残留的 `.old` 备份不再让下一次更新连带失败。

### 6. 操作总数只有一处真源，发布流程前后各断言一次（R-8）

- `script/skill-ops-count.sh` 把「操作数」收敛到唯一真源 —— 已发布二进制 `-v` 输出里的 `plugin command coverage: N ops` —— 并据此渲染 `SKILL.md` 的描述、Need/Read 表与 `references/commands.md` 标题；`--check` 只校验不写入。
- 发布流水线在**打包前**（新构建的二进制）与**打包后**（解开已打包的技能包 zip）各断言一次，两者都必须与文档一致，避免「文档写一个数、二进制是另一个数」这种只在发布页上才暴露的偏差。

### 7. `status` 里的拒绝记录不再被当成当前判决（R-9）

- `godot-ai-cli status` 的 `recent_rejections` 明确是**历史**：每条带 `age_seconds`（渲染时计算），并且只保留真正指向某次拒绝的条目。
- 之前把「拒绝进程还活着」当成「现在仍被拒绝」，会把早已结束的一次启动拒绝一直挂在状态里误导排障；现在只有单一渲染路径，字段语义与文档一致。

### 8. 随包插件升到 godot-ai 4.3.2（同步上游 v4.3.0）

上轮之后上游发布了 v4.3.0（17 个提交）；本次把插件全量对齐，并重放了本 fork 的全部改动。用户能感到的部分：

- **沙箱 / 容器**：容器与 Flatpak 环境下的编辑器启动、配置目录与进程探测按上游实现对齐（宿主配置目录仍有一处已知差异，见下方登记）；
- **进程身份识别**：判断「编辑器是否已打开、是否还在跑」改用更可靠的方式，减少被误判成「另一个进程占着这个工程」；
- **Node 类型属性解析**：NodePath / 对象槽位的类型解析更严谨，`node set-property` 写 Node 引用时更少出现「写进去了但要重开才生效」；
- **文件系统发现**：原来的超大文件整体拒写大幅收敛 —— 打包二进制（`.res` / `.scn`）不再按字节读，而是从引擎自己的依赖记录里枚举引用（#1131），移动/改名/删除可以走通；压缩容器（`RSCC` 魔数）先解压、上限按**解压后**大小判定；引用扫描按观测到的帧间隔让路（#1137），大工程不再把编辑器卡住；
- **更诚实的失败**：仍会整单失败关闭的「脚本 / 文本所有者」情形改用结构化错误码 **`FILESYSTEM_DISCOVERY_FAILED`** 说清自己卡在哪，而不是含混的 `unchanged` / `INVALID_PARAMS`；
- **运行时可观测性**：游戏内 eval 与日志链路更稳；插件停靠面板显示的版本号与 `plugin.cfg` 对齐；
- **版本钉住**：`plugin.cfg` 4.3.0 → 4.3.1 → **4.3.2**；CLI 侧握手放宽到 patch 级差异（只报 `plugin_stale` + 升级建议），minor / major 差异仍按不兼容拒绝。

> 已知缝隙（登记，不阻断）：① Flatpak 下宿主配置目录（`HOST_XDG_CONFIG_HOME`）的读取仍与上游实现有差异（RS-068）；② 超大文件拒写只是**部分**收敛 —— 打包二进制已放行，`.gd` / `.cs` / `.gdshader` / `.tscn` / `.tres` 这类「能命名目标的文本所有者」仍整单失败关闭；③ 23 个 `.gd` 文件的 fork 标记疏密不均（RS-069），同步时靠逐文件比对而不是标记自动判定；④ 新错误码 `FILESYSTEM_DISCOVERY_FAILED` 尚未写进 `references/troubleshooting.md`（登记，下一次文档更新补齐）。

## 文档与技能包

- `SKILL.md` 与 `references/commands.md` 按新的单一真源重新渲染（**186** ops）；`references/workflows.md` 补 `game ui-activate`、`theme stylebox-override --fresh`、两条 `--content-file` 内容通道、本地快照 / 改动与新的 `written_paths` / `resaved_resources` 回包字段；`references/troubleshooting.md` 补本地颜色反查与 `locked_by`、本地快照 / 改动的排查条目。
- `docs/fork-patches.md` 新增 §22（R-1…R-9 需求批次）与 §23（上游 v4.3.0 同步的逐文件处置、冲突解法、脆接口再审计、版本钉住理由、故意不同步的上游文件清单）。

完整的开发与测试约定见 [README](https://github.com/mimajiushi/godot-ai-cli/blob/main/README.md) 与随附的 `godot-ai-skill.zip`（agent 技能包，含 SKILL.md 与安装脚本）。
