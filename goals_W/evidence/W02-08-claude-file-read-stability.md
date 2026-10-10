# W02.08 / W02.09 · 设置来源读取的文件身份与路径变化

日期：2026-10-09。W02.07/08/09 保持 **in_progress**，整体 **9/40 done、Goal active**。

## 问题与交付

启动来源读取原先主要记录 JSON 字节。受管文件被同内容替换、读取中修改权限或时间、符号链接改指向，以及关闭文件后替换时，固定 SDK 仍能返回相同设置，工作台也会报告 read_complete=true。基线 worker 的五类红灯已保存在持久工作目录。

现在 SDK 读取与附加文件读取共用文件描述符、路径和字节的核对：记录设备/inode、完整模式、UID/GID、链接数、长度及纳秒 mtime/ctime，核对实际解析位置，并在读取结束和整个来源采样结束时复查。atime 不参与版本，正常读取不会令版本变化。重复读取发现身份变化后，不会被后一次成功覆盖。

文件记录新增 resolved_path 与 identity_sha256；路径解析另有 path_bindings。Go 接口保留并验证这些信息，既有前后采样比较可以拒绝“内容相同、文件或路径版本已变”的创建，或将预览标为来源不稳定。界面的“本次路径解析”展示来源与解析位置，变化仍通过“读取期间变化”提示。

实现中另补了两个有红灯依据的边界：解析后、打开前的替换不能用较新的打开记录覆盖原解析观察；realpath 的一次 EIO 即使被 SDK 回退读取掩盖，也保留为读取不完整。

这仍是有明确观察窗口的采样，不锁定外部文件，也不证明读取之后不会变化。

## 验证与环境恢复

暂停前已复现文件身份误报并开始修复，属于 progress。恢复时原句柄已不存在，日志显示当时探针失败；旧 /tmp 原始目录也已消失。初始日志保留，没有补造旧原始记录。后续使用基线 JS 副本，在 .dev/w0208-settings-read-stability-work/owned 下重新取得可审计红灯。

恢复后的[前置检查](W02-08-claude-file-read-stability/resumed-prerequisites.json)确认匿名 Unix IPC、Unix/TCP 监听和只读 PowerShell 版本均通过；当前原生执行条件已恢复，未再要求用户代跑。测试沿用独立 profile、项目、状态目录、本地模拟供应商和自有插件。

| 层次 | 实际结果 |
|---|---|
| 基线复现 | 五类文件变化误报；另保留解析至打开窗口及 realpath EIO 的失败。稳定文件/链接和文件消失作为对照 |
| 固定 SDK / 生产读取器 | 最终 **29 例、294 项通过**，含原有文件/注册表错误、同内容变化、路径解析、错误保留及读取上限 |
| 跨次真实文件采样 | **8 个独立 SDK 进程、40 项通过**：未修改时版本稳定，同字节替换/权限变化改变文件版本；同目标重新建链接只改变路径绑定 |
| Go 接收与入口守卫 | 新增字段丢失、坏元数据及跨次版本变化共 **11 个红灯**转绿；相关 race 测试 **93 个叶场景通过** |
| 当前真实 CLI | Application/Handler 插件设置预览 **33**、来源读取 **13**、普通创建五例 **25**，共 **71 项通过** |
| 当前工具/模型协议回归 | 真实 Claude CLI + 本地供应商：MCP **39**、插件 **33** 项通过，涵盖审批、拒绝、hook、结果返回、原 ID 恢复与来源漂移 |
| 浏览器 | **18 项通过**。正常/坏 JSON 来自本轮真实 Handler；受管文件竞态来自本轮 SDK 受控 OS 报告，HTTP/SSE 与这些故障的会话关联为回放 |
| 其他 | runtime/app/console 全包 race、nativeclaude tagged vet、bridge/Web 类型检查和构建、开发二进制构建通过 |
| 独立审计 | **79 项通过**：按实际 OS 元数据重算身份 SHA，核对失败、原生日志、hook/工具记录、HTTP 计数、回收路径、浏览器及旧证据哈希 |

