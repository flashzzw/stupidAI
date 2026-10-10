# W01/W02 接续与配置边界补验

日期：2026-09-30。承接 [V0.3 实验](execution-capability-experiments.md)，只补关键入口与身份风险，不增加开源候选。当前状态：**W01/W02 部分实现与验证；已接入控制核对、状态目录绑定、最小启动快照、外部能力引用及 Codex stdio MCP 原生选择，完整六入口未通过。** 第 1–6 节保留首轮边界实验，第 7–10 节记录后续实现和补验。

源码入口：[workbench_boundary_probe.py](../tests/workbench_boundary_probe.py)。最终记录：[机器可读结果](../goals/evidence/W01-W02-boundaries.json)。先前两客户端协议实验扩展为真实 PTY 中的原生 TUI；CLI 仍固定 Codex 0.157.1、共享后台 0.158.0、Claude 2.1.280。模型均为本地模拟响应，凭据均为合成值，配置、项目和状态位于新建临时目录；没有读取用户 CLI 配置或运行真实供应商任务。

## 1. 本轮结果

| 实验 | 实际验证 | 结果与限制 |
|---|---|---|
| B01 `codex-tui` | 真实 TUI 连接自有共享后台；第二客户端观察同一在途轮次；TUI 完成后退出，客户端发第二轮，再由 TUI 恢复原线程发第三轮 | 指定路径通过。三个主请求、同一 thread、完整三轮历史；不是强制输入仲裁或任意前台接管 |
| B02 `codex-standalone` | 真实 `--no-daemon` 独立 TUI 启动本地请求，记录原身份并扫描自有目录的 socket | 观察脚本通过：未发现共享控制端点；**实时接管仍未验证**，不据此断言不存在其他接入机制 |
| B03 `claude-foreground` | 普通独立交互 TUI 完成一轮；输入草稿后尝试左键切换；清除草稿，执行 `/background`，attach 后发送第二轮 | 草稿未发送且阻止切换；转换后历史进入下一轮，但实际模型请求 `metadata.user_id.session_id` 改变。**严格同原生 ID 条件未通过** |
| B04 `claude-foreground-explicit-id` | 与 B03 相同，但最初显式给定 `--session-id` | 再次出现身份改变；不是只比较短作业 ID，而是对比转换前后实际模型请求中的完整会话 ID |
| B05 `claude-supervisors` | 两个独立状态目录的 supervisor 同时有主请求在途；分别 attach、脱离、重新 attach；停止 A，再恢复 A | A/B 身份、历史与凭据不串用；脱离不换 worker；停止 A 不影响 B 的 worker；A 重启换 worker、保留原会话和历史。工具/审批在途与崩溃重启仍未验 |
| B06 `same-id` | 两个状态目录、同一项目路径、同一个指定 Claude 原生 ID 同时新建并恢复；A 改从符号链接目录恢复 | 两份历史及供应商凭据分别正确；别名恢复到 A 原历史。说明身份必须带状态命名空间，目录别名不构成独立配置 |

B01 还观察到标题等辅助模型请求与短生命周期线程。主请求按原 thread 的 `client_metadata.thread_id` 区分，不用总请求数推断重复执行。未提交草稿可见于 TUI，但不在原生历史中，且没有成为主请求；平台不能依赖历史推断终端草稿为空。

B03/B04 中 `agents --json` 可以列出 `kind=interactive` 的前台记录；它没有后台作业的短 ID，不能直接等同于可 attach 会话。后台记录的 `status` 与 `state` 也不同：本次可出现 `status=idle`、`state=working/blocked`。不能把摘要字段直接当作真实在途状态，最终判据同时使用请求、进程、原生记录和终端输出。

本轮没有改变“同一原生会话”的需求。B03/B04 证明了一条保留历史但改变身份的路径，后续可评估为**显式上下文交接**；在用户决定调整契约前，不能把它显示为已满足严格实时接续。

## 2. 六入口矩阵

