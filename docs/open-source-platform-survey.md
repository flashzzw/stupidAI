# Windows / WSL 多 CLI Agent 工作台：开源项目深度筛选

研究日期：2026-09-30（第一轮），2026-09-30 同日完成第二轮重点核对（第 11 节）。对应需求：[requirements-analysis.md](requirements-analysis.md)。研究范围为公开文档、仓库元数据、发布记录和关键源码；没有安装候选产品、启动付费模型任务或完成 Windows/WSL 实机验收。

**后续需求补充：** 用户新增多供应商执行配置并发隔离、每次执行选择 Skill/MCP/CLI 插件、共享和专属能力管理及 Harness 运行约束。下列排序尚未验证这些新增条件；产品评审以 [V0.2 设计](product-design.md) 和 [补充设计依据](harness-and-execution-config.md) 为准，不能把原候选的“支持插件”直接视为同时满足两类插件和执行级选配。

**第二轮补充（第 11 节）：** 用户指定重新核对 `deepseek-ai/deepseek-harness`（同一 commit，检查第一轮未覆盖的包家族），并新增三个仓库：`NanmiCoder/dsh-agent-teams`（DSH 多 Agent 团队插件）、`pingdotgg/t3code`（多 harness 控制面板）、`makecindy/cindy`（多 harness 混合执行客户端）。本轮结论已合并进第 1、2、3 节。

## 1. 结论与推荐顺序

**有值得改造的项目，但本次没有找到证据足以确认完整满足全部需求的现成底座。** 最值得对照验证的是 **AionUi + AionCore** 与 **Untrivial-ai Agent Orchestrator**；**AWS CAO** 适合验证 WSL 原生 CLI 角色协作。DeepSeek Harness 继续作为插件架构参考，但当前 Codex/Claude 子代理不能直接承担用户要求的完整会话工作流。

第二轮补充三个高相关结论（依据见第 11 节）：**dsh-agent-teams** 证明“以第三方插件实现完整 Captain/成员协作协议”可行，其任务状态机、attempt 语义和质量门与本产品 V0.3 的 Task/Attempt/Gate 模型高度同构，是协作语义的最直接参照；**t3code** 提供多 harness 控制面板的成熟工程范式（事件溯源、hidden git refs 检查点、Claude 多账号 `CLAUDE_CONFIG_DIR` 方案），适合作为工作台执行层架构参照；**cindy** 的 capability-routing 给出“稳定能力 ID 与 harness 实现 ID 分离”的可复用数据模型。三者都不改变底座候选排序：dsh-agent-teams 运行在 DSH 原生 sub-agent 上而非原生 Codex/Claude CLI 会话，t3code 与 cindy 都没有四角色协作与验收 Gate 模型，也均未提供接管外部已运行 CLI 的证据。

| 顺序／用途 | 候选 | 最接近的需求 | 决定性的缺口 |
|---|---|---|---|
| 首轮产品底座候选 A | AionUi + AionCore | Windows 桌面、Leader/Teammate、Codex/Claude、可安装扩展与设置入口 | WSL 运行目标未证实；共享目录；冷恢复不保存轮次中间状态；四角色与独立 Gate 待补 |
| 首轮产品底座候选 B | Untrivial-ai Agent Orchestrator，以下简称 AO | 项目协调者、原生 CLI、worktree、独立 Reviewer、持久控制器交接 | 新版已移除第三方 npm 插件；Windows 原生 ConPTY 不等于 WSL；外部已有进程接管未证实 |
| 执行层对照 | AWS CLI Agent Orchestrator，以下简称 CAO | Profile 与 provider 分离、真实 CLI、tmux、Supervisor 委派与恢复日志 | 桌面和功能插件体验需补；worktree 非默认；已有独立 CLI 接管未证实 |
| 备选桌面与会话参考 | Emdash | Windows 桌面、CLI/ACP、worktree、持久工作区服务 | WSL 专用入口、完整 Lead 协作和外部功能插件契约未证实 |
| 插件机制参考 | DeepSeek Harness，以下简称 DSH | 插件生命周期、依赖与服务组合、Web UI | Codex/Claude 当前是一轮临时委派，不能原样满足所有角色可选 CLI 和原会话接续 |
| 协作语义参照（第二轮） | dsh-agent-teams，以下简称 AT | Captain/成员、任务 DAG、attempt、质量门、冷恢复 | 成员是 DSH 原生 sub-agent，不证明原生 Codex/Claude 会话可当成员；无 WSL/桌面入口 |
| 执行层架构参照（第二轮） | t3code | 多 harness 会话控制、多账号隔离、恢复与检查点 | 无 Lead 协作与 Gate；不接管外部已运行 CLI；WSL 无专门支持 |
| 能力路由参照（第二轮） | cindy | 跨 harness 能力路由、混合 harness 任务、共享记忆 | 后端服务闭源；托管服务依赖；直接复用成本高 |

这是**进入验证的优先级**，不是已经选定底座，更不是要求把上述系统同时部署。AionUi 更适合先验证“插件工作台”，AO 更适合对照“可靠的编码协作”。是否能保留用户的 WSL 和已有会话工作方式，是两条路线共同的淘汰条件。[A1–A7][O1–O8][C1–C5][E1–E4][D1–D4]

## 2. 怎样理解“高分开源项目”

本次没有把 Star 当成质量分，也没有编造行业评分。分开判断四项：

1. **社区与持续维护**：Star、最近提交、发布状态、是否宣布停止维护。仓库未 archived 不代表仍在维护。
2. **需求匹配**：Windows/WSL、原生 CLI、角色选择、接续、插件和恢复分别检查，硬缺口不能被其他优点抵消。
3. **工程证据**：追到实际 adapter、控制器交接、状态恢复和插件 loader。测试文件存在只说明有对应验证入口，不代表本次测试通过。
4. **复用条件**：许可证、拆仓依赖、当前分支与发行版差异，以及扩展是否需要重新编译整个平台。

通过 GitHub 仓库搜索补充候选，再核查下列 11 个产品／编排项目；AionCore 是追踪 AionUi 后端时追加的依赖仓库。搜索并非所有开源项目的穷尽调查。[META]

### 社区和维护快照

Star 为本次 GitHub API 取数，短时间内也会变化；提交时间使用 UTC 日期。完整 commit 在文末列出。

