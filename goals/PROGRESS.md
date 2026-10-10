# 实施进度与证据索引

W01–W07 的任务文件和状态维护已转到 [goals_W](../goals_W/README.md)及 [W 系列进度](../goals_W/PROGRESS.md)。本页保留 T00–T15 的实现状态、迁移前 W 记录和原证据路径；旧 T 包继续遵循 [EXECUTION](EXECUTION.md)，后续去向见 [路线映射](../docs/implementation-plan.md)。

## 迁移前 W 状态快照（2026-09-30）

以下是建立 goals_W 前的归档状态，不作为新的执行入口；最新子任务状态以新目录为准。

当前交付：W02 最小快照/外部引用之后，Codex profile-defined stdio MCP 原生选择已贯通生产 Application / Runtime / Web，支持逐会话启用/禁用、按线程核对加载、单次原生审批和重启保持。[最新实现与边界](../docs/workbench-boundary-results.md#10-codex-stdio-mcp-原生选择与加载核对)、[机器可读证据](evidence/W02-native-selection.json) 已就绪。原引用继续只记录；[快照证据](evidence/W02-snapshots.json)、[先前原生故障/MCP 调用](evidence/W01-W02-native-lifecycle.json)、[功能清单](../docs/netcap-feature-ownership.md)和 [W03 原型](../docs/workbench-prototype.html)保留。W01/W02 为 in_progress，下一片补显式改配和版本生效边界；W03 本地走查完成、用户试用待反馈，W04–W07 未开始。严格六入口、Claude worker 工具结果和完整有效配置仍有缺口。

## T 系列状态

2026-09-30 本机 TCP/Unix socket 检查与本轮原生实验均可运行，T07 的旧环境阻塞已经解除。T07 保留“代码已有、完整验收待续”，不记完成；其保活链路不再作为新平台 Goal/Task 的强制前置。

| 工作包 | 状态 | 证据/结果 | 下一步 |
|---|---|---|---|
| T00 | done | [基线与检查](evidence/T00.md) | 已建立源码快照、本地 Git、隔离检查入口 |
| T01 | done | [三类协议实验](evidence/T01.md)；[实际结果](evidence/T01-results.json) | 六个 agent/route 组合通过，M0 完成 |
| T02 | done | [Web 纵切与验证](evidence/T02.md)；[浏览器结果](evidence/T02-browser.json) | 生产构建、双轮流式、重启和隔离/旧版回归通过 |
| T03 | done | [交互与恢复](evidence/T03.md)；[浏览器结果](evidence/T03-browser.json) | M1 本地验收通过 |
| T04 | done | [Claude bridge 与故障验证](evidence/T04.md)；[浏览器结果](evidence/T04-browser.json) | 本地验收通过，M2 等待 T05/T06 |
| T05 | done | [HTTP/SSE、交互与故障验证](evidence/T05.md)；[浏览器结果](evidence/T05-browser.json) | 每会话隔离与完整本地矩阵通过；资源回收/规模验收归 T14 |
| T06 | done | [统一工作台与 Alpha](evidence/T06.md)；[浏览器矩阵](evidence/T06-browser.json) | M2 Alpha 本地验收通过；继续 T07 |
| T07 | in_progress | [路由与探测进展](evidence/T07.md)；[绑定/doctor](evidence/T07-route-bindings.json) | 旧环境阻塞已解除；原生/HTTP/browser 完整验收待续，新路线按路由绑定与插件探测分别复用 |
| T08 | pending | — | 等待技术前置 |
| T09 | pending | — | 等待技术前置 |
| T10 | pending | — | 等待技术前置 |
| T11 | pending | — | 等待技术前置 |
| T12 | pending | — | 等待技术前置 |
| T13 | pending | — | 等待技术前置 |
| T14 | pending | — | 等待技术前置 |
| T15 | pending | — | 等待技术前置 |

## 续接记录

2026-09-30：完成 W02 Codex stdio MCP 原生选择片。新建时选择自动进入原快照和引用；配置层只允许已登记 user 来源，实际加载按原线程完整分页核对，其余 MCP 保持继承。网页原生单次审批、拒绝、重复/过期回复、不可用服务阻断、控制台/daemon 重启后原 ID/快照保持通过。旧 Codex/Claude 快照、Codex 命令审批/问题回归和 18 项浏览器检查通过，全量 race/vet/生产构建通过。当前不支持原会话改配、Claude/Skill/CLI 插件选配、远程 MCP 或完整有效配置；未修改用户配置、提交或推送。下一片补 W02 改配与版本边界，W01 硬入口缺口保留。

2026-09-30：完成 W02 最小启动快照与外部能力引用片。快照与创建意图一起持久化，后续操作引用同一记录；资源目录的脚本等依赖变化可发现并阻止继续输入。登记表更新/移除不改写旧版本；旧记录不补造历史。真实 Codex/Claude 各三轮、控制台崩溃重启、来源变化/恢复及引用不隐式加载通过；Web 的新建引用、快照核对、漂移拒绝和窄屏走查通过，全量 race/vet 与生产构建通过。当前只记录主配置与显式外部来源，尚未实现全部有效配置或原生选配。未提交、推送或改变用户配置；下一片接原生选择与加载证据。

2026-09-30：继续 W01 原生故障补验并衔接 W02。Claude 真实审批、attach 客户端崩溃及 supervisor 重启保留原 worker/工具/ID/结果；worker 被杀后新 worker 报 idle 时原工具仍存活，完成结果未进入后续请求，保留明确缺口。复现并修复生产恢复使用展示缓存授予控制的问题，新增 `ObserveFresh`。同一 stdio MCP 定义的 Codex A/B 实际调用、完整结果、daemon 重启后同 ID 排除通过，Claude A/B 回归通过。全量 race/vet、构建、生产 Codex 控制与 Claude 空闲回收后恢复检查通过；UI 未改，本片未重复浏览器检查。未提交、推送或调用真实供应商。下一片按 W02 最小执行快照与能力引用推进，W01 的硬入口及工具结果缺口继续列示。

2026-09-30：继续按序实施 W01/W02。先复现恢复未查在途工具、旧人工消息反复触发接管、观察静默改 ID、同内容配置目录改指绕过身份四类问题，再修复。生产控制台新增持久恢复核对、外部输入基线、原生 ID/状态目录守卫及共享目录提示。真实 Codex TUI 审批、在途工具跨 Go 崩溃且只执行一次、恢复控制通过；Claude 前台退出后完整 ID 后台恢复通过；共享 MCP A/B 实际调用、凭据/目录绑定及排除后同 ID 恢复通过。全量 race/vet、生产构建和新增回归检查通过，真实 Web 的恢复/历史/别名/窄屏检查通过。没有改用户配置、提交或推送；严格独立前台实时接入、Claude 工具在途及 W02 完整能力快照等仍待续。

2026-09-30：按用户要求依序补齐功能清单、定向补验与 W03 原型。六个原生样本均完成：Codex 真 TUI 三轮串行接续、Claude 双 supervisor、同 ID 跨命名空间恢复通过；Codex 独立前台只确认未发现共享 socket；Claude 普通/显式 ID 两条前台转换均保留历史但改变实际原生 ID，因此整组返回非零，严格接续未通过。W03 的 17 项交互检查、三个宽度下的五场景布局检查通过，无脚本异常。全部为自有临时配置与本地模拟供应商，生产代码未改、未提交或推送。下一步继续 W01 的身份/输入权/工具在途与 W02 实际能力绑定；W04 前验证 Windows/WSL 最小控制链路。

2026-09-30：用户确认先交付新路线、关键能力实验和产品 V0.3。新增两份可重跑原生 CLI fixture；四配置并发/原会话恢复、两 CLI 的测试 Skill/MCP/插件选配、Codex 共享后台第二客户端接续、Claude 原生后台 TTY attach 通过。Claude settings/env 合成认证的两条后台路径未通过，profile `apiKeyHelper` 路径通过；保留失败记录，不将 SDK Adapter 整体提升为热接管支持。最终 CLI 实验、Python 语法/帮助入口、12 份文档的引用/表格/围栏、27 项需求覆盖和证据哈希检查通过；自有测试进程已清理。未修改生产代码、未提交或推送、未运行真实供应商、Windows 或 T07 完整验收。下一步入口见新实施计划第 6 节。

2026-09-28：T00 的 race（禁用缓存）、vet、独立构建和旧 CLI 集成全部通过。用户授权在项目建立本地 Git，已初始化 main，未提交或配置远端。原 `bin/netcap` 和共享后台保留。T01 已取回 OpenAI App Server 官方文档并查询 Claude/OpenCode 官方索引，接下来固定 SDK 开发依赖并实测三类最小协议。

2026-09-28：T01 三类 × 两路由全部通过两轮、结束后恢复、第三轮及实际端点/凭据/模型/历史检查。可重复入口 `make runtime-deps && make runtime-contracts`；配置策略与未验能力见 `tests/runtime/README.md`。T02 契约指纹存于 `.dev/logs/T02-contracts.json`，新入口必须独立于旧天气初始化，T03 前仅隔离试用。

2026-09-28：T02 完成，入口 `.dev/bin/netcap --config <console.json> serve`。`make console-integration` 实测两轮流式和重启；浏览器脚本 `tests/console_browser.js` 通过桌面/窄屏、暂停、离线和同步。修复只读轮询阻止发送及离线状态延迟，均有先失败后通过的反馈。自有测试进程已退出。详细状态和复现入口见 T02 证据及 `CONSOLE.md`。T03 在现有 Runtime/Application seam 扩展，不能清空 Unknown 或通过放宽权限绕过审批。

2026-09-29：T03 已新增 `Proxy.Respond/PendingRequest/ConnectionID`，跟踪 `serverRequest/resolved`；`go test -race -count=1 ./internal/codex` 通过。尚未接入产品审批 UI/持久操作或真实工具验证，接续细节见 T03 证据。当前没有需等待的自有后台进程；总 Goal 仍 active，T04–T15 尚未解锁。

2026-09-29：T03 已贯通 Application/Runtime/Web；真实批准、拒绝、异步问题、丢创建/发送回执、部分历史和转人工重启验证通过。新增恢复测试通过 race；补齐子会话队列/Goal 占用检查。正在执行独立生命周期矩阵（`tests/codex_lifecycle_smoke.py`），完整验收和接续缺口见 T03 证据。

2026-09-29：T03 全部本地验收完成，M1 通过。真实生命周期矩阵、后台终端占用、Pending 跨连接、错误/正确关联、浏览器和旧 CLI 回归通过；全量 race/vet 与生产构建通过。全部自有测试进程已清理。开始 T04，不替换总 Goal。

2026-09-29：T04 完成。固定 SDK bridge、两轮/恢复、原生审批/问题/后台停止、Go/bridge/CLI 故障、丢回执/截断日志、A/B 凭据/规则/hook 隔离、闲置回收和浏览器通过；全量 race/vet、Codex 回归及生产构建通过。原二进制保留、自有测试进程清理。继续 T05；M2 和总 Goal 尚未完成。

2026-09-29：T05 完成。OpenCode 每会话独立 HTTP/SSE server，真实会话/交互/恢复/审批失效/子任务和跨重启 PTY 占用、配置/插件/探测隔离、浏览器全部通过；全量 race/vet、生产构建与 Codex/Claude 交互回归通过。记录 native 每进程约 0.5 GiB 的当前资源代价，空闲回收与规模验收继续归 T14。自有后台已清理；继续 T06，M2 Alpha 与总 Goal 尚未完成。

2026-09-29：T06 完成。三类同一生产控制台同时流式与审批、事件/Operation/凭据隔离、只读观察与唯一通知处理方契约、CLI 能力准入、浏览器/恢复矩阵通过。修复迟到通知和观察重启提示；Claude 恢复测试期限与初始化 120 秒同步。全量 race/vet、生产构建通过，自有进程清理，原二进制保持。M2 Alpha 本地验收通过；开始 T07，总 Goal 保持 active。

## 需保留的决定

2026-09-29：T07 第五片完成离线准备：新增独立 `native-probe-contracts` 正例/错误矩阵，以及三类此前真实 UTC 记录的回放与缺失结果负例；Runtime 非 socket 和验收程序 race、Python 4 项检查、全库 vet、Go/bridge 构建通过。实际启动原生验收在模拟供应商 TCP socket 创建处以 EPERM 失败，Unix socket 创建允许但 bind 同样 EPERM。该环境条件已连续多轮存在；现有可独立路径完成后，剩余路径均依赖真实原生/HTTP/browser 验收，T07 记 blocked，按工具规则将同一总 Goal 标 blocked。恢复入口和未验矩阵见 T07 第五片；没有把 probe 或 Codex route_evidence 提升为 supported。

2026-09-29：T07 离线状态链路已补齐：原生来源/重复结果校验、普通请求成功刷新、TTL/晚到结果顺序、结构化错误与有限退避、持久超时与一次中止。Application/console/events/notify race、Runtime 非 socket race、全库 vet 和生产构建通过。当前环境无法创建 socket，三类 probe 保持 unverified；Codex 共享后台凭据选择/轮换尚未验收，route_evidence 也保持 unverified，普通回复不能刷新其指定路由健康。自有 probe fake 验证不能解锁 T08；下一步与细节见 T07 第三片证据。

目前沿用设计中已修订的 M0–M6 与 F1–F5。后续只记录新增的、有依据的决定，并链接对应设计或证据；可发现的配置事实保留在配置中。

## 真实环境验证与切换

尚未执行真实供应商保活、外部通知、真实 hooks 安装或用户状态迁移。实施过程中按实际验证更新；本表不预授予这些操作的权限。