| 首版入口 | 已有或本轮通过的指定样本 | 未满足或仍缺的证据 |
|---|---|---|
| Codex 新建 | B01 真实共享后台 TUI；B02 独立 TUI；旧 Alpha 平台创建 | Windows 入口及最终底座集成 |
| Claude 新建 | B03 普通独立前台；B05 原生后台；旧 SDK 路径 | 最终底座与不同连接方式统一诊断 |
| Codex 实时接入 | B01 真实 TUI 原轮观察、串行切到客户端再回 TUI | `--no-daemon` 无共享端点样本、并发输入仲裁、审批/工具/草稿完整交接 |
| Claude 实时接入 | B05 预先后台化的原 worker attach/脱离/重接 | B03/B04 前台转换改变原生 ID；无准备接管与平台仲裁尚不成立 |
| Codex 暂停/退出后恢复 | B01 完成轮次后退出 TUI、原线程客户端/TUI 接续 | 工具在途退出、未决审批和未知状态的完整恢复 |
| Claude 暂停/退出后恢复 | B05 停自有 supervisor 后恢复原 ID；B06 CLI 退出后的命名空间内恢复 | 原交互前台直接退出/恢复、工具/审批在途、崩溃而非正常停止的恢复 |

## 3. 后续实现遵守的接续契约

这是从已有需求与本轮实验收敛的行为边界，不预设数据库、协议或桌面框架。

| 对象／操作 | 必须保存或检查的内容 | 失败时行为 |
|---|---|---|
| 原生身份 | 执行环境（未来含 WSL 发行版/用户）、Backend、规范化状态命名空间、完整原生 ID | 不同命名空间的同 ID 分开；别名标记共享；不以标签、PID 或短作业 ID 替代原生身份 |
| 执行器连接 | 原始来源（独立前台/预后台/平台创建）、版本、连接方式、控制端点和对应实例 | 连接失败或身份不符只保留观察，不启动替代执行者 |
| 输入权交接 | 先暂停后续自动请求，再核对原轮次、工具、审批、已提交外部输入与终端草稿处理情况 | 任一未确认保持“交接待核对”；attach 成功不自动授权调度 |
| 未决操作 | 原持久意图、原消息/轮次标识、回执及实际历史 | 超时不能等同未发送；先核对，禁止盲重试 |
| 原会话恢复 | 原命名空间与 ID、执行配置及有效快照 | 原生 ID 变更必须单列为交接或新尝试，不能靠复制历史冒充 resume |
| 能力改配 | 选择意图、原生实际清单、适用时机、前后版本 | 工具在途保留旧快照；无法排除或无法核对时显式不通过，不自动放宽 |
| 原生辅助请求 | 已观察到的路由、认证来源与用量 | 不把一次用户发送视作仅一次调用；未知费用显示未知 |

平台仍无法阻止用户在独立外部终端输入，因而需要“发现外部输入后让路”与“已核对的控制权交接”分别实现；本轮串行安排的客户端不是平台互斥实现。

## 4. 剩余工作与顺序

1. **W01**：针对前台转换的身份变化，寻找保持原 ID 的版本支持路径，或明确保留未满足项；补审批、实际工具在途、草稿处理、双端竞争与崩溃核对。不能仅修改 capability 声明。
2. **W02**：把身份与快照契约接入生产配置；继续补 Codex 同 ID/命名空间边界、共享 MCP 实际调用及认证连接隔离、复杂 hooks/子 Agent、配置漂移与权限冲突。本轮六组实验不代表 W02 全部完成。
3. **W03**：使用 [交互原型](workbench-prototype.html) 验证五条流程，见 [原型走查](workbench-prototype-notes.md)；不因协议缺口停止独立交互验证。
4. **W04**：使用 [功能归属清单](netcap-feature-ownership.md) 做真实插件纵切，同任务比较 AionUi/Core 与 AO，并验证 Windows→WSL 最小控制链路。当前未执行宿主链路；不能推迟到 W07 才发现底座不适配。

## 5. 重跑与证据解释

