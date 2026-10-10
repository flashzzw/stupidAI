# Agent 工作台与 netcap

当前产品方向是 Windows/WSL Agent 工作台：Lead 组织角色协作，两类 CLI 的多执行配置与能力选配统一管理，netcap 作为平台功能插件。入口为 [需求分析](docs/requirements-analysis.md)、[产品设计 V0.3](docs/product-design.md)、[新实施计划](docs/implementation-plan.md) 和 [W 系列任务与进度](goals_W/README.md)。

已有 `serve` 提供独立 Web 会话 Alpha，使用说明见 [本地工作台说明](CONSOLE.md)；新产品尚未实现完整首版。[首批 CLI 实验](docs/execution-capability-experiments.md) 与最新 [W01/W02 补验](docs/workbench-boundary-results.md) 使用隔离环境和本地模拟供应商，不代表生产能力已提升。旧 T00–T15 的复用与调整见新实施计划，不默认继续旧整包路线。

仓库同步源码、依赖锁文件、设计与进度文档，以及 W 系列验收摘要；大体积原始实验归档保留在本地，范围与复验说明见 [验收材料说明](goals_W/evidence/README.md)。

本轮已补齐 [功能归属清单](docs/netcap-feature-ownership.md)。可直接打开 [W03 五流程交互原型](docs/workbench-prototype.html)，无需安装；[浏览器走查与限制](docs/workbench-prototype-notes.md) 单列。原型全为模拟数据；严格独立前台接续仍有缺口，底座尚未锁定。