| 项目 | Star | 最近推送 | 许可 | 维护／适用性判断 |
|---|---:|---|---|---|
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | 240,458 | 09-29 | MIT | 2026-08-13 建仓，developer preview；最近三次发布均为 RC，热度不等于稳定性；第二轮核对时 Star 已高于首测值 |
| [AionUi](https://github.com/iOfficeAI/AionUi) | 33,229 | 09-09 | Apache-2.0 | 有正式发布；当前开发还依赖独立 AionCore，不能只 fork UI 判断能力 |
| [Vibe Kanban](https://github.com/BloopAI/vibe-kanban) | 28,225 | 09-19 | Apache-2.0 | README 明确写出 **sunsetting**，降为代码与交互参考 |
| [t3code](https://github.com/pingdotgg/t3code) | 23,940 | 09-30 | MIT | 2026-02-08 建仓；桌面/Web/移动三端有正式分发；README 明确暂不接受大功能贡献，fork 前需接受该路线 |
| [Happy](https://github.com/slopus/happy) | 23,950 | 09-28 | MIT | 接续交互参考；产品重点是移动/Web 控制，不是四角色交付工作台 |
| [Superset](https://github.com/superset-sh/superset) | 14,748 | 09-30 | Elastic License 2.0 | 当前 README 明确 **Windows not yet available**；须与 MIT/Apache 候选分开看 |
| [Agent Orchestrator](https://github.com/Untrivial-ai/agent-orchestrator) | 12,539 | 09-30 | Apache-2.0 | 原 ComposioHQ 地址重定向到此；已有 Go 重写，旧 npm 插件介绍不再适用 |
| [ZCode](https://github.com/zai-org/ZCode) | 7,178 | 09-29 | Apache-2.0 | WSL 源码有价值；2026-09-20 建仓，公开维护历史很短 |
| [Emdash](https://github.com/generalaction/emdash) | 5,882 | 09-29 | Apache-2.0 | 有 Windows 发布入口，近期有正式版；可作为桌面备选 |
| [cindy](https://github.com/makecindy/cindy) | 2,840 | 09-30 | Apache-2.0 | 客户端开源，后端服务闭源在另一仓库；有托管服务与登录态依赖，复用需按“Skip Sign-In”边界评估 |
| [AWS CAO](https://github.com/awslabs/cli-agent-orchestrator) | 1,360 | 09-29 | Apache-2.0 | 热度较低但原生 CLI 协作契合；main 有较多新增行为，需锁版本 |
| [dsh-agent-teams](https://github.com/NanmiCoder/dsh-agent-teams) | 1,855 | 09-29 | MIT | 单维护者插件，与 DSH 同期建仓（08-12）；docs/ 内大量按宿主 RC 版本的兼容审计，升级跟随成本高 |
| [Augani Agent Orchestrator](https://github.com/Augani/agent-orchestrator) | 7 | 09-14 | MIT | 契约、工作区和 Gate 思路贴近，但社区证据弱，且当前协调者绑定 Codex |
| [multiagents](https://github.com/zetbrush/multiagents) | 8 | 04-26 | API 未识别许可证 | 小型 MCP 编排参考；不能仅凭公开源码默认获得可复制的许可 |

首行为第二轮 GitHub API 快照（09-30），其余行为第一轮快照；短期内均会变化。上表许可基于仓库 license 元数据，并读取可取得的根许可文件；Superset 实际根许可为 ELv2，不能写成 MIT。它允许一定条件下使用和修改，但含托管服务、license key、声明保留等限制，不归入本报告的宽松开源底座首选。[META][S1][S2][V1]

发布记录还显示：AionUi 最近正式版为 `v2.2.2`，Emdash 为 `v1.2.7`，CAO 最近发布为 `v2.5.1`；AO 正式版为 `v0.13.1`，最近三条 release 是后续 nightly；DSH 最近三条是 RC。**下面检查的是固定 commit 的源码，不保证这些能力都已进入用户下载的稳定安装包。** AionUi 与 AionCore 的两个 main 快照也没有做过配套运行验证。[REL]

## 3. 能力矩阵：不把相似名词当成满足需求

“部分”表示存在相关实现，但不能完整满足验收；“未证实”表示没有找到充分证据，不能推导为绝对做不到。

| 候选 | Windows + WSL | 四角色各选 Codex/Claude | 新建／历史接续 | 控制外部正在运行的原 CLI | 功能插件 | 长程与恢复 |
|---|---|---|---|---|---|---|
| AionUi/Core | Windows 有；WSL 未证实 | Lead/member backend 可选；Explore 约束和独立 Gate 需补 | 有新建与 resume 路径；外部原生历史导入待测 | 未证实 | manifest、安装、生命周期、设置贡献有实现 | 有团队状态；冷启动丢失轮中状态，需补核对 |
| AO | Windows 有；默认原生 ConPTY，WSL 未证实 | Orchestrator/Worker/Reviewer 可选；Explore 需细化 | 平台内有原生恢复及 TUI↔Chat 交接 | 现有证据限平台内会话 | **当前没有第三方 npm 插件加载** | detached host、控制器代次与持久交接较强 |
| CAO | Linux/tmux 可用于 WSL 验证；不是完整 Windows 桌面 | role、provider 分离，最接近配置需求 | 有 `resume_session_id` 路径，双 CLI 实测未做 | tmux attach 证据限其管理的会话 | Python 事件插件；Agent Plugin 界面默认 gated | journal、idempotency、恢复策略；工作流审批语义需适配 |
| Emdash | Windows 有；host 类型为 local/SSH，WSL 未证实 | 可选 CLI；四角色组织未证实 | 两类 CLI resume 参数和持久 host 路径 | 未证实 | 内置 agent/integration plugin；外部功能插件契约未证实 | 有存活重接及原生 resume，业务任务恢复待测 |
| DSH | Web 可放 WSL；完整 Windows 宿主待补 | 原生 CLI 子代理 one-shot，不能直接当可续行 Lead/teammate | 子代理新建有；该路径无 resume | 该路径不支持 | 插件体系最明确 | DSH 自身 Teams 状态有；不能转称原 CLI 状态恢复 |
| ZCode | 有明确 WSL resolver/backend | 查到的是 ZCode 自身 subagent runtime，未证实原生双 CLI 角色 | 不据自身 Session 推断双 CLI 历史接续 | 未证实 | 有扩展能力，netcap 业务插件待验证 | 自身后台任务不等于所需外部 CLI 任务恢复 |
| Happy | Web/CLI；当前独立桌面入口为 macOS | 不提供所需四角色闭环 | 通过 wrapper 启动、切换时重启到远程模式 | **不等同于无准备接管任意原 CLI** | 不作为功能插件底座推荐 | 适合参考控制权交接，不能替代任务交付状态 |
| AT（第二轮） | 无自有入口；宿主为 DSH Web/桌面 | Captain/成员均为 DSH 原生 sub-agent；不证明原生 CLI 会话可任成员 | 成员可续行（宿主 sub-agent 语义）；与原生 CLI 六入口无关 | 不适用（不接触原生 CLI 进程） | **本身是一个完整第三方插件的实证** | 任务终态只读、attempt 撤销与冷恢复重试有实现 |
| t3code（第二轮） | Windows 正式分发；CLI 在本机直跑，WSL 无专门支持 | 无四角色与 Gate 模型；Claude=官方 SDK、Codex=app-server，均平台自启 | 平台自建会话有完整生命周期与检查点 | 未提供接管外部已运行 CLI 的证据 | 无外部插件体系（暂不收贡献） | 事件溯源、幂等命令、hidden refs 检查点最成熟 |
| cindy（第二轮） | 桌面有 Windows 分发；无 WSL 专门入口 | 无四角色；但单任务可跨 harness 规划/执行/审查 | harness 中途切换保持工作区与记忆连续（产品声明，实测未做） | 未证实 | 插件体系在建设中（市场 in the making） | 后端闭源，Skip Sign-In 模式边界待评估 |

六个入口场景仍需逐项验收：Codex、Claude 各自的新建、实时接入、暂停后恢复。任何表中的“有恢复路径”，都不能直接把六项勾完。[A1–A7][O1–O8][C1–C5][E1–E4][D1–D4][Z1–Z3][H1–H2]

## 4. AionUi + AionCore：最值得先验插件纵切

### 已有资产

AionUi README 明确描述 Leader 分解任务、Teammate 执行、Team MCP、异步 mailbox 和共享 task board，后端可包含 Codex/Claude。开发文档说明 UI 为 Electron，实际后端已拆到 Rust 的 AionCore。[A1][A2]

```text
AionUi/
├── packages/desktop/src/renderer/pages/team/       团队界面与成员交互
└── examples/hello-world-extension/                扩展 manifest、设置、Skill、Agent 示例
AionCore/
├── crates/aionui-team/src/                        provisioning、mailbox、task_board、scheduler
├── crates/aionui-team-prompts/src/role_prompt.rs  Leader/Teammate 行为提示
├── crates/aionui-session/src/backend/             Codex/Claude、暂停与恢复
└── crates/aionui-extension/src/                   loader、registry、lifecycle、hub
```

Extension 示例不是只有 Skill：manifest 包含 `engine`、`lifecycle`、`permissions`，可贡献 ACP adapter、MCP server、assistant、agent、skill、theme、settings tab；Core 有对应 manifest 解析和生命周期执行。它给 netcap 做状态/配置入口提供了真实起点，但还没有证明扩展拥有操作核心会话所需的稳定公共 API。[A3][A4]

### 需要改造的边界

- **角色不是四类成品**：已有 Leader/Teammate 支持职责提示；需要把 Explore 的只读行为、Implementer 的范围、Reviewer 的独立判断落实到权限和 Gate。`role_prompt.rs` 还包含默认先提人员方案再确认的行为；这是候选产品的默认交互，不是本用户新授权规则，应按用户“授权内自主执行”的偏好配置或调整。[A5]
- **共享目录**：README 明确 all agents share the same folder，provisioning 有 team workspace 继承路径。不能把“team-isolated workspace”解释为每个写任务都有独立 worktree。首轮验证必须包含两个可能改同一文件的实现任务。[A1][A6]
- **恢复缺口可直接定位**：`rehydrate.rs` 的实现返回 `SessionState::Idle`；注释明确只持久化原生 session anchor 与 turn generation，pending permission、in-flight tool 等轮中状态丢失，由用户重新驱动。这与 R06 的自动核对及防重复要求有差距；它描述的是已检查的 session 冷启动路径，不代表已经检查所有后台子系统。[A7]
- **原生 CLI 接入不等于外部进程接管**：Core 的 `codex_conn.rs` 创建 App Server 连接，`claude_conn.rs` 为会话启动进程并支持原生 session ID；还需证明如何连接用户已经独立启动的那个进程，而非读取 ID 后启动另一个执行者。[A8]
- **Windows/WSL 要补证据**：Windows 开发文档走 `aioncore.exe`；本次没找到足以确认“Windows 桌面原生选择 WSL 发行版、用户、Linux CLI”的完整路径。用 Web 服务部署在 WSL 可作实验候选，不能直接宣布原生桌面入口达标。[A2]

**建议的最小实验**：一个扩展在工作台显示 netcap 的只读状态和配置入口；同一扩展通过平台会话能力对一个显式选中的测试会话注册托管。在 Core 重启后核对会话，不能由插件和平台同时补发“继续”。前一部分成功，只证明 UI/扩展可行；后一部分才验证真正的功能插件边界。

## 5. Agent Orchestrator：协作和恢复最值得借鉴，插件需要重做

### 已有资产

README 描述 project orchestrator 保留项目规划上下文、创建/调整 workers，并结合 PR、CI 和 review 事实推进。源码 `ProjectConfig` 分别保存 `Worker`、`Orchestrator` 和 `Reviewers`，Reviewer 支持 Codex/Claude 等独立选择；因此不只是同一聊天窗口中的几个角色标签。[O1][O2]

```text
backend/internal/
├── domain/projectconfig.go              角色与 harness 的独立配置
├── adapters/agent/                      CLI 启动与原生会话信息
├── session_manager/interface_transition.go  TUI/Chat 持久交接
├── service/chat/                        原生协议、detached host
├── review/                              独立 reviewer 与目标 SHA
└── storage/sqlite/                      状态、迁移和交接记录
frontend/                               Electron 工作台
```

关键价值在 `interface_transition.go` 与架构文档：TUI/Chat 共用一个 AO session 和原生 conversation 身份，替换控制器时保留 workspace，使用持久阶段和 controller generation，不能同时让两个控制器活动。Review `LaunchSpec` 记录 `TargetSHA`，是把验证绑到具体产物的参考。[O3][O4]

### 不能直接复用的假设

1. **旧插件优势已经失效**：当前官方 `plugins/authoring.mdx` 明确不再加载 `manifest/create()` 的第三方 Node 包，没有 `ao plugin create` 或 marketplace；`registry.go` 是编译进 Go 的构造器列表。旧版“多个可替换插件槽位”的文章不能用于评价当前版本。[O5][O6]
2. **Windows 原生支持不证明 WSL**：当前平台文档明确 Windows 使用 ConPTY，macOS/Linux 用 tmux。WSL 发行版、用户、路径及常驻服务接入需要单独实现或找到现成路径并实测。[O7]
3. **平台内交接不等于外部接管**：它证明的是 AO 已管理会话的 TUI↔Chat；尚未证明能无准备吸纳任意正在运行的 WSL Codex/Claude 进程。架构还允许部分 Terminal 恢复在历史不可用时显式 fresh-start；本需求的“恢复原会话”不能把 fresh-start 当成功。[O3]
4. **仍需补 Explore 和业务插件**：现有 reviewer 不代表所有审查/测试 Gate 都满足，现有 worker 不代表只读探索已受约束。netcap 若变成功能插件，需要新建稳定的扩展入口，不能只加一个 Go adapter 就称插件化。[O2][O5]

**建议用途**：作为 Aion 路线的可靠性对照；若插件接口补齐成本可接受，可成为更偏代码交付的底座。不要为了沿用旧 npm 插件机制就默认退回不再演进的旧架构。

## 6. AWS CAO：最直接的原生 CLI 角色实验基础

CAO 运行完整 CLI 进程，Profile 用 Markdown + YAML 表达 `role`、`provider`、`skills`、`allowedTools` 等。启动参数可以覆盖 provider；子任务 profile 能选择不同于父任务的 provider。这支持用同一套职责分别尝试 Codex 和 Claude。[C1][C2]

最值得借的模块是 `services/session_service.py` 的 `resume_session_id`、`idempotency_key`，`services/worktree_service.py` 的隔离，以及 workflow journal 对未知结果的暂停核对。其恢复文档区分结果回放、重新执行和需要人工判定，适合参考长程执行的失败模型。[C3][C4]

需要特别保留以下差异：

- `use_worktree` 默认 `False`；终端隔离不等于文件隔离。所读 teardown 使用 `git worktree remove --force`，可能清理未提交产物；不能原样作为本产品“停止任务”的默认语义。[C3]
- Tool Restrictions 文档明确：允许完整 shell 的“只读”提示不能保证不写文件。Codex provider 文档还说明默认 `--yolo` 路径及 `codexProfile` 的例外。Explore 需要按实际工具能力验证，不能仅根据 profile 名称判合格。[C2][C5]
- Python `cao.plugins` 是服务事件插件，安装依赖服务器 Python 环境并需重启。另一套 Agent Plugins 管理面默认 gated，Web tab 还有构建开关；两者都不能自动等同于所需的可安装工作台业务页面。[C6]
- 工作流作者先写 Python/YAML，journal 恢复并不自动证明 Lead 能根据目标自主规划。当前 workflow 的计划/运行审批语义也需与产品授权偏好对齐，不能照搬成每一步都问用户。[C4]

**建议用途**：在 WSL 以一个小任务验证四类角色与两种 CLI、消息传递、打回及隔离；保留结果，再判断哪些运行模块值得移植。它可以承担实验底座，不必成为最终 Windows 产品壳。

## 7. 其余项目的定位

### Emdash

有 Windows 安装入口，Codex/Claude agent plugin 实际定义原生 resume 命令。workspace-server 文档说明关闭 output view 只断开视图，存活进程可重接，丢失进程可按 provider resume handle 恢复；进程重启不承诺完整终端 scrollback。[E1–E3]

但是本次查到的 host target 是 `local-socket | ssh`，插件主要是仓内 agent/integration 契约。没有充分证据确认外部业务扩展安装、四类角色自动协作和专门 WSL 入口。可作桌面第二梯队及远端 host 参考，不能依据“支持很多 Agent”推断已满足 Lead 驱动协作。[E4]

### DeepSeek Harness 与 ZCode

DSH 的 `subagent-codex`／`subagent-claude-code` 文档和源码明确：包内原生 payload、无宿主 CLI fallback、fresh one-shot、无该路径的 continuation/resume，provider 声明 `NO_START_CAPABILITIES`。Teams 是另一套可续行机制，不能因为产品同时有两种功能就认为两种原生 CLI 都可直接当 Team Lead/member。第二轮在同一 commit 补充核对了 subagent 家族其余后端、sandbox、guard、goal、skill、workflow 包，原生 CLI 后端仍为 one-run，但家族层面的 continuable 模式与三级文件策略值得借用；见第 11.4 节。[D1–D3]

DSH 插件生命周期和服务组合仍然最值得研究。若选它做壳，需要开发完整原生会话 adapter、外部控制入口、工作区和验证契约；代价明显高于“写个转发插件”。ZCode 已有 WSL resolver、代理和远程 backend，可借鉴其环境入口，但其自身 agent runtime 不能替代原生 Codex/Claude 的身份和会话保证。[D4][Z1–Z3]

### Vibe Kanban、Superset、Happy、小型编排器

- **Vibe Kanban**：保留 task/workspace/diff review 和 Rust executor 作为参考；README 已宣布 sunsetting，不再作为有持续上游维护的首选。[V1]
- **Superset**：并行终端、持久 session、worktree 很有参考价值，但 Windows 尚未提供，ELv2 与宽松开源要求也有区别。[S1][S2]
- **Happy**：README 明确用 `happy`／`happy codex` wrapper 启动，远程切换会重启 session 到 remote mode。它提供有价值的人机交接体验，但不能据此宣称无重启接管任意原进程；也不覆盖四角色 Gate。[H1][H2]
- **Augani Agent Orchestrator**：持久计划、one-writer-per-workspace、task contract、review gate 值得看；但 README 明确当前 Codex task 就是 coordinator，Codex worker 使用 ephemeral exec，review gate 也围绕 Codex。它尚不满足 Lead/Reviewer 也可自由选择 Claude 的要求。7 Star 的短历史项目适合作为设计素材，不作为“高分成熟平台”推荐。[G1]
- **multiagents**：作为简洁 MCP broker 参考保留；社区与许可证据不足，不进入首轮底座验证。[META]

## 8. 最难的共同问题：已有 CLI 的实时接入

必须分别记录三种身份：工作台 Session、CLI 原生 conversation/thread、实际进程或共享服务实例。**原生 ID 相同不自动证明只存在一个执行者。**

| 入口 | 能证明什么 | 不能自动证明什么 |
|---|---|---|
| 读取历史文件／`thread/read` | 能展示历史 | 已订阅实时事件、可以操作原执行器 |
| `resume` | 新控制路径继续原生历史 | 原进程已交出控制、没有重复执行 |
| attach 到已托管 tmux／PTY | 继续观察和操作该托管终端 | 能吸纳任意外部 shell 已启动的 CLI |
| 同一 App Server 的多客户端 | 共享服务内的同一运行会话可被控制 | 所有独立 CLI 默认都连接这个服务 |
| 官方 Remote Control | 官方支持的客户端可访问原会话 | 任意自建工作台获得公共控制协议或自动具备同等能力 |

OpenAI 官方文档已记录 App Server 的 stdio、WebSocket 和 Unix socket，以及 CLI `--remote`、`thread/resume`。`thread/read` 明确不订阅事件。它提供 Codex 的可行接缝，但需要逐版本验证用户原 CLI 是否已经处于可连接的服务中；相关传输仍有 experimental 标记。[N1][N2]

Claude 官方 Remote Control 支持在已有交互会话运行 `/remote-control`，带上当前历史，连接 Claude 自己的 Web/App；目前要求符合条件的 claude.ai 订阅身份，API key 不支持。本次资料没有证明这是可供本工作台直接复用的公共接管 API。因此不能据此宣布 Claude 接续问题已解决，也不把“做不到”写成永久结论。[N3]

技术验证应给每个入口标明：CLI 版本、配置根、原生 ID、服务/进程身份、是否需要预先 wrapper/tmux/共享服务、是否重启、是否保留在途工具。需要启动前准备的方案仍可评估，但**不能静默缩减“接续独立已有任务”的验收**。若严格的任意现存 CLI 实时操作无法做到，这将成为明确的产品决策点。

## 9. 应补进需求文档的内容

研究后的需求修订是收紧可观察行为，不提前锁定实现：

| 修订 | 依据 | 验收重点 |
|---|---|---|
| 把 CLI 能力与版本显式展示 | one-shot、平台内 resume、实时 attach 经常混用 | 不支持项说明原因；不静默新建或换 backend |
| 拆开观察与输入控制权 | AO 单控制器交接、Happy wrapper | 切换时不产生第二执行者；断连后先核对 |
| 保留“待核对”恢复状态 | Aion 冷恢复 Idle 与 CAO 未知结果差异 | 不能仅因进程退出便宣布空闲／重新派发 |
| Gate 绑定产物版本 | AO review 目标 SHA | 修改后旧验证失效；测试未跑不能标通过 |
| 验证角色实际权限 | CAO shell 与 Codex 模式差异 | Explore 无业务写入；Reviewer 不承担主实现 |
| 验证插件生命周期及调度归属 | Aion extension、AO 内置 adapter、CAO 事件插件差异 | 一个真实 netcap 功能贯通 UI/执行/状态；无双重调度 |
| 把停止与清理产物分开 | CAO worktree teardown | 停止／崩溃／插件禁用后未提交成果仍可检查 |

## 10. 最小选型实验与淘汰条件

下一阶段按风险先后验证，不先拼装完整 UI：

1. **入口实验**：两类 CLI 的六个场景；既有任务分别使用“无预先托管”和“事先托管”样本。观察消息、输入、审批、在途工具和唯一执行者。严格场景失败即记录硬缺口，不以历史 resume 替代。
2. **插件实验**：Aion 路线先做一个 netcap 状态入口及显式会话托管；AO 路线估算同样能力需要增加哪些公共接口。禁止两边维护互相矛盾的 Session/Task 权威状态。
3. **角色实验**：四角色 × 两 CLI 共八种组合；只读探索、明确写入范围、独立 Reviewer；混合 CLI 和单一 CLI 两种团队都覆盖。
4. **真实闭环**：选一个跨模块开发目标，由 Lead 实际分工；Reviewer 发现一个真实未满足条件，打回修复后再次验证。固定任务脚本不能代替这一项。
5. **恢复实验**：界面关闭、daemon 断连、请求已执行但确认丢失、轮中崩溃各一次。记录重接／恢复／待核对，不重复派发、不删未提交成果。

比较结果看：人工介入次数、实际通过的入口和 Gate、重复执行/冲突次数、恢复后的缺失内容、可观测用量和耗时。具体并发、费用阈值及样例由用户确认；本次不虚构性能或成本数字。

## 11. 第二轮重点核对：DSH 插件生态与两个新工作台

本节为 2026-09-30 第二轮，按用户指定的四个仓库核对。方法与第一轮一致：固定 commit 读源码与文档，不安装、不运行付费任务。四个仓库的 commit 见第 13 节快照表。

| 仓库 | 形态 | 与本产品的关系 |
|---|---|---|
| deepseek-ai/deepseek-harness（`639ed01`，同第一轮） | Cordis 插件化 harness | 补看第一轮未覆盖的包家族（11.4） |
| NanmiCoder/dsh-agent-teams（`9cba4fe`，v0.1.22） | DSH 第三方插件 | Lead 协作协议的已实现实证（11.1） |
| pingdotgg/t3code（`0fcd5f9`） | 多 harness 控制面板 | 执行层架构与恢复语义参照（11.2） |
| makecindy/cindy（`2c7d941`） | 多 harness 混合执行客户端 | 能力路由数据模型参照（11.3） |

### 11.1 dsh-agent-teams：Lead 协作协议的已实现实证

AT 以一个第三方 npm 插件（MIT）实现了 Captain（对应 Lead）、可续行成员、依赖 DAG、调度器、持久 mailbox、Web 工作区面板与质量门，全部不修改宿主。这比 AionUi 的 hello-world 示例更直接地证明 R11 的关键命题：**功能插件可以携带一整套协调系统，而不只是贡献一个设置页。** 宿主锁定 DSH `0.2.0-rc.2`。[AT1]

其内部机制与本产品 V0.3 的 Task／Attempt／Gate 模型高度同构，可直接对照验收语义：

| AT 机制 | 实现 | 对应本产品概念 |
|---|---|---|
| 显式任务状态机 | `TASK_TRANSITIONS`：`pending → claimed → in_progress → completed\|failed\|cancelled`，终态只读，`claimed` 不能直接 `completed`；依赖只认上游 `completed`，`failed`/`cancelled` 永不解锁下游 | 任务结果状态（R05“失败不解锁依赖”） |
| attempt 语义 | `beginTaskAttempt()` 生成 `attemptId`；迟到写入被拒；重新分配先撤销旧 attempt 并等待旧 worker 静默；冷重启后仅对“本进程未观察到 settle 的持久 open attempt”做一次恢复重试 | Attempt 与“重连不算新尝试”（R06、§9.3 恢复核对） |
| 质量门“直到共识” | 机器可判定：必需门禁全过 + acceptance 全过 + 无 blocker/high finding + 最新实现版本经独立 Reviewer 审查 + 声明的验证命令通过 + 修改范围合法；审查失败自动生成 `repair-N`／`review-N+1`；禁止实现者自批、禁止复活终态任务、repair 不依赖 failed review | Gate 与验证证据（R09/R19、P05 打回修复循环） |
| 范围审计 | 完成时记录 `changed_paths` 并用 git/工作区 diff 审计越界修改；README 如实标注“这是完成时审计，不是宿主写入拦截” | 范围与限制（§6.1）；同样承认提示词不等于文件隔离 |
| 磁盘真相 | 团队状态存 `<workspace>/.agent-teams/`，Web 面板读磁盘并与实时活动合并 | 状态可追溯（R25 快照思想） |
| 成员路由快照 | 成员创建时解析 provider/model/effort 并快照，续行恢复该快照；异构团队需显式指定 | 执行配置快照（R25、§11.5 产品节） |

[AT2][AT3][AT4]

必须保留的差异：成员是 **DSH 原生 sub-agent**，不是原生 Codex/Claude CLI 会话——四角色×双 CLI（R14）与六入口（R02b）不由它证明；它默认“先审计划再执行”（staged roster + Approve & Run），与本产品已确认的“交给 Lead 直接推进、授权内自动推进”默认相反，采纳其协议时需把审批默认值对齐本产品决定；单维护者插件跟随宿主 RC 频繁出兼容审计（`docs/` 下按宿主版本成组），复用其代码要接受升级跟随成本。[AT1][AT5]

### 11.2 t3code：多 harness 控制面板的工程范式

t3code 是“控制本机 agents”的服务器 + Web/桌面/移动客户端，支持 Claude、Codex、Cursor、Grok、OpenCode、Antigravity。Claude 走官方 `@anthropic-ai/claude-agent-sdk` 的 query 会话，Codex 有独立 app-server 协议包；provider 差异全部收敛在 adapter 边界后，编排层只见规范化命令与事件。[TC1][TC2] 它没有四角色协作、验收 Gate 或外部插件体系，价值在执行层工程范式：

1. **事件溯源与幂等命令**。event log 是编排状态唯一真相；decider 只产生事件、不做任何 provider/文件系统 I/O；事件、持久化投影、命令回执在一个数据库事务内提交；副作用由 Reactors 在意图落账后执行并经命令回馈。因此**命令确认表示意图已提交，不表示 provider 工作完成**——这正是 R06“发送后回执丢失先核对、不重复发送”的成熟实现基础。turn 结束与后续工作落定也是两个里程碑，迟到的 checkpoint/diff 不会延长已记录的 turn。[TC1]
2. **hidden git refs 检查点**。用隐藏 Git 引用捕获工作区状态，不给用户分支增加 commit；revert 必须协调工作区与 provider 会话，provider 不能回滚其会话时先拒绝操作再动文件系统。这为“目标启动时逐个选择 Git 策略（默认禁止自动 commit）”提供了中间档实现：平台自存快照而不污染用户历史。[TC3]
3. **Claude 多账号方案**。每账号一个独立 `CLAUDE_CONFIG_DIR`，**保持 `HOME` 与系统 keychain 位置不变**，并明确警告改 `HOME` 会把凭据放到 provider 找不到的位置；已有会话只能在相同 config 目录的实例间切换，独立目录含各自会话状态完全隔离；仅 API key/endpoint 不同的预设用实例环境变量而非启动参数。这印证本产品 V0.3 实验 E01 的方向并给出两个补充约束（不动 HOME；会话按 config 目录绑定），可直接并入 W02 验收。[TC4]
4. **能力协商纪律**。客户端与服务端独立升级，功能开关经 environment descriptor 能力字段协商，旧字段保留派生数据、降级后覆盖缓存；不假设协同发布。对应 R16“能力分别显示、不支持时说明原因”。[TC1]
5. **探活无副作用**。健康检查刻意避免触发认证、MCP 启动或登录浏览器；登录回调成功不等于认证完成。对应本产品“检查不自动安装、升级、登录”。[TC2]
6. **权限模式分级降级**。Supervised／Auto-accept edits／Auto／Full access 四级，`Auto` 依赖 provider 自带的自动审查，无等价能力的 provider 回退为询问，而不是伪装支持。[TC5]

边界：threads 由 t3code 自己启动和管理，没有接管外部已运行 CLI 进程的证据；Windows 正式分发但 CLI 在本机直跑，无 WSL 专门入口；README 明确暂不接受大功能贡献，作为底座 fork 需接受其路线。事件溯源等范式可移植，代码（effect 体系）直接复用成本高。[TC1][TC6]

### 11.3 cindy：跨 harness 能力路由与连续性

cindy 客户端 Apache-2.0，但**后端服务在另一个闭源仓库**，且有托管服务与登录态依赖；本地“Skip Sign-In”模式能力受限。作为底座不可行，其 `capability-routing` 数据模型对本产品 R22/R23 很有价值：

- **模型层能力 ID 与 harness 实现 ID 分离**：同一稳定 capability id 可由未来 harness 映射到自己的插件/工具名，产品策略不随 harness 变化——即本产品“共享能力定义与 CLI 适配版本分离”（§11.3 产品节）的一个具体实现路径。
- **来源六分类 + 表面五分类 + 调用三级**：`cindy-host / cindy-plugin / user-skill / project-skill / harness-builtin / harness-plugin` × `skill / plugin / mcp / app / tool` × `auto / explicit-only / disabled`；带命名空间 id（`feishu-delegate:message-feishu-coworkers`）与容器 id（`feishu-delegate@personal`）区分同名资产。这组维度可直接参考进能力库与本次执行面板的数据模型。
- README 声明多 harness 混合任务（同一任务由不同 harness×model 规划、并行执行、审查）与切换时 workspace/memory/skills/tools 连续；本轮只核对到类型与模块划分（`maker-core` 的 `agents/claude-code|codex|pi`、`auto-review-decision`、`auto-compact-controller`；`maker-cc-manager` 的 session-registry/remote-claude-env），未实测该连续性。[CY1][CY2][CY3]

### 11.4 DSH 同一 commit 的增量：第一轮未覆盖的包家族

第一轮只核对了 subagent 两个原生 CLI 后端与 plugin-manager。本轮补看家族后修正一处表述、新增五项可借鉴资产：

- **subagent 家族**（修正）：核心 seam 已支持 one-shot 与 continuable 两类子代并存，in-process `spawn`（全新）与 `fork`（以父会话已完成轮次为一次性种子快照）可续行；out-of-process 有 ACP、dsh-sdk、claude-code、codex 四种后端。`subagent-claude-code` 与 `subagent-codex` **仍是 fresh one-run、无续行/resume/池化**（第一轮结论对这两个后端继续成立），但两个后端的细节值得借用：claude-code 后端在叠加显式 env 前先移除凭据形状的环境变量（防宿主环境凭据泄入子进程）、省略 SDK `settingSources` 以尊重原生 user/project/local 设置、平台固定版本 payload 且从不回退宿主 `claude` 可执行文件、只接受 `success` 且非空的结果；codex 后端每 run 新起 app-server + ephemeral thread、原生 `HOME`/`CODEX_HOME` 保持权威、失败映射到固定错误类别集（`limit/access-policy/service/transport/product-error/invalid-result/unknown/process` + HTTP 状态码）。固定错误类别直接可参考进 R02 的错误定位验收。[D5][D6][D7]
- **sandbox 家族**：`read-only / workspace-write / danger-full-access` 三级文件效果策略，Linux/macOS/Windows（ACL 写限制）平台后端，被策略拒绝的调用可经用户批准一次性升级。这是 Explore 只读约束（R05）少见的带 Windows 后端的现成实现；注意其自述为 same-world confinement（同内核同文件系统），不是容器/VM 隔离。[D8]
- **guard 家族**：`repeat-tool-reminder`（识别完全相同的重复工具调用并提醒换路）与 `timeout-policy`（声明时限的工具调用超时返回明确错误）两个小插件。对应 R10“无进展不无限重试”的最小实现形态。[D9]
- **goal 家族**：每会话仅一个持久完成目标，`/goal` 人工命令零模型轮次；goal **记录完成状态而不调度工作**，自动续行由独立 continuation 包启用。这个“状态与调度分离”与本产品 Goal（平台对象）与调度（Lead）分离一致。[D10]
- **skill 家族**：`skill-filesystem`（项目/自定义/用户目录发现）+ `tool-skill`（排序持久会话目录、`skill` 工具按需加载全文、`/name` 直接调用）；不同来源产出统一模型可见格式。渐进发现与本产品 §16“Skill 按需加载”同路。[D11]
- **workflow 家族**：模型编写编排脚本 fan out 子代理（`ralph` 为固定序列 fresh agents），脚本运行在调用会话的文件策略下，hooks 与子生命周期归 workflow 引擎所有——即编排脚本也不绕过会话策略。[D12]

另：AT 的安装文档提到 DSH 桌面应用侧栏（Plugins → Add plugin），说明 `0.2.0-rc` 已有桌面分发；DSH 仓库内只见 `web` 包，桌面分发与仓库对应关系未核实，不据此更新第 3 节 DSH 的 Windows 判断。[AT1]

### 11.5 可复用资产清单

按“直接复用（含许可证）／语义移植／架构参照”分层的结论；除标注外均为 MIT/Apache-2.0 允许的源码级复用，但跨语言（本项目 Go）时主要是语义移植。

| 资产 | 来源 | 对应需求 | 建议复用方式 |
|---|---|---|---|
| 任务状态机 + attempt 撤销/静默 + 冷恢复重试 | AT `src/state.ts`、`src/scheduler.ts` | R05/R06、P04/P06 | 语义移植进平台协作层；对照 P06 恢复验收逐条核对 |
| “直到共识”机器判定 + repair/review 轮次合同 | AT `docs/quality-gates.md` | R09/R19、P05 | 合同字段（kind/round/verdict/findings/changedPaths/verify）直接借鉴进 Gate 证据结构 |
| 事件溯源：decider 纯化 + 命令回执事务 + Reactors | t3code `apps/server/src/orchestration` | R06、P06 | 架构参照，Go 侧按现有 `internal/runtime` 接缝评估 |
| hidden git refs 检查点 + revert 会话协调 | t3code `CheckpointStore.ts` | R18 + 新确认的 Git 逐目标策略 | 实现参照：平台快照不污染用户分支 |
| Claude 多账号 `CLAUDE_CONFIG_DIR`（保持 HOME/keychain） | t3code `providers-claude.md` | R21、P11 | 方案并入 W02 验收；E01 已部分验证同方向 |
| 稳定能力 ID 与 harness ID 分离 + 六来源/五表面/三级调用 | cindy `capability-routing.ts` | R22/R23、P12/P13 | 数据模型参照进能力库 schema |
| 三级文件策略 + Windows ACL 后端 + 一次性升级 | DSH `sandbox` 家族 | R05 Explore 只读、R18 | TypeScript 组件，评估 WSL 侧等效实现（landlock/namespace 或 go-sandbox） |
| repeat-tool 提醒 + 工具超时 guard | DSH `guard` 家族 | R10、R26 | 语义移植，小而独立 |
| ephemeral 委派固定错误类别 + 凭据环境清洗 | DSH `subagent-codex/claude-code` | R02 | 错误分类集与 env 处理规则并入 adapter 契约 |
| 探活无副作用 + 能力协商降级 | t3code `providers.md`/`overview.md` | R16、P01 | 纪律写入 W01/W02 验收用例 |

以上均为“进入验证的参照”，不改变第 1 节底座排序；是否逐项吸收由对应 W 阶段的验收决定。

## 12. 研究边界与仍待确认的事项

- **已完成**：第一轮 11 个候选及 AionCore 依赖的公开资料筛选；固定 commit 的关键源码核查；社区/维护/许可证区分；需求修订与选型实验说明。第二轮按用户指定核对 DSH（同 commit 增量包）、dsh-agent-teams、t3code、cindy 四个仓库的文档与关键源码，产出第 11 节。
- **未执行**：安装或构建候选、候选测试套件、Windows/WSL 实测、付费模型任务、六入口／八角色验收。发现测试源码不表示测试运行成功；第二轮未实测 cindy 的 harness 切换连续性与 t3code 的恢复行为，只核对到文档与源码声明。
- **仍待用户决定**：Backend 选择（已于产品侧确认为双模式，见需求分析）；真实验收任务；并发和费用上限；netcap 首个插件功能（已确认“状态展示 + 单会话显式托管”）。严格已有 CLI 接管是否需要调整，仅在协议实验给出证据后再讨论，不在本轮代替用户降级需求。
- **工程工作未开始**：没有将当前 netcap 自动改成平台核心，没有新建功能插件，没有改运行配置或既有实施计划；第 11.5 节清单为参照建议，未开始移植。

## 13. 源码快照与引用

下列固定 commit 链接是两轮核查对象（前十二行为第一轮，后四行为第二轮）。引用组如 `[A1–A7]` 表示该段综合使用 A1 到 A7，不是测试通过标记。官网文档及 GitHub 元数据为研究当日快照，会继续变化。

| 仓库 | 本次源码 commit |
|---|---|
| deepseek-ai/deepseek-harness | `639ed015397290b3745d163aafe02ffee4aa3f84`（两轮同 commit） |
| zai-org/ZCode | `29628c9acdb81b703bbd4080c207a0e7ce5e276e` |
| awslabs/cli-agent-orchestrator | `db54caaafb23bf06b56eab3cecf7066c168f5a4d` |
| Untrivial-ai/agent-orchestrator | `c97a54c3959546a85952f978c49629eee26ef3c2` |
| iOfficeAI/AionUi | `6744099b279b991c17e31c243f0920477bd31cb6` |
| iOfficeAI/AionCore | `ea24f50af26977f38ebb3775816bbb1b7815a018` |
| generalaction/emdash | `873a3e2067f4abc136272ed2b61abea3a2c07bcf` |
| BloopAI/vibe-kanban | `d5cbb5380fa0b32e98ef9b8d987f63decce4be3a` |
| superset-sh/superset | `811c99df5fb28195ee9c51f1c98ee1ad44a4b5d4` |
| slopus/happy | `4cf54d18488cba4787cc251cc37010f31125af29` |
| Augani/agent-orchestrator | `8d9b897c49500e8e1b54ec8028352544db95618d` |
| zetbrush/multiagents | `03fcf6e875804a21650ed4e45eb9b6624460f7fb` |
| NanmiCoder/dsh-agent-teams | `9cba4fe4171f27c019991cafd2a107f87ef3517b`（v0.1.22） |
| pingdotgg/t3code | `0fcd5f90611451cca842689faea53b5450c022da` |
| makecindy/cindy | `2c7d941a2d2cb46b7c7a375c1ccff89f7d4c61b1` |

- **[META]** GitHub REST API：分别读取上表仓库的 `https://api.github.com/repos/{owner}/{repo}`，保存研究日的 Star、建仓、推送、许可与 archived 字段；搜索使用 `search/repositories` 的 `codex claude orchestrator stars:>1000` 等查询。数值见第 2 节；未用 Star 推导通过率。
- **[REL]** 对 DSH、ZCode、CAO、AO、AionUi、Emdash、Vibe Kanban、Happy 读取各仓库 `/releases?per_page=3`，另读取 AO、AionUi、Emdash 的 `/releases/latest` 核对正式版；正式下载与 main 功能是否一致仍待安装验证。

- **[A1]** [iOfficeAI/AionUi: readme.md](https://github.com/iOfficeAI/AionUi/blob/6744099b279b991c17e31c243f0920477bd31cb6/readme.md)。
- **[A2]** [iOfficeAI/AionUi: docs/contributing/development.md](https://github.com/iOfficeAI/AionUi/blob/6744099b279b991c17e31c243f0920477bd31cb6/docs/contributing/development.md)。
- **[A3]** [iOfficeAI/AionUi: examples/hello-world-extension/aion-extension.json](https://github.com/iOfficeAI/AionUi/blob/6744099b279b991c17e31c243f0920477bd31cb6/examples/hello-world-extension/aion-extension.json)；[iOfficeAI/AionUi: examples/hello-world-extension/contributes/settings-tabs.json](https://github.com/iOfficeAI/AionUi/blob/6744099b279b991c17e31c243f0920477bd31cb6/examples/hello-world-extension/contributes/settings-tabs.json)。
- **[A4]** [iOfficeAI/AionCore: crates/aionui-extension/src/manifest.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-extension/src/manifest.rs)；[iOfficeAI/AionCore: crates/aionui-extension/src/loader.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-extension/src/loader.rs)；[iOfficeAI/AionCore: crates/aionui-extension/src/lifecycle.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-extension/src/lifecycle.rs)。
- **[A5]** [iOfficeAI/AionCore: crates/aionui-team-prompts/src/role_prompt.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-team-prompts/src/role_prompt.rs)。
- **[A6]** [iOfficeAI/AionCore: crates/aionui-team/src/provisioning.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-team/src/provisioning.rs)；[iOfficeAI/AionCore: crates/aionui-team/src/service/spawn_support.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-team/src/service/spawn_support.rs)。
- **[A7]** [iOfficeAI/AionCore: crates/aionui-session/src/backend/rehydrate.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-session/src/backend/rehydrate.rs)；[iOfficeAI/AionCore: crates/aionui-team/src/scheduler/crash_recovery.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-team/src/scheduler/crash_recovery.rs)。
- **[A8]** [iOfficeAI/AionCore: crates/aionui-session/src/backend/codex_conn.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-session/src/backend/codex_conn.rs)；[iOfficeAI/AionCore: crates/aionui-session/src/backend/claude_conn.rs](https://github.com/iOfficeAI/AionCore/blob/ea24f50af26977f38ebb3775816bbb1b7815a018/crates/aionui-session/src/backend/claude_conn.rs)。
- **[O1]** [Untrivial-ai/agent-orchestrator: README.md](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/README.md)。
- **[O2]** [Untrivial-ai/agent-orchestrator: backend/internal/domain/projectconfig.go](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/backend/internal/domain/projectconfig.go)。
- **[O3]** [Untrivial-ai/agent-orchestrator: docs/architecture.md](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/docs/architecture.md)；[Untrivial-ai/agent-orchestrator: backend/internal/session_manager/interface_transition.go](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/backend/internal/session_manager/interface_transition.go)。
- **[O4]** [Untrivial-ai/agent-orchestrator: backend/internal/review/launcher.go](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/backend/internal/review/launcher.go)；[Untrivial-ai/agent-orchestrator: backend/internal/review/planner.go](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/backend/internal/review/planner.go)。
- **[O5]** [Untrivial-ai/agent-orchestrator: frontend/src/docs/content/plugins/index.mdx](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/frontend/src/docs/content/plugins/index.mdx)；[Untrivial-ai/agent-orchestrator: frontend/src/docs/content/plugins/authoring.mdx](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/frontend/src/docs/content/plugins/authoring.mdx)。
- **[O6]** [Untrivial-ai/agent-orchestrator: backend/internal/adapters/agent/registry/registry.go](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/backend/internal/adapters/agent/registry/registry.go)。
- **[O7]** [Untrivial-ai/agent-orchestrator: frontend/src/docs/content/platforms.mdx](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/frontend/src/docs/content/platforms.mdx)。
- **[O8]** [Untrivial-ai/agent-orchestrator: backend/internal/session_manager/codex_operation_gate.go](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/backend/internal/session_manager/codex_operation_gate.go)；[Untrivial-ai/agent-orchestrator: backend/internal/adapters/agent/codex/codex.go](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/backend/internal/adapters/agent/codex/codex.go)；[Untrivial-ai/agent-orchestrator: backend/internal/adapters/agent/claudecode/claudecode.go](https://github.com/Untrivial-ai/agent-orchestrator/blob/c97a54c3959546a85952f978c49629eee26ef3c2/backend/internal/adapters/agent/claudecode/claudecode.go)。
- **[C1]** [awslabs/cli-agent-orchestrator: README.md](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/README.md)；[awslabs/cli-agent-orchestrator: docs/agent-profile.md](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/docs/agent-profile.md)。
- **[C2]** [awslabs/cli-agent-orchestrator: docs/tool-restrictions.md](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/docs/tool-restrictions.md)。
- **[C3]** [awslabs/cli-agent-orchestrator: src/cli_agent_orchestrator/services/session_service.py](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/src/cli_agent_orchestrator/services/session_service.py)；[awslabs/cli-agent-orchestrator: src/cli_agent_orchestrator/services/worktree_service.py](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/src/cli_agent_orchestrator/services/worktree_service.py)。
- **[C4]** [awslabs/cli-agent-orchestrator: docs/workflows.md](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/docs/workflows.md)。
- **[C5]** [awslabs/cli-agent-orchestrator: docs/codex-cli.md](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/docs/codex-cli.md)；[awslabs/cli-agent-orchestrator: docs/claude-code.md](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/docs/claude-code.md)。
- **[C6]** [awslabs/cli-agent-orchestrator: docs/plugins.md](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/docs/plugins.md)；[awslabs/cli-agent-orchestrator: docs/agent-plugins.md](https://github.com/awslabs/cli-agent-orchestrator/blob/db54caaafb23bf06b56eab3cecf7066c168f5a4d/docs/agent-plugins.md)。
- **[E1]** [generalaction/emdash: README.md](https://github.com/generalaction/emdash/blob/873a3e2067f4abc136272ed2b61abea3a2c07bcf/README.md)。
- **[E2]** [generalaction/emdash: packages/plugins/src/agents/impl/codex/index.ts](https://github.com/generalaction/emdash/blob/873a3e2067f4abc136272ed2b61abea3a2c07bcf/packages/plugins/src/agents/impl/codex/index.ts)；[generalaction/emdash: packages/plugins/src/agents/impl/claude/index.ts](https://github.com/generalaction/emdash/blob/873a3e2067f4abc136272ed2b61abea3a2c07bcf/packages/plugins/src/agents/impl/claude/index.ts)。
- **[E3]** [generalaction/emdash: agents/architecture/workspace-server.md](https://github.com/generalaction/emdash/blob/873a3e2067f4abc136272ed2b61abea3a2c07bcf/agents/architecture/workspace-server.md)。
- **[E4]** [generalaction/emdash: apps/emdash-desktop/src/core/services/hosts/api/targets.ts](https://github.com/generalaction/emdash/blob/873a3e2067f4abc136272ed2b61abea3a2c07bcf/apps/emdash-desktop/src/core/services/hosts/api/targets.ts)；[generalaction/emdash: agents/integrations/integration-plugins.md](https://github.com/generalaction/emdash/blob/873a3e2067f4abc136272ed2b61abea3a2c07bcf/agents/integrations/integration-plugins.md)；[generalaction/emdash: apps/emdash-desktop/src/core/features/agents/api/node/plugin-registry.ts](https://github.com/generalaction/emdash/blob/873a3e2067f4abc136272ed2b61abea3a2c07bcf/apps/emdash-desktop/src/core/features/agents/api/node/plugin-registry.ts)。
- **[D1]** [deepseek-ai/deepseek-harness: README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/README.md)；[deepseek-ai/deepseek-harness: packages/subagent/subagent-codex/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/subagent/subagent-codex/README.md)。
- **[D2]** [deepseek-ai/deepseek-harness: packages/subagent/subagent-claude-code/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/subagent/subagent-claude-code/README.md)；[deepseek-ai/deepseek-harness: packages/subagent/subagent-codex/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/subagent/subagent-codex/src/index.ts)；[deepseek-ai/deepseek-harness: packages/subagent/subagent-claude-code/src/index.ts](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/subagent/subagent-claude-code/src/index.ts)。
- **[D3]** [deepseek-ai/deepseek-harness: packages/experimental/agent-team/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/experimental/agent-team/README.md)。
- **[D4]** [deepseek-ai/deepseek-harness: packages/boot/plugin-manager/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/boot/plugin-manager/README.md)。
- **[Z1]** [zai-org/ZCode: README.en.md](https://github.com/zai-org/ZCode/blob/29628c9acdb81b703bbd4080c207a0e7ce5e276e/README.en.md)。
- **[Z2]** [zai-org/ZCode: packages/server/src/remote/wsl-backend.ts](https://github.com/zai-org/ZCode/blob/29628c9acdb81b703bbd4080c207a0e7ce5e276e/packages/server/src/remote/wsl-backend.ts)；[zai-org/ZCode: packages/server/src/remote/wslProxy.ts](https://github.com/zai-org/ZCode/blob/29628c9acdb81b703bbd4080c207a0e7ce5e276e/packages/server/src/remote/wslProxy.ts)。
- **[Z3]** [zai-org/ZCode: packages/desktop/src/main/desktopWslTargetResolver.ts](https://github.com/zai-org/ZCode/blob/29628c9acdb81b703bbd4080c207a0e7ce5e276e/packages/desktop/src/main/desktopWslTargetResolver.ts)；[zai-org/ZCode: apps/zcode-cli/packages/core/src/subagent/runner.ts](https://github.com/zai-org/ZCode/blob/29628c9acdb81b703bbd4080c207a0e7ce5e276e/apps/zcode-cli/packages/core/src/subagent/runner.ts)。
- **[V1]** [BloopAI/vibe-kanban: README.md](https://github.com/BloopAI/vibe-kanban/blob/d5cbb5380fa0b32e98ef9b8d987f63decce4be3a/README.md)。
- **[S1]** [superset-sh/superset: README.md](https://github.com/superset-sh/superset/blob/811c99df5fb28195ee9c51f1c98ee1ad44a4b5d4/README.md)。
- **[S2]** [superset-sh/superset: LICENSE.md](https://github.com/superset-sh/superset/blob/811c99df5fb28195ee9c51f1c98ee1ad44a4b5d4/LICENSE.md)。
- **[H1]** [slopus/happy: README.md](https://github.com/slopus/happy/blob/4cf54d18488cba4787cc251cc37010f31125af29/README.md)。
- **[H2]** [slopus/happy: packages/happy-cli/src/claude/claudeLocal.ts](https://github.com/slopus/happy/blob/4cf54d18488cba4787cc251cc37010f31125af29/packages/happy-cli/src/claude/claudeLocal.ts)；[slopus/happy: packages/happy-cli/src/claude/claudeRemote.ts](https://github.com/slopus/happy/blob/4cf54d18488cba4787cc251cc37010f31125af29/packages/happy-cli/src/claude/claudeRemote.ts)。
- **[G1]** [Augani/agent-orchestrator: README.md](https://github.com/Augani/agent-orchestrator/blob/8d9b897c49500e8e1b54ec8028352544db95618d/README.md)。
- **[N1]** [OpenAI 官方 App Server 文档](https://developers.openai.com/codex/app-server/)：transport、`thread/read`、`thread/resume`、远程 TUI。
- **[N2]** [OpenAI 官方 CLI Reference](https://developers.openai.com/codex/cli/reference/)：`--remote`、`resume`、App Server 实验状态。
- **[N3]** [Claude 官方 Remote Control](https://code.claude.com/docs/en/remote-control)：已有会话启用、支持客户端、订阅认证和恢复限制。通过 Context7 定位并直接取回官网页面核查。

第二轮引用（commit 见第 13 节快照表）：

- **[AT1]** [NanmiCoder/dsh-agent-teams: README.md](https://github.com/NanmiCoder/dsh-agent-teams/blob/9cba4fe4171f27c019991cafd2a107f87ef3517b/README.md)：团队协议、成员路由快照、宿主兼容矩阵、桌面应用安装入口。
- **[AT2]** [NanmiCoder/dsh-agent-teams: docs/quality-gates.md](https://github.com/NanmiCoder/dsh-agent-teams/blob/9cba4fe4171f27c019991cafd2a107f87ef3517b/docs/quality-gates.md)：“直到共识”判定、合同字段、repair/review 轮次、范围审计与显式禁做清单。
- **[AT3]** [NanmiCoder/dsh-agent-teams: src/state.ts](https://github.com/NanmiCoder/dsh-agent-teams/blob/9cba4fe4171f27c019991cafd2a107f87ef3517b/src/state.ts)：`TASK_TRANSITIONS`、`unsatisfiedDependencies`、attempt 生命周期、团队状态读写。
- **[AT4]** [NanmiCoder/dsh-agent-teams: src/scheduler.ts](https://github.com/NanmiCoder/dsh-agent-teams/blob/9cba4fe4171f27c019991cafd2a107f87ef3517b/src/scheduler.ts)：空闲认领、parked attempts、冷恢复重试边界。
- **[AT5]** [NanmiCoder/dsh-agent-teams: compatibility.json](https://github.com/NanmiCoder/dsh-agent-teams/blob/9cba4fe4171f27c019991cafd2a107f87ef3517b/compatibility.json)：宿主版本支持矩阵与升级跟随成本。
- **[TC1]** [pingdotgg/t3code: docs/internals/overview.md](https://github.com/pingdotgg/t3code/blob/0fcd5f90611451cca842689faea53b5450c022da/docs/internals/overview.md)：所有权边界、事件溯源、turn 落定、能力协商。
- **[TC2]** [pingdotgg/t3code: docs/internals/providers.md](https://github.com/pingdotgg/t3code/blob/0fcd5f90611451cca842689faea53b5450c022da/docs/internals/providers.md)：adapter 边界、driver/instance、探活无副作用、安装器所有权。
- **[TC3]** [pingdotgg/t3code: apps/server/src/checkpointing/CheckpointStore.ts](https://github.com/pingdotgg/t3code/blob/0fcd5f90611451cca842689faea53b5450c022da/apps/server/src/checkpointing/CheckpointStore.ts)：hidden git refs 检查点与 revert 协调。
- **[TC4]** [pingdotgg/t3code: docs/user/providers-claude.md](https://github.com/pingdotgg/t3code/blob/0fcd5f90611451cca842689faea53b5450c022da/docs/user/providers-claude.md)：`CLAUDE_CONFIG_DIR` 多账号、HOME 警告、会话按 config 目录切换限制、用量限额显示。
- **[TC5]** [pingdotgg/t3code: docs/user/permission-modes.md](https://github.com/pingdotgg/t3code/blob/0fcd5f90611451cca842689faea53b5450c022da/docs/user/permission-modes.md)：四级权限模式与 provider 降级。
- **[TC6]** [pingdotgg/t3code: README.md](https://github.com/pingdotgg/t3code/blob/0fcd5f90611451cca842689faea53b5450c022da/README.md)：支持的 provider、分发渠道、贡献政策。
- **[CY1]** [makecindy/cindy: README.md](https://github.com/makecindy/cindy/blob/2c7d941a2d2cb46b7c7a375c1ccff89f7d4c61b1/README.md)：多 harness 混合声明、订阅复用、后端闭源与 Skip Sign-In 边界。
- **[CY2]** [makecindy/cindy: packages/maker-core/src/types/capability-routing.ts](https://github.com/makecindy/cindy/blob/2c7d941a2d2cb46b7c7a375c1ccff89f7d4c61b1/packages/maker-core/src/types/capability-routing.ts)：能力路由类型定义全文。
- **[CY3]** [makecindy/cindy: packages/maker-core/src/agents](https://github.com/makecindy/cindy/tree/2c7d941a2d2cb46b7c7a375c1ccff89f7d4c61b1/packages/maker-core/src/agents)：claude-code/codex/pi 三 harness 适配与 auto-review/auto-compact 模块划分。
- **[D5]** [deepseek-ai/deepseek-harness: packages/subagent/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/subagent/README.md)：one-shot 与 continuable 子代、五种后端。
- **[D6]** [deepseek-ai/deepseek-harness: packages/subagent/subagent-claude-code/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/subagent/subagent-claude-code/README.md)：env 凭据清洗、settingSources 省略、固定 payload、结果接受条件。
- **[D7]** [deepseek-ai/deepseek-harness: packages/subagent/subagent-codex/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/subagent/subagent-codex/README.md)：ephemeral thread、原生配置权威、固定错误类别。
- **[D8]** [deepseek-ai/deepseek-harness: packages/sandbox/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/sandbox/README.md)：三级文件策略、平台后端、一次性升级、same-world 边界。
- **[D9]** [deepseek-ai/deepseek-harness: packages/guard/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/guard/README.md)：repeat-tool-reminder 与 timeout-policy。
- **[D10]** [deepseek-ai/deepseek-harness: packages/goal/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/goal/README.md)：每会话单目标、状态与调度分离。
- **[D11]** [deepseek-ai/deepseek-harness: packages/skill/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/skill/README.md)：发现来源、会话目录、按需加载、`/name` 调用。
- **[D12]** [deepseek-ai/deepseek-harness: packages/workflow/README.md](https://github.com/deepseek-ai/deepseek-harness/blob/639ed015397290b3745d163aafe02ffee4aa3f84/packages/workflow/README.md)：脚本 fan-out、ralph、文件策略归属、生命周期归属。
