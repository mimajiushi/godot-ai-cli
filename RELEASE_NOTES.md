# godot-ai-cli v0.2.0-beta.1

预发布版（prerelease）。0.1.0 之后的第一批功能更新：补齐「场景内联资源」这条链路（内联 shader 材质的指派/共享/读写）、给截图补上坐标系与基线对比、把 PowerShell 5.1 吞引号这类传参硬阻塞从 CLI 侧根除，并让 `update` 在 GitHub API 限流时仍有降级通道。随包插件升到 **godot-ai 4.3.0**（minor 升级，升级方式见下）。

## 这是什么

`godot-ai-cli` 是一个 Go 单文件 CLI，与随包携带的 `godot_ai` 编辑器插件（fork 自 hi-godot/godot-ai，MIT）配套：一条 `launch` 命令完成插件安装 + 本地 daemon 启动 + 打开编辑器，之后约 **184 个编辑器操作**（场景/节点/脚本/信号/UI/主题/动画/材质/资源/TileMap/粒子/相机/环境/测试/截图……）全部是打印 JSON 的子命令，无需 MCP。

- **支持的 Godot**：4.7+（仅 4.x 线；4.5/4.6 与 5.x 在启动时拒绝），标准版或 .NET（Mono）构建
- **平台**：Windows / macOS / Linux（amd64 与 arm64）
- **本次插件版本**：godot-ai **4.3.0**（随 CLI 一起打包，老项目里的插件需要重新安装，见下）

## 安装与升级

**新用户**：下载本页对应平台的 `godot-ai-cli-0.2.0-beta.1-<os>-<arch>.zip`，用 `godot-ai-cli-0.2.0-beta.1-checksums.txt` 校验（`sha256sum -c`），解压即用；也可以用 `godot-ai-skill.zip` 里的安装脚本自动解析最新版 + 校验 + 安装。

**已在用 0.1.0 的用户——按这三步走**（本次插件是 **minor 升级**：4.2.5 → 4.3.0，新旧之间仍有握手兼容门禁）：

1. `godot-ai-cli update` —— 拿到 v0.2.0-beta.1（GitHub API 限流时改用 `--from-atom`，或已经下好包时 `--zip … --checksums …`）；
2. `godot-ai-cli plugin install --project <你的工程目录>` —— 把项目里的 `addons/godot_ai` 对齐到 4.3.0；
3. **完全退出并重新打开 Godot 编辑器** —— 内存里的插件随编辑器进程存活，不重开不生效。

> 忘了做第 2、3 步也不会再白跑：`status` 现在会说清**哪一侧**是旧的（`stale_side`）以及该敲哪条命令（`suggested_commands`）。如果旧的一侧其实是 daemon，提示会直接改成 `godot-ai-cli launch --project <dir> --upgrade-daemon`——保留已打开的编辑器，只换 daemon。

## 用户可感的变化（按主题）

### 1. 场景内联 shader 材质：指派、共享、读写（R-1、R-2）

此前 `material apply-to-node --type shader` 不读 shader 路径，会**静默**挂上一个 shader 为 null 的材质；内联材质（`.tscn` 里的 `[sub_resource]`）既没有写 uniform 的入口，也没有「两个节点共用同一份实例」的出口，只能手写 `.tscn` 文本。现在：

- `material apply-to-node --node-path Sprite --type shader --shader-path res://…gdshader --props '{"shader_parameter/flash_mode":1,"resource_local_to_scene":true}'` —— 缺 `--shader-path` 明确报错（不再静默），`--props` 支持 `shader_parameter/<uniform>` 与 `resource_local_to_scene`，默认**内联不落盘**：`scene save` 后 `.tscn` 里就是 `[sub_resource type="ShaderMaterial" …]`；
- `material assign --node-path Funnel --from-node-path Sprite` —— 把已存在的内联材质**同一实例**挂给第二个节点（回包 `shared:true`，落盘只有一份 `[sub_resource]`）；
- `resource set-property --node-path Sprite --property material_override --resource-property resource_local_to_scene --value true` —— 新命令，往节点槽位上已有的资源写字段，回包带 `old_value` / `new_value`；
- `material set-shader-param --node-path Sprite --param flash_mode --value 1` 与 `material get --node-path Sprite` —— 内联材质的 uniform 读写（`get` 回全量 `shader_parameter_values` 字典）；原有的 `--path <材质文件>` 通道一字未动；
- `resource create --type ShaderMaterial --properties '{"shader":…,"shader_parameter/pulse":0.5}'` —— **同一次调用**既写 shader 又写 uniform 现在真的成立（此前同一条命令 5/5 失败：`PROPERTY_NOT_ON_CLASS`，而报错里的 `valid_properties` 反而列着被拒的键）。

### 2. 传参不再被 shell 吞掉（R-4 问题 1、R-2）

Windows PowerShell 5.1 调原生程序会剥掉参数载荷里的 ASCII 双引号，含 `"` 的 `--old-text` / `--params` 根本进不了 CLI。现在有三条文件通道（第一条对所有 op 命令生效）：

- `--params-file <path>` —— JSON 基值从文件读（自动剥 BOM），优先级 **文件 → `--params` → 显式 flag**；
- `node set-property … --value-file <json>` —— 任意 JSON 值；
- `script patch --old-file <a> --new-file <b>` —— **文本直读**，引号、反斜杠、换行原样保留。

### 3. `script patch` 不再假报解析错误（R-4 问题 2）