后续 W01/W02 已接入恢复前核对、人工输入基线、原生 ID 保护及新会话真实状态目录绑定；通过真实终端/工具崩溃恢复和 Claude 双配置 MCP 调用补验。[实现与证据](docs/workbench-boundary-results.md#7-w01-控制修复与-w02-状态绑定) 区分生产功能、实验通过与剩余缺口；使用变化见 [恢复自动化说明](CONSOLE.md#恢复自动化与状态归属)。

此前已修复恢复误用展示缓存，并验证同一 MCP 定义在 Codex/Claude 双配置中的实际调用与恢复排除。Claude 原生后台的 supervisor 崩溃可继续原工具；worker 崩溃后工具输出连续性仍有缺口，详见 [原生故障边界](docs/workbench-boundary-results.md#8-原生故障边界实时核对与跨-cli-mcp-调用)。

W02 已新增 [最小启动快照与外部能力引用](CONSOLE.md#执行快照与外部能力引用)：保存启动选择及来源版本，后续操作保持同一快照，来源变化可查看并阻止继续输入。引用仅用于记录和核对，原生加载/授权保持原规则；[验证与剩余范围](docs/workbench-boundary-results.md#9-最小启动快照与外部能力引用) 单列。

最新 W02 纵切已接入 [Codex stdio MCP 原生选择](CONSOLE.md#codex-原生-mcp-会话选择)：新建时启用或禁用、按会话核对工具清单、网页单次审批及重启保持。其他 MCP 继续继承，原有引用仍只记录；[真实 CLI 与浏览器验证](docs/workbench-boundary-results.md#10-codex-stdio-mcp-原生选择与加载核对)通过，完整 W02 仍在实施。

旧 Go `run` 流程为：自动临时 UTC 日期探测 → Codex 原生 Goal 文件清单 → 持久天气会话；人工介入后暂停。其自动模式使用无审批、无沙箱权限，模型请求均经过 Codex CLI，这不是新平台的默认权限策略。启动与配置见 [Go 后端说明](GO_BACKEND.md) 和 [配置示例](netcap.example.json)。原有 [设计文档](DESIGN_GO_WEB.md) 和 [演示原型](design-preview.html) 保留历史参考。下文仍描述独立的 Python 实现，旧行为保留。

会话自动接管与 Goal 顺序任务使用 `codex_guard.py`。设计及验收条件见 [执行规划](EXECUTION_PLAN.md)。`ping_codex.py` 仍是原有天气轮询入口，使用方式在本文后半部分。

## 会话托管与顺序 Goal

复制 `guard.example.toml` 为 `guard.toml`，填写需要自动接管的会话或工作目录，以及按顺序执行的 Goal 文档。示例文件不含实际任务；空任务队列会在探测成功后进入运行状态简报。

```bash
cp guard.example.toml guard.toml
python3 codex_guard.py --config guard.toml run
```

默认使用 `/home/zzw/.codex`，不读取当前 shell 的 `CODEX_HOME` 来改变默认值。需要其他配置目录时，在 TOML 中设置 `codex_home`，或使用 `--codex-home` 覆盖。托管器连接该目录下的共享 Codex 服务；服务尚未运行时，默认尝试启动。Goal 会话使用 `never` 审批策略和 `danger-full-access` 沙箱，权限对应 `codex --dangerously-bypass-approvals-and-sandbox`。

在同一个共享服务中启动日常交互会话，托管器才可以观察并回复它：

```bash
CODEX_HOME=/home/zzw/.codex codex --remote unix:///home/zzw/.codex/app-server-control/app-server-control.sock -C /你的项目目录
```

如果希望自己的交互会话也使用无审批、无沙箱模式，可在该终端命令加入 `--dangerously-bypass-approvals-and-sandbox`；托管器不会改变原有交互会话的权限。独立服务中正在执行的旧会话不会自动转移到共享服务。

指定会话 ID 后，托管器在 Codex 本轮结束且超出 `takeover_after_seconds` 时发送以下固定原文；你的新消息会停止后续自动接管：

```text
如果原任务还有明确、已授权且无需补充信息的步骤，请继续；如果任务已完成或需要用户决定，请执行一次运行状态检查，只返回一行结果。
```

托管器首次接管会话前会单独登记运行状态脚本的用法，因此你会看到一次初始化消息。需要人工决定或正在等待用户输入的会话不会被自动回答。输入框里尚未提交的文字无法被后台感知。

可在另一个终端管理托管器：

```bash
python3 codex_guard.py --config guard.toml status
python3 codex_guard.py --config guard.toml sessions
python3 codex_guard.py --config guard.toml watch THREAD_ID
python3 codex_guard.py --config guard.toml unwatch THREAD_ID
python3 codex_guard.py --config guard.toml tasks
python3 codex_guard.py --config guard.toml pause
python3 codex_guard.py --config guard.toml resume
python3 codex_guard.py --config guard.toml retry TASK_ID
python3 codex_guard.py --config guard.toml reset TASK_ID
```

队列全部完成后，`reset TASK_ID` 会把指定的已完成任务安排为新一轮执行。

状态文件、文档快照和进程锁放在所选配置目录的 `netcap/` 中。重启后会核对服务器里的 Goal 和消息队列，再继续；结果不确定时会停留在待核对状态，避免重复执行。Goal 为 `blocked`、手动暂停或达到用量限制时不会推进下一项。网络恢复后，若原 Goal 仍活跃而会话空闲，会在原会话中发出一次有记录的继续请求。探测遇到认证或配置错误会停下；修复配置后使用 `resume` 重新探测。队列全部完成后默认运行只读的 `runtime_status.py`，也可将 `keepalive.mode` 改为 `weather`。空闲和保活间隔只是起始配置，应根据供应商实际断连时间调整。托管器退出不会停止共享服务；退出期间由共享服务自身继续运行的 Goal 会在下次启动时重新核对。

## 原有天气轮询脚本

运行：

```bash
python3 ping_codex.py
```

## 运行规则

1. `probe`：使用 `--ephemeral`，让模型执行 `date -u +%FT%TZ` 并返回 UTC 时间；不保存 Codex 会话。失败等待 3 秒后重新探测，成功进入 `persist`。
2. `persist`：创建落盘会话，直接调用 `weather.py` 查询当前城市；如果已经收到会话 ID，即使本次调用失败，也保留 ID 供 `resume` 重试。
3. `resume`：在同一会话内查询城市。成功完成后等待 10 秒，再查询下一个城市；31 个城市依次循环。
4. 落盘会话任何失败（超时、限流、服务错误等）均采用同一规则：首次失败后，最多再恢复该会话重试 5 次。任意一次成功即清零失败计数；5 次重试全部失败才放弃当前 ID，返回 `probe`。
5. 未取得持久会话 ID 时无法恢复，失败直接回到 `probe`。失败和重建均不推进城市索引。
6. 不主动轮换成功会话。每次启动脚本都从临时探测和城市列表首项开始；历史日志用于留存成功会话 ID，不会自动恢复旧会话。

“成功”表示 Codex 退出码为 0、收到 `turn.completed`、未收到 `turn.failed` 且有最终响应；持久会话还需有一致的会话 ID。其用途是确认提供商完成了响应，不对天气内容做额外成功判定。例如，模型原样返回天气服务的错误仍可算提供商响应成功。

每个落盘会话首次成功时写入 `codex_success_sessions.jsonl`，包括首次创建失败、后来恢复成功的情况；同一运行期间每个会话仅记录一次。保留旧日志的字段格式与已有内容。

重试计数示例：

```text
初次请求失败 → 重试 1 → 重试 2 → 重试 3 → 重试 4 → 重试 5
                 任一次成功：清零计数，10 秒后查询下一个城市
最后一次仍失败：放弃会话，回到临时探测；城市保持不变
```

这里的 5 次指脚本层面的 5 次 `codex exec resume` 调用；Codex CLI 每次调用内部可能还会自行重连。

## 参数

| 参数 | 默认值 | 含义 |
|---|---:|---|
| `--interval` | 3 | 失败后等待秒数 |
| `--weather-interval` | 10 | 天气消息成功完成后等待秒数 |
| `--timeout` | 120 | 每次 Codex 调用的超时秒数 |
| `--max-tries` | 0 | 最多调用次数，包含探测、创建和恢复；0 表示持续运行 |
| `--log` | 脚本目录下的 `codex_success_sessions.jsonl` | 成功会话日志 |

调用串行执行。10 秒从上次成功响应完成开始计算，不是每 10 秒并发发起一次请求。

到达 `--max-tries` 时退出码为 2；Ctrl-C 清理本次调用的进程组并返回 130。超时也会清理整个进程组，输出临时文件自动删除。控制台保留状态摘要，最终响应只打印一次。日志写入等本地运行错误会明确停止，避免卡在写盘重试中影响持续消息发送。

## 单城市天气脚本

```bash
python3 weather.py 北京
python3 weather.py 纽约 --timeout 20
```

城市名称限定为 `weather.py` 中的 31 个城市。城市坐标与轮询顺序只维护这一份。每次调用只进行一个城市的天气请求，无内部重试，无缓存，无第三方 Python 依赖。

输出一行城市、天气、摄氏温度、当地查询时间、数据时间和来源；失败时向 stderr 输出原因，并返回非零退出码。

接口采用 [Open-Meteo 当前天气 API](https://open-meteo.com/en/docs)，天气为模型估算。使用 API 返回的 UTC 偏移转换时间，将查询时间和天气数据时间分开显示。

持久会话的提示词形如：

```text
请直接使用 /home/zzw/codex-netcap/weather.py 查询北京天气。用法：python3 /home/zzw/codex-netcap/weather.py 北京。请执行一次并原样返回输出，失败则原样返回错误，不要另找方法或展开解释。
```

路径由脚本自动计算并进行 shell 引号处理。Codex 的工作目录固定为脚本目录。

探测使用 `read-only` 沙箱；天气任务使用 `workspace-write` 并设置 `sandbox_workspace_write.network_access=true`，以允许天气脚本联网。这些设置仅作用于本次命令；模型和提供商仍沿用用户配置。配置含义参见 [OpenAI 官方配置文档](https://developers.openai.com/codex/config-reference/)。

原有天气轮询脚本与新托管入口分别运行；旧脚本不会向新入口登记它所创建的会话。
