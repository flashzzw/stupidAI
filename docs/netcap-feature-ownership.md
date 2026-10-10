# netcap 功能归属与首片范围

日期：2026-09-30。依据当前工作区源码、[需求](requirements-analysis.md)与[实施计划](implementation-plan.md)。本清单补全需求 §2 的内部盘点；它确定本轮实验与首片默认范围，不代表已经选定底座、完成迁移或通过产品验收。

## 1. 逐项归属

“保留”表示继续保留原实现和既有要求，不自动纳入新平台首片；“平台”描述责任，不要求立即搬目录或更换语言。下列源码链接均为本轮实际读取的入口。

| 现有功能 | 源码与实际边界 | 新归属 | 首片及后续处理 |
|---|---|---|---|
| 原生会话创建、历史、发送、审批、问题、停止与恢复 | [Runtime](../internal/runtime/adapters.go)、[Codex](../internal/runtime/codex.go)、[Claude](../internal/runtime/claude.go)、[bridge](../bridges/claude/src/session.ts)；实际支持按连接方式区分 | 平台公共执行能力；原生循环仍归 CLI | W01 复用接缝，补来源身份和跨端交接；插件不另建 CLI 或审批循环 |
| 持久操作、丢回执核对、暂停、人工控制 | [Application](../internal/app/application.go)、[模型](../internal/app/model.go)、[存储](../internal/app/store.go)；未决操作重启为 uncertain，不能假设没执行 | 平台公共会话与执行能力 | 首片必须通过同一入口提交动作；重复请求、输入权与恢复由平台统一核对 |
| 显式会话托管、空闲后续发策略 | Python [Takeover](../netcap/takeover.py) 包含订阅、人工消息识别、队列取消及发送核对；[Supervisor](../netcap/supervisor.py) 将它与 Goal/天气协调 | 策略、启用范围和解释归 netcap 插件；身份、发送、去重、输入仲裁归平台 | **首片纳入**：一个显式选择的测试会话；默认关闭。不移植工作区批量发现后自动托管；不直接调用旧 Supervisor |
| 供应商探测、UTC 日期验证 | [旧探测](../internal/codex/probe.go)、[Runtime 探测](../internal/runtime/probe.go)、[应用探测记录](../internal/app/probes.go)、[Python Probe](../netcap/probe.py) | 探测任务策略及业务展示归 netcap 插件；隔离执行、取消、凭据路由和持久记录归平台 | 首片读取已有真实状态；主动探测作为独立后续切片。T07 完整原生/HTTP/browser 验收未完成，不提升 capability |
| 路由健康、TTL、错误分类、退避 | [健康计算](../internal/app/route_health.go)、[路由绑定/doctor](../internal/app/routes.go)、[Codex 路由](../internal/runtime/codex_route.go) | 平台公共路由与准入能力；插件贡献可溯源探测结果 | W02 复用绑定与指纹。普通会话活动不等同指定路由健康；首片不新增自动切供应商 |
| 保活与天气轮转 | [Go Guard.Step](../internal/guard/guard.go)、[天气操作](../internal/guard/operations.go)、[Python Supervisor](../netcap/supervisor.py) 将探测、Goal、天气串行协调 | 保活策略是后续 netcap 插件能力；不能上收为平台默认自动发消息行为 | **暂不迁移到首片**，保留旧入口；T08 仍待验。未来与托管、Goal 调度共享准入，避免多处发送“继续” |
| 天气查询、城市表和演示负载 | [weather.py](../weather.py)、[旧配置](../internal/config/config.go)；旧 Guard.Initialize 强制要求城市表 | 可选业务工具/演示资产 | **暂不迁移**。新平台与首插件不依赖天气初始化；不将外部天气查询作为会话接续验收 |
| 原生 Goal 与文档顺序队列 | [Go Goals](../internal/guard/goals.go)、[Python Goals](../netcap/goals.py)、[旧状态](../internal/state/state.go)；直接绑定 Codex 原生 Goal，不是跨 CLI Lead 编排 | 平台拥有新的 Goal/Task/Attempt/Gate；旧原生 Goal 保留为历史能力 | W05/W06 复用持久意图和核对经验，不直接搬旧队列当 Lead；首插件不维护第二套目标完成状态 |
| 通知、观察来源及去重契约 | [events](../internal/events/events.go)、[只读观察接入](../internal/app/observations.go)、[notify](../internal/notify/config.go)；notify 当前是配置、归属与候选筛选，尚无发送 transport | 平台统一通知；插件贡献带来源的业务事件 | 首片呈现站内动作与停止原因；外发与系统通知归后续交付，旧通知兼容契约保留，不声称已经实现发送 |
| OpenCode 后端 | [HTTP/SSE adapter](../internal/runtime/opencode.go)、[进程服务](../internal/runtime/opencode_server.go)、[PTY](../internal/runtime/opencode_pty.go) | 保留的可选 Backend 资产 | **不迁移进首个业务插件**，也不成为双 CLI 首版强制前置；保留既有实现及测试，后续沿公共 Adapter 接口接入 |
| CLI 实例、配置、凭据与能力声明 | [配置](../internal/app/config.go)、[实例](../internal/app/instances.go)、[凭据](../internal/runtime/credential.go)、[能力](../internal/runtime/description.go) | 平台执行配置管理 + CLI 适配 | W01/W02 增补状态命名空间、执行器归属、快照和来源核对；插件只引用配置身份，不复制凭据 |
| 旧 Web 控制台与 HTTP/SSE | [serve 入口](../cmd/netcap/main.go)、[console](../internal/console/server.go)、[React 主界面](../web/src/main.tsx)、[RoutePanel](../web/src/RoutePanel.tsx) | 可复用的平台界面/控制服务资产 | W03 使用独立模拟原型；后续按底座复用组件与操作语义。首插件贡献业务面板，不复制整个控制台 |
| 旧守护 CLI、状态查看与管理接口 | [control](../internal/control/server.go)、[codex_guard.py](../codex_guard.py)、[runtime_status.py](../runtime_status.py) | 保留的兼容入口；迁移由平台交付负责 | W07 前保留；首片不用旧 run 入口启动新平台，避免同时启用旧托管与新调度 |
| 持久化和进程锁 | [旧 Store](../internal/state/store.go)、[Python State](../netcap/state.py)、[console Store](../internal/app/store.go)；console 明确拒绝旧 state.sqlite 目录 | 平台拥有会话/操作/事件；插件仅拥有策略配置与业务记录 | 不合并三个现有状态库，不复制会话账本。W04 验证插件禁用/重启数据归属；W07 才做可回退迁移演练 |