编辑器异步重载的时序抖动会被误报成 `level:"error"` + `reload_reason:"parse_error"`，让人回头去改一个本来正确的文件。现在抖动降级为 `level:"info"` + `reload_jitter:true`，回包给 `reload_pending:true` / `reload_reason:"reload_pending"`；**真**解析失败仍是 `error` + `parse_error`，没有被一律降级。

### 4. `scene open --force-reload` 真的重读磁盘（R-2）

在编辑器外手改 `.tscn` 后，当前编辑场景第一次调用就会 `reloaded_from_disk:true`（走 `EditorInterface.reload_scene_from_path()`），不必先 `filesystem scan`。目标开在别的页签时仍只能切页签，但回包会带 `hint` 指向 `filesystem scan` + 重试，不再静默回 `false`。

### 5. 截图的坐标系入口（R-3）

同一台机器上两个独立 agent 都踩过同一个坑：游戏侧给的是**画布坐标**，而 `--region` / `--assert` 吃的是**源图像像素**，回包里没有任何字段能揭示两者差一个 stretch 比例。三条修法都给：

- 回包补 `canvas_size` / `canvas_scale` / `note`（game 源用真实窗口比例）；
- `--coords canvas` —— 用画布坐标写 `--region` / `--assert`（自动乘比例、向下取整；该模式强制整帧，忽略 `--max-resolution`）；**默认 `--coords image` 的行为逐字节不变**；
- 新命令 `game node-screen-rect --path <运行中的节点>` —— 一次给出 `canvas_rect` / `image_rect` / `scale`，并用 `rect_kind` 区分「真实包围盒」(`bounds`) 与「只有原点、没有固有尺寸」(`origin_only`)，绝不伪造矩形。

缺元数据时（旧插件 + 新 CLI）报 `COORD_SPACE_UNAVAILABLE`，而不是猜一个比例。

### 6. 截图基线对比（R-2）

`editor screenshot --baseline <png> --diff-threshold 0.02 --diff-out <png>`：CLI 本地逐像素比对**最终图**（`--region` 裁剪之后），差异超阈值 → exit 1 + `BASELINE_DIFF_FAILED`（`diff_ratio` / `diff_pixels` / `samples` / `diff_out`）；`--diff-out` 把差异像素涂成 `#FF00FF`，失败路径也会落盘。验证 12Hz 正弦频闪这类「随时间振荡」的视觉，不用再连拍 + 逐张 `image probe`。

### 7. 升级流程不再空转（R-5）

`status` / `launch` 的 `plugin_stale` 现在会说清**方向**：插件比 daemon 新 → `stale_side:"daemon"` + `daemon_bundled_version` + `suggested_commands:["godot-ai-cli launch --project <dir> --upgrade-daemon"]`；插件比 daemon 旧 → `stale_side:"plugin"` + `plugin install` 两步建议；两侧版本无法比较时不猜方向（相关字段整体省略）。附带：`known_daemons[].cli_version` 与 `status.daemon.cli_version` 能看出「这个 daemon 是哪个 CLI 版本起的、要不要重启」。

### 8. `update` 在限流下仍能升级（R-6）

- 403 + `X-RateLimit-Remaining: 0` → `UPDATE_CHECK_RATE_LIMITED`，带 `rate_limit_reset`（RFC3339 UTC）与 `next_steps[]`（三条降级通道怎么写）；
- `--tag <vX.Y.Z>` —— 走单 release 端点，绕开列表查询（tag 不存在 → `UPDATE_TAG_NOT_FOUND`）；
- `--from-atom` —— API 不可达时从 `releases.atom` 取最新 tag 后走同一路径（失败 → `UPDATE_ATOM_FAILED`）；
- `--zip <已下载 zip> --checksums <checksums.txt>` —— 完全离线，双源校验（文件名命中 + sha256 一致）通过才替换，失败不留半成品；不给 `--checksums` 直接拒绝。

> 已知缝隙（登记，不阻断）：① GitHub 的二级限流（403 + `Retry-After`、无剩余额度头）仍报 `UPDATE_CHECK_FAILED`（带 `http_status`），不会被误报成限流；② `--tag` 用于「装指定的新版」，不能用来回退安装（等于或旧于当前版本时按「已是最新」处理，不下载）；③ `--zip` 只做「文件名命中 checksums + sha256 一致」双源校验，不校验包内二进制是否匹配当前 OS/架构。

## 文档与技能包

- `references/commands.md` 随本次命令面重新生成（新命令 `resource set-property` / `game node-screen-rect`，新参数 `--params-file` / `--value-file` / `--old-file` / `--new-file` / `--coords` / `--baseline` / `--diff-threshold` / `--diff-out` / `--tag` / `--from-atom` / `--zip` / `--checksums` / `--shader-path` / `--from-node-path` / `--node-path`），与生成器逐字节一致；
- `references/troubleshooting.md` 新增 `COORD_SPACE_UNAVAILABLE` / `BASELINE_READ_FAILED` / `BASELINE_DIFF_FAILED` / `UPDATE_CHECK_RATE_LIMITED` / `UPDATE_TAG_NOT_FOUND` / `UPDATE_ATOM_FAILED` 的排查说明，以及上面三条已知缝隙的登记。

完整的开发与测试约定见 [README](https://github.com/mimajiushi/godot-ai-cli/blob/main/README.md) 与随附的 `godot-ai-skill.zip`（agent 技能包，含 SKILL.md 与安装脚本）。