```bash
python3 tests/workbench_boundary_probe.py
python3 tests/workbench_boundary_probe.py --case codex-tui
python3 tests/workbench_boundary_probe.py --case claude-foreground
python3 tests/workbench_boundary_probe.py --case claude-supervisors
python3 tests/workbench_boundary_probe.py --case same-id
```

完整入口执行六个样本，即使中间条件未满足也继续收集其他样本。B03/B04 的同 ID 条件当前失败，因此整组返回非零；这不是“全部通过”的测试套件。结果分别记录 `execution`（实验是否跑完）与 `expectations_met`（被验证条件是否成立）。B02 的检查只验证观察事实，不能解释成实时接管通过。

开发过程中修正了实验脚本的线程唯一假设、辅助请求计数、首次信任提示和 roster 字段判断；这些早期运行不作为能力结论。最终完整运行及脚本/原始记录哈希见机器可读证据。自有终端、后台与本地供应商在每个实验结束时清理；原始 `/tmp` 记录可能被系统清理，仓库保留结论与可重跑入口。

## 6. 本轮查询的官方依据

- [Codex App Server](https://learn.chatgpt.com/docs/app-server)：实际获取页面，确认 `--remote` 的 WebSocket/Unix 连接方式；以固定本机版本 help 和真实 TUI 为最终实验输入。
- [Claude Agent view](https://code.claude.com/docs/en/agent-view)：实际获取官方 `.md` 正文，核对 `/background`、attach、草稿限制和独立 `CLAUDE_CONFIG_DIR`；对文档概括与 `agents --json` 实测差异保留证据，不自行补成通用保证。

本轮没有重新筛选底座、安装候选产品、运行真实模型协作、Windows/WSL 宿主测试或完整生产回归。

收尾检查：整组六样本均 `execution=completed`，四个样本的指定检查成立（含 B02 的观察检查），B03/B04 的严格身份条件失败；返回码 1 与记录一致。63 份原始证据和实验源码哈希核对通过；Python 语法、原型 JavaScript 语法、九份变更文档的 130 个相对引用与尾随空白检查通过。与执行前快照比较，100 个生产源码文件未改变；扫描本轮自有配置的进程未见残留。

## 7. W01 控制修复与 W02 状态绑定

2026-09-30 后续按序推进。实现见 [控制核对](../internal/app/control_handoff.go)、[Application](../internal/app/application.go)、[状态目录身份](../internal/runtime/identity.go)；回归测试见 [control_handoff_test.go](../internal/app/control_handoff_test.go)。机器可读记录见 [W01-control.json](../goals/evidence/W01-control.json)。本片已修改生产代码与构建资产，不应继续沿用上一轮“仅实验”的状态。

### 已实现的行为

- `resume` 从立即切换本地标志改为持久登记、只读观察、验证后提交控制权；在途工具、不完整历史、未决审批与身份不符均保持暂停。新的暂停/转人工优先于迟到检查结果，中断的控制权申请重启后不自动重放。
- 保存已核对外部输入的身份/内容摘要。重复观察旧人工消息不再反复撤回控制；新外部输入仍让路。原生观察、回执和候选关联不能静默替换已绑定 ID，异常保留原视图并等待核对。
- 新建普通 Codex/Claude 会话绑定规范化真实状态目录。目录别名改指另一目录时拒绝旧会话发送、观察与恢复；配置内容相同也不能绕过。实例与 Web 详情标记别名共享关系，终端能力标签明确限定 Codex 预共享后台。

前三个控制问题与目录改指问题均先通过实际 Application 接口复现失败，再修复重跑。没有复制一套调度器或新建数据库；现有 JSON 状态兼容新增字段。旧记录无目录绑定时保留兼容，不宣称历史命名空间已经验证。

### 新增原生证据

| 编号 | 入口与结果 | 适用范围 |
|---|---|---|
| C01 | [native_handoff_probe.py](../tests/native_handoff_probe.py) `--case claude-stopped-resume`：普通交互前台完成一轮后 `/exit`，再 `--resume <完整 ID> --bg`；实际第二轮请求的原生 ID、历史和凭据均保持 | **退出后恢复通过**；不能替代 B03/B04 失败的实时前台转换 |
| C02 | 同脚本 `--case codex-control --netcap .dev/bin/netcap`：生产 Application 观察真实 TUI 人工输入、重新获得控制；真实 TUI 批准一个自有工具，工具在途时恢复被拒绝；Go 控制台崩溃后工具继续、原操作最终成功、工具启动计数为 1 | 预共享后台的控制与在途工具恢复；不是 `--no-daemon` 接管，也不是对外部输入的系统级排他锁 |
| C03 | [mcp_binding_probe.py](../tests/mcp_binding_probe.py)：两份 Claude 原生配置采用同一 stdio MCP 定义，分别实际调用 `whoami`；工具进程目录/凭据正确，精确工具结果回到模型；同 ID 恢复并排除 MCP 后不再暴露或调用 | 超过仅 inventory 的调用证据；仍只覆盖这一个本地 MCP，未完成 Codex 实际调用、OAuth、复杂插件或生产能力库 |

C01 依据实际取回的 [Claude Agent view](https://code.claude.com/docs/en/agent-view) 中“完整 ID + `--resume --bg` 可原地继续，否则明确提示复制”的契约。C02 使用实际取回的 [Codex App Server](https://learn.chatgpt.com/docs/app-server) 与固定版本原生接口。SDK 配置参考也已核对官方 [TypeScript reference](https://code.claude.com/docs/en/agent-sdk/typescript)，本片没有改 SDK bridge 的配置合并方式。

C02 的空线程尚未有持久 rollout，先完成一轮再从 TUI 恢复。首次 TUI 会写界面偏好，触发既有配置指纹漂移；最终 fixture 在绑定前显式写入已观察到的 `tui.screen_reader_detection_done`。没有为了实验通过而删除生产漂移保护，此交互限制留给 W02 配置来源/漂移设计。

### 验证与剩余边界

全量 `go test -race ./...`、`go vet ./...`、Web 类型检查/构建与隔离 Go 构建通过；新增恢复、未知身份、目录改指、别名展示、迟到核对与中断重启检查通过。C01/C02/C03 均实际运行并通过。真实控制台浏览器完成发送、转人工→恢复、同 ID、刷新历史、共享目录提示及窄屏检查，见机器可读证据。所有模型及 MCP 凭据均为临时合成值，没有调用真实供应商或修改用户会话。

W01 仍缺任意独立前台的严格实时接入、Claude 审批/工具在途及不同故障形态的完整矩阵；平台无法感知未提交草稿。W02 仍需完整执行配置快照、实际能力库绑定、Codex MCP 调用、复杂 hooks/子 Agent、权限冲突和配置漂移处理。本片没有完成这些条件，W04–W07 不提前宣告完成。

## 8. 原生故障边界、实时核对与跨 CLI MCP 调用

2026-09-30 继续。证据见 [W01-W02-native-lifecycle.json](../goals/evidence/W01-W02-native-lifecycle.json)。本片先补 Claude 原生后台的审批和工具在途，再修复生产恢复误用缓存，最后验证 Codex 对共享 MCP 的实际调用。全部使用固定版本 CLI、自有临时状态目录及本地模拟供应商。

### Claude 原生后台的两种崩溃结果

[native_claude_lifecycle_probe.py](../tests/native_claude_lifecycle_probe.py) 使用真实 `--bg`、`attach`、Bash 审批和自有 Python 工具。只向已核对临时 `CLAUDE_CONFIG_DIR` 的 PID 发信号；使用 pidfd 避免 PID 复用。审批只允许本次，不修改长期权限。

| 场景 | 实测结果 | 可作出的结论 |
|---|---|---|
| 审批等待时 attach 客户端被 SIGKILL | 原 worker、待审批状态保留；重接后同一请求可批准，批准前工具未启动 | 终端断开不等于审批取消 |
| 工具在途时 attach 客户端、随后 supervisor 被 SIGKILL | 重新 attach 启动新 supervisor，连回原 worker；工具 PID 与会话 ID 保持，执行计数为 1，完整结果回到模型 | 本样本的控制端故障可继续原执行 |
| 工具在途时 worker 被 SIGKILL | supervisor 启动新 worker，保留会话 ID 和对话；原工具仍存活，替换 worker 已报 `status=idle`、`state=working` | **列表 idle 不能证明原执行已清空** |
| 上述 worker 崩溃后释放原工具，再显式续聊 | 工具落盘完成且只启动一次；新提示保持原 ID/历史，但原工具结果未回到后续模型请求 | **原工具输出连续性未验证，不能记为完整崩溃恢复通过** |

实验的 `expectations_met` 只覆盖列出的身份、审批与不自动重放断言；worker 场景另存 `original_tool_output_continuity=unverified`。不能据其进程退出码将所有故障恢复标为通过。当前生产 Claude 仍使用 SDK bridge，未把原生后台的列表或内部状态文件接成自动控制接口。

官方 [Agent view](https://code.claude.com/docs/en/agent-view) 本轮已实际取回，明确 `agents --json --all` 是读取状态的入口，`jobs/<id>/state.json` 不是稳定接口。观察应保留 `state`、`status`、`waitingFor`、worker 身份和执行证据的区别；仅看 `pid` 或 `idle` 不足以授予控制权。任意独立前台实时接入仍沿用第 1–7 节的未通过/未验证结论。

### 生产恢复不再使用展示缓存

新增回归 `TestResumeCannotGrantControlFromDisplayCache` 在修改前失败：底层新读取失败时，旧完整 idle 视图仍让 `resume` 成功。Application 现在调用 Runtime 的 `ObserveFresh`；Claude 跳过展示缓存，Codex/OpenCode 继续使用本来就访问原执行器的观察路径。目录绑定检查仍在统一适配入口执行。

普通轮询仍使用缓存，不取消闲置资源回收。[Claude 适配回归](../internal/runtime/claude_control_test.go) 验证显示缓存可读、控制核对必须重新联系 bridge；[真实空闲恢复检查](../tests/claude_idle_smoke.py) 验证 CLI/bridge 回收后重新申请控制权不发送模型输入，下一轮继续原 ID 和历史。没有把该修复解释成修好了原生后台 worker 丢失工具结果的问题。

### 同一 MCP 定义在两类 CLI 中实际调用

[codex_mcp_binding_probe.py](../tests/codex_mcp_binding_probe.py) 与 [Claude 调用实验](../tests/mcp_binding_probe.py) 复用同一 MCP 定义生成函数。Codex A/B 各自通过原生 MCP 审批后调用 `whoami`，完整工具返回值中的目录、配置标记和合成凭据检查均正确；原生调用日志计数各为 1。原生 daemon 停止后，在原会话 ID 上恢复并排除该 MCP，工具不再出现在 native inventory 或模型工具定义中，调用数不增加，原历史保留。Claude A/B 同定义回归也通过。

这证明一个共享 stdio MCP 定义能被两类 CLI 使用，执行绑定分别成立。Codex 的排除发生在 **daemon 重启后的同 ID 恢复**，不宣称在途热变更、同一存活 daemon 的完整重配、OAuth 或任意 MCP/插件均已验证。依据为本轮实际取回的 [Codex App Server](https://learn.chatgpt.com/docs/app-server) 文档及原生协议结果；没有新建生产能力库或完整执行快照。

### 验证与下一片

新增回归、全量 `go test -race ./...`、`go vet ./...`、`make dev-build` 和 Python 语法检查通过。生产 Codex TUI 的输入交接/工具跨 Go 崩溃回归、Claude 空闲回收后的只读恢复均通过。本片没有修改 UI，未重复浏览器走查。

W01 继续保留严格独立前台接入、原生后台 worker 故障后的工具结果核对及平台接入缺口。W02 接下来实现最小执行配置快照与能力资产引用，再验证显式改配、漂移和权限冲突；不再把本片已经通过的单 MCP 调用列为待做。W04–W07 仍未开始。

## 9. 最小启动快照与外部能力引用

2026-09-30 按序推进 W02。实现见 [execution.go](../internal/app/execution.go)，使用契约见 [控制台说明](../CONSOLE.md#执行快照与外部能力引用)，结果见 [W02-snapshots.json](../goals/evidence/W02-snapshots.json)。本片不增加 CLI 参数或修改原生能力加载方式，先打通可追溯记录、引用关系、来源核对与用户界面。

- 新建普通 Codex/Claude 会话，在原生操作之前事务性保存不可变 `execution_snapshot`；创建和后续操作记录 `execution_snapshot_id`。快照内含启动时的配置选择、主配置来源和所引用资源的版本/内容标识；原生操作证据继续单独保存，不写回冒充启动事实。
- 登记表 `capability_assets` 与本次 `capability_refs` 分开。外部 Skill、MCP 定义或 CLI 插件可记录为引用，声明适用工具与版本；复制所引用版本的元数据，不依赖登记表的最新内容。停用/移除条目不改写旧引用。引用不是加载指令，界面明确显示加载未验证。
- 文件或目录内容、实际路径、文件权限参与来源核对；目录包含脚本等依赖。检查不保存正文或凭据值。变化和缺失可只读查看，并阻止下一次发送、回复或恢复；持久意图之后、实际发送之前再次核对。原生读取、中止及本地暂停仍保留各自原有契约。
- 沿用现有 SQLite 中的会话/操作 JSON 记录增加字段，没有重选数据库。旧记录不补造快照，列表不附带完整快照正文，来源扫描只在创建、相关控制操作或用户主动核对时发生。

### 验证

Application 回归覆盖：快照先于原生写入持久化、重启与登记表移除后记录不变、脚本变化导致漂移、意图提交后变化阻止发送、缺失/重复/停用/不适用引用拒绝、旧记录无伪造证据、最初缺失的 Claude settings 后续出现可检测。查询结果不能被调用者修改来覆盖历史。

[execution_snapshot_smoke.py](../tests/execution_snapshot_smoke.py) 的 Codex、Claude 两组均使用实际生产 Go HTTP 与真实 CLI，分别完成三轮、控制台崩溃重启、登记版本变化、依赖脚本变化、发送/恢复被拒绝和恢复原内容后的续聊。所有操作引用同一快照，原生 ID/历史保持，核对不发送模型请求。测试资产放在原生自动发现范围之外，确认只记录引用不会隐式加载它；不把这种阴性结果误称为实现了能力排除。

浏览器验证新建时引用、快照展示、只读来源核对、漂移提示/拒绝发送、刷新后同一快照，以及 1440px/768px 布局；无 JavaScript 异常。预期的未登录 401 和漂移拒绝 409 与脚本异常区分。全量 race、vet、Web 类型检查/构建、隔离 Go 构建通过；上一片的 Codex 控制交接和 Claude 空闲回收恢复回归通过。

### 剩余边界

`coverage=recorded_sources_only`：只证明主配置文件与已登记来源，不等于完整有效配置快照。资产的声明兼容性、全部原生继承、动态/账号能力、加载/使用证据、角色/Gate 版本，以及原会话显式改配仍未贯通。来源文件未被冻结或备份，原生在途热更新仍可能发生；目录大小和符号链接限制见使用说明。

下一片将已登记引用与一个可验证的原生能力选择接通，补计划/实际加载的逐项核对，再扩展原会话改配与版本生效边界。W01 的独立前台和 worker 工具结果缺口保持可见；W01/W02 不记整体完成，W04–W07 不提前解锁。


## 10. Codex stdio MCP 原生选择与加载核对

本片将已登记 MCP 接入生产 Application / Runtime / Web。新增 `create.capability_selections`，与只记录来源的 `capability_refs` 分开；选择自动进入不可变引用和快照。当前只选择所用 Codex profile `config.toml` 内已有的本地 stdio server，启用/禁用按会话覆盖，其他 MCP 继承原生配置。使用方式见 [控制台说明](../CONSOLE.md#codex-原生-mcp-会话选择)，完整证据见 [W02-native-selection.json](../goals/evidence/W02-native-selection.json)。第 9 节保留此前仅引用片的历史边界。

### 原生依据与执行保护

[官方 App Server 文档](https://learn.chatgpt.com/docs/app-server)与 Context7 的 `/openai/codex` 源码索引核对了配置读取、MCP 状态分页及 elicitation 回复。`config/read(includeLayers=true)` 只用于证明选中 server 的定义来自已登记 user 文件，不能代替实际加载证据。带 `threadId` 的 `mcpServerStatus/list` 核对本次会话，完整分页且没有重复后才确认；启用必须已连接且无清单错误，禁用必须明确 disabled、工具集为空且无清单错误。其他继承 server 单列。

选择状态不符时清除 `ExecutionClear`，空闲状态改为 unknown，发送/回复/恢复自动化不能继续。原生会话可能已创建，因此创建的 accepted 回执不是 MCP 就绪保证。原生后台重启时恢复使用同一选择，再核对实际状态。快照来源漂移、原 ID 与输入权保护继续复用。

`mcpServer/elicitation/request` 只接入已验证的 `mode=form`、`codex_approval_kind=mcp_tool_call`、空 object schema 单次调用审批，按原生 `action/content` 回复。不给永久或会话级授权，不代填任意表单；请求仍绑定原连接、会话、轮次与请求 ID。详情显示服务/工具的原生提示和参数。历史成功调用只接受完成且无错误的 `mcpToolCall`，不保存原始 MCP 结果到新快照。

### 验证结果

- Application / Runtime 回归：选择自动引用并持久化、重启和被动引用兼容、来源/模式/重复/后端错误拒绝、配置层冲突、分页失败/循环/缺条目、禁用仍暴露工具、加载不符阻止输入、审批原生格式及过期/重复保护通过。
- [生产纵切脚本](../tests/native_mcp_selection_smoke.py)：同一后台同时保有启用和禁用会话；原生审批前零调用，允许后仅调用一次，同会话第二次调用仍需重新审批，完整绑定结果回到模型；拒绝不调用、不记成功；控制台崩溃及 daemon 重启保持原 ID、快照和选择；不可用服务阻止发送/恢复且没有模型请求。profile 未改写，新快照/证据未复制 MCP 合成凭据值。
- 此版本 Responses 请求省略工具 schema，不能用请求里缺少 `tools` 证明排除。实验通过原生 `functions.exec` 的 `ALL_TOOLS` 注册表读出 `identityAvailable=false`，结合按线程的 disabled 状态和实际零调用证明本样本排除。首次仅检查模型请求 schema 的测试未进入审批，修正验收依据后通过，没有放宽产品检查。
- Codex/Claude 旧快照三轮、重启及漂移回归通过；Codex 原生命令批准/拒绝、重复回复与用户问题回归通过。
- [浏览器脚本](../tests/native_mcp_selection_browser.js)的 18 项检查通过：默认继承、被动引用未勾选、显式选择提交、自动引用、加载和继承分列、单次审批、完成结果、刷新后同快照、1440px/768px 布局；无 JavaScript 异常。截图与结果见 [浏览器证据](../goals/evidence/W02-native-selection-browser.json)。
- 全量 `go test -race -count=1 ./...`、`go vet ./...`、`make dev-build` 通过。所有验证使用自有临时环境和本地供应商，没有修改用户配置、覆盖 `bin/netcap`、提交或推送。

### 剩余范围与下一步

只支持 Codex profile-defined stdio MCP 的逐会话启停；不支持 Claude、Skill、CLI 插件原生选配、远程 MCP、任意安装或原会话改配。快照没有固定 MCP 可执行程序及依赖的内容、全部进程环境、账号动态能力，也没有实现完整配置继承和权限合并，`coverage=recorded_sources_only` 保持不变。加载核对是最近观察证据，不承诺原生在途变化的原子阻断。

下一片沿 W02 补显式改配和版本生效边界，再扩展第二类原生选择。W01 严格独立前台和 Claude worker 工具结果缺口继续列示；W01/W02 保持 in_progress，W04–W07 未开始，不锁定底座。