## 2. 首个插件的可验证边界

暂用名“会话托管”，沿用实施计划 §7 的默认，不新增产品选择。

1. **只读片**：显示选定平台会话的 Backend、执行配置、运行/输入控制状态、最近动作；供应商健康与会话活动分别展示。真实数据来自平台，不从旧数据库拼一个假会话。
2. **动作片**：用户显式启用一个测试会话后，插件根据托管策略向平台提出动作。平台核对暂停、输入归属、未决请求、容量、路由绑定和动作唯一性后发送；插件展示接受、拒绝或待核对的原因。
3. **生命周期片**：人工接手立即停止插件后续自动请求；禁用不删除会话/产物；重启先核对在途动作，不能再发一次。保留业务设置与动作来源，可重新启用。

首片不包括主动探测、自动保活、天气、OpenCode 迁移、旧 Goal 队列、工作区批量托管和真实外发。原有要求保持，后续独立验收。

## 3. W04 前必须验证的接缝

| 接缝 | 插件需要的平台行为 | 验证条件 |
|---|---|---|
| 会话引用 | 按环境、原生状态命名空间及原生 ID 定位，附当前执行器/连接方式 | 别名目录识别为共享；不同命名空间的同 ID 不串用 |
| 动作请求 | 持久意图、去重、输入权及准入统一处理 | 插件和 Lead 不重复发送；回执丢失转待核对，禁止盲重试 |
| 状态与事件 | 只读订阅、来源标记、业务通知交给统一通知方 | 断连不冒充空闲；暂停、失联、等待审批都不能触发空闲托管 |
| 生命周期与数据 | 停插件只撤销其自动策略，保留会话、成果和未决操作 | 启用→接手→禁用→重启后身份连续、无第二执行者 |
| Windows/WSL | Windows 发起并控制目标 WSL 环境中的执行器 | W04 做最小控制链路与路径核对；W07 做安装、升级、休眠完整验收 |

接口形式仍待 AionUi/Core、AO 纵切比较。上述表格是行为边界，不预设 RPC、数据库或插件 SDK；仅阅读源码不能证明候选底座支持它们。

## 4. 本轮盘点结果

源码阅读确认三条边界：旧 Guard/Supervisor 混合了业务策略与发送机制，不能整包作为插件启动；旧原生 Goal 不等于新平台 Goal；通知契约不等于已有发送器。需求 §2 的功能清单已补齐，首片范围沿用已存在默认。底座适配和生产迁移仍待 W04；本轮不移动生产模块、不运行旧守护进程。
