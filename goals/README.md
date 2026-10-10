# 实施入口与历史工作包

新产品 W01–W07 已有独立入口 [goals_W](../goals_W/README.md)、[最新进度](../goals_W/PROGRESS.md)和[持续实施目标](../goals_W/IMPLEMENT.md)。本目录保留 T 系列及迁移前 W 过程记录；W 后续状态和新证据按新目录约定维护，旧证据原路径保留。

下文保留 [历史实施规划](../MULTI_AGENT_DESIGN.md) 的 T00–T15 入口和依赖，仅用于明确继续旧 Web 控制台范围的任务。已有实现和证据继续有效，去向见新计划第 4 节；不能从旧工作包的最小编号推断新产品下一步。真实环境切换遵循已有用户授权。

## 选择入口

- **完整实施**：使用 [IMPLEMENT.md](IMPLEMENT.md)。一个 Codex Goal 按依赖持续执行，工作包完成后继续下一项，直到首期本地交付全部验收。
- **逐项实施**：使用下表中的一个 Txx 文档。仅完成该工作包；前置未满足时说明缺口，不越过前置或自行扩大范围。
- [EXECUTION.md](EXECUTION.md) 是各 Goal 的公共执行规则；[PROGRESS.md](PROGRESS.md) 是跨轮次/会话的进度与证据索引。它们不是待执行工作包。

在 Codex 的 Goal 入口填写短目标即可，例如：

```text
在 /home/zzw/codex-netcap 中读取 goals/IMPLEMENT.md，按其要求完成轻量多 Agent 会话管理首期实现与验证。先读取公共执行规则和进度，按依赖持续推进；完成全部验收后再完成当前 Goal。
```

只执行首个工作包时：

```text
在 /home/zzw/codex-netcap 中读取 goals/T00-baseline.md，只完成 T00 的基线与隔离准备，按公共规则记录验证结果；满足全部完成条件后再完成当前 Goal。
```

以上是目标文本，不是 shell 命令。不设置默认 token budget；用户指定时才使用对应预算。

## 工作包索引

表中顺序是默认单代理执行顺序；“前置”才是技术依赖。T04/T05 可分别推进，T13 不依赖并发调度完成。

| 工作包 | Goal 文档 | 前置 | 交付节点 |
|---|---|---|---|
| T00 | [基线与隔离准备](T00-baseline.md) | 无 | M0 |
| T01 | [三类最小协议实验](T01-runtime-probes.md) | T00 | M0 |
| T02 | [Codex Web 会话纵切](T02-codex-session.md) | T01 | M1，需 T03 一起验收 |
| T03 | [审批、恢复与人工接管](T03-codex-recovery.md) | T02 | M1 |
| T04 | [Claude SDK bridge](T04-claude-bridge.md) | T03 | M2 |
| T05 | [OpenCode Adapter](T05-opencode-adapter.md) | T03 | M2 |
| T06 | [统一交互与事件契约](T06-session-alpha.md) | T04、T05 | M2 Alpha |
| T07 | [供应商路由与探测](T07-route-health.md) | T06 | M3 |
| T08 | [路由与会话保活](T08-keepalive.md) | T07 | M3 |
| T09 | [单文件 Task 与验收](T09-single-task.md) | T08 | M4 |
| T10 | [Goal runner 与顺序清单](T10-goal-queue.md) | T09 | M4 Beta |
| T11 | [依赖与工作区并发](T11-task-concurrency.md) | T10 | M5 |
| T12 | [候选路由与容量恢复](T12-route-dispatch.md) | T11 | M5 |
| T13 | [通知兼容与独立订阅](T13-notifications.md) | T06、T07、T10 | M6 |
| T14 | [UI、打包与资源测量](T14-delivery.md) | T08、T10、T12、T13 | M6 |
| T15 | [迁移、降级与最终验收](T15-migration.md) | T14 | M6 v1 |

## Goal 模式与快照

OpenAI Docs 确认 App Server 的 `thread/goal/set/get/clear` 管理与 TUI `/goal` 相同的持久目标；objective 最多 4,000 字符。用短目标引用文件，具体要求留在 Markdown。官方依据：[Codex App Server](https://learn.chatgpt.com/docs/app-server)，2026-09-28 获取。

当前原生 Goal 已存在时，沿用它持续工作；总 Goal 内切换工作包只更新进度，不替换 objective、不嵌套创建子 Goal，也不重置预算。原生状态工具遵守运行环境的实际规则，详情见公共执行规则。

如果外部清单执行器把任务文件复制成快照，应将 cwd 指向项目根目录。所有任务的执行引用按项目根目录解析，不能改为相对快照文件所在目录；本目录链接主要供人工浏览。

只向逐项清单加入 T00–T15，按前置排序；不要把 README、IMPLEMENT、EXECUTION、PROGRESS 一并按通配符加入。完整实施与逐项模式二选一，避免两个执行器管理同一工作包。当前旧 `netcap run` 还会在队列结束后继续天气保活；本目录不提供或启动其自动运行配置。

## 交付范围

Goal 的完成目标是本地实现、隔离测试、打包和迁移演练。真实供应商保活效果、用户共享后台切换、真实 hooks 安装和外部通知收取分别记录；没有已有明确授权时留作上线操作，不能因此跳过可完成的本地实现。