原生预览只保留原查询的 project/custom/plugin 三次启动 hook；来源读取保留原有一次 marker。五类创建中，无效权限格式没有原查询和 hook；有效创建的 user/project hook 各一次。元数据验收无模型输入或结果，预览没有增加 HTTP 请求。MCP/插件回归的模型协议全部指向自有本地供应商。

最初夹具加入 realpath 映射后，SDK 改读规范路径，原故障注入没有命中；修正路径集合后再分别运行基线与当前 worker。文件消失样本最初还错误要求保留已被后续缺失记录覆盖的字节哈希，已修正为检查变化诊断。这些失败日志与初版源码均保留，不计作生产缺陷已通过的证据。

## 复验与版本

~~~bash
npm --prefix bridges/claude run build
python3 tests/claude_startup_preflight_probe.py
python3 tests/claude_startup_file_version_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime ./internal/app ./internal/console
NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -tags nativeclaude -race ./internal/runtime ./internal/app -run '^TestClaudeNative(StartupSettingsRead|PluginSettingsPreview|CreateRejectsInvalidSettingsBeforeStartup)$' -count=1 -v
python3 tests/claude_mcp_selection_smoke.py --netcap .dev/bin/netcap
python3 tests/claude_plugin_execution_smoke.py --netcap .dev/bin/netcap
npm --prefix web run build
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./cmd/... ./internal/... ./web
GOCACHE=/tmp/netcap-w0206-auth-go-cache go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
~~~

SDK 为 **0.3.280**，原生 CLI **2.1.280**，Node **v24.13.0**。系统策略变化用受控文件/注册表回复；真实原生验收另外读取了当前宿主，两项注册表来源均明确缺席。没有将测试策略注入视为真实企业策略准入。

- 开发二进制 SHA-256：3aa93ca69f8939a64121a8b490b2d042267470e32405ccad15e5d21c88a423ca。
- settings_preflight.js SHA-256：80578b6f58c31445eaf324e5a8b66c0989fc134e89285eff59ceca5908449fc9。
- 旧 bin/netcap 保持 e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf。
- **8,170** 项旧 W 证据与 **31** 项旧 goals 证据未变，其他 bridge 输出和依赖未变。生产 Go 变更仅在来源读取结果边界；自有测试进程已退出，浏览器已关闭，未提交。

工作目录为 .dev/w0208-settings-read-stability-work。归档包括[重新取得的基线红灯](W02-08-claude-file-read-stability/resumed-red.log)、[解析至打开红灯](W02-08-claude-file-read-stability/resolve-open-red.log)、[路径读取错误红灯](W02-08-claude-file-read-stability/realpath-error-red.log)、[最终 SDK 检查](W02-08-claude-file-read-stability/sdk-release.log)、[最终原生检查](W02-08-claude-file-read-stability/native-release.log)、[审计](W02-08-claude-file-read-stability/audit.json)、[源码差异](W02-08-claude-file-read-stability/source.diff)、[原始文件索引](W02-08-claude-file-read-stability/archive-index.json)及[manifest](W02-08-claude-file-read-stability/manifest.json)。截图可见[路径解析](W02-08-claude-file-read-stability/resolved-path-1440.png)与[窄屏变化诊断](W02-08-claude-file-read-stability/changed-source-390.png)。

## 剩余条件

完整受管/远端策略的真实性与新鲜度、helper 执行准入、有效权限、所有外部竞态、正式已安装插件选择和原 ID 持久事务仍未完成。本片不冻结全目录或运行状态，complete 保持 false。

下一片接回完整启动准入、已安装插件持久候选与原 ID 应用，并利用已恢复的环境推进四配置联合条件。真实方法/Gate、独立前台、共享后台/输入独占、第二 Windows 环境与全部首版硬要求保留；本片通过不提升整包完成状态。
