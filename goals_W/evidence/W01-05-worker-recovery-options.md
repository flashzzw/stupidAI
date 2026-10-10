# W01.05 · 原生恢复选项与后台工具输出

2026-10-08。继续[上一片原生恢复与只读准入](W01-04-native-recovery.md)，使用固定 Claude CLI 2.1.280、隔离 profile、本地模拟供应商和自有 Python 工具。原生后台工具试验不使用模拟 Runtime，也没有改写原生历史。恢复开关试验沿用既有生产只读观察，SDK 仍为 0.3.280。开发二进制 SHA256 仍为 `dd1f5367639be39e80452ccc7c37f9e10159e1ad58d5be0912dfae17f9d02ede`；本片没有生产代码或构建变更。

本片确认两个边界：原生 supervisor 会覆盖启动环境中的关闭恢复选项；已经取得原生后台任务回执的工具可以按原回执取回输出，但原退出状态仍未知。前台 Bash 尚未返回回执时的自动重发及结果脱离上下文仍未解决，W01.05 不记完成。

| 场景 | 实际结果 | 未满足部分 |
|---|---|---|
| 启动环境设置 `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0` | 原 worker 环境不含该选项；替换 worker 被设置为 `1`，并带异常退出后的继续提示；原有 19 项诊断/生产守卫通过 | 仍自动重发原提示，新请求缺原工具调用/结果，默认权限需新审批；完整恢复失败 |
| 前台工具输出文件 | SIGKILL 前自有工具 stdout/stderr 指向同一原生 `.output`；完成后文件仍含原输出 | 主 transcript 没有该文件引用，模型请求没有该结果；文件存在不等于原工具回执恢复 |
| 后台 Bash 在运行时 worker SIGKILL | 最终 19 项通过；原工具 PID/启动标识、原 ID、原调用和后台回执保留；明确续聊的原生 Read 取得原输出 | 所测 10 秒窗口没有自动通知；原生退出回执缺失，不能标成功 |
| 后台 Bash 在运行时 `claude respawn` | 最终 19 项通过；原生 task-notification 对应原工具 ID/输出文件，随后 Read 把全部输出行交回原模型上下文 | 通知同时报告 completed 与 exit code -1；文件中的“退出码未知”尾注也进入模型，原退出状态未验证 |

两种后台样本都先等待原生 Bash 返回后台任务回执、模型完成启动轮次，再对原 worker 注入故障或执行明确 respawn。因此它们没有重发已完成的原提示，不能据此推断尚未结束的前台轮次不会重试。工具只启动一次，没有批准第二次 Bash、重新运行原工具、复制会话或插入伪造的 tool_result。供应商按原生回执指定的路径发出真实 Read；输出是新 Read 的结果，原 Bash 回执仍作为来源留在同一原 ID 的上下文中。

新入口：

```bash
python3 tests/claude_native_task_recovery_probe.py --case worker-crash
python3 tests/claude_native_task_recovery_probe.py --case respawn
python3 goals_W/evidence/W01-05-worker-recovery-options/retry-off.py
```

关闭恢复选项试验原目录为 `/tmp/netcap-claude-worker-retry-off-_6vdbt67`；最终 SIGKILL / respawn 目录为 `/tmp/netcap-claude-task-recovery-b_5cbcyh`、`/tmp/netcap-claude-task-recovery-edp2decx`。持久工作目录 `.dev/w0105-worker-recovery-options-work`。原始结果见[恢复开关](W01-05-worker-recovery-options/retry-off/result.json.gz)、[后台 SIGKILL](W01-05-worker-recovery-options/worker-crash/result.json.gz)和[后台 respawn](W01-05-worker-recovery-options/respawn/result.json.gz)。这些是固定版本原生 CLI + 本地脚本供应商的协议证据，不代表真实模型方法执行、生产输入交接或任意原生工具恢复。

[独立审计](W01-05-worker-recovery-options/audit.json)51 项通过。它重新解析供应商请求、原生主 JSONL、进程身份取样、原生输出文件和实际工具记录，核对完整 ID、原调用、原生回执路径、唯一一次实际执行、Read 的全部内容和状态尾注；单独检查有无显式用户提示、通知顺序与未知退出码。关闭选项的原生新审批和生产只读守卫仍成立。审计只读取原生数据，没有补写原历史或把副作用重复执行当作恢复。

保留的失败与验证演进：

- 首次后台探针将最后一条用户消息的多个内容块拼接后匹配标记，原生附加的上下文块导致没有发出工具请求，等待审批超时。改为按独立文本块精确匹配后，两例各 15 项通过；没有改变实际权限或工具行为。
- 补充实际 `/proc` 进程路径、profile 归属和启动标识后，两例各 17 项通过。respawn 可使用预热进程，PID 较小不代表仍是原 worker；核对依据是实际 PID/启动标识和原配置，不能只按数值大小推断先后。
- 首次独立审计误要求 native output 永远等于裸 stdout，因 respawn 的真实未知退出码尾注失败。随后把原内容与原生状态尾注分别核对，检查全部文件行进入 Read 的模型请求；最终两例各 19 项通过。没有删除或忽略该尾注，也未把 `completed` / `-1` 改为成功退出。

[查证记录](W01-05-worker-recovery-options/documentation.md)保存 Context7 与实际取回的官方环境变量、Agent view、CLI reference。官方站点包含晚于本地 2.1.280 的说明，未据文档推定本地能力；`RESUME_INTERRUPTED_TURN=0` 的 SDK 约定与原生 supervisor 行为分别实测。jobs/state.json 和二进制内部实现不作为稳定生产接口；未升级 CLI、SDK 或浏览器。

本片新增一个原生探针并更新使用说明/进度。Python 语法、相关真实原生运行、独立审计、文档链接、最终差异和归档哈希均核对；生产 Go/Web/bridge 与两个二进制未改，未重复 Go、构建或浏览器矩阵。6,620 份旧证据保持不变。归档按文件白名单保留所有本轮原始结果、供应商请求、stdout/stderr/ANSI、主 JSONL、输出文件、有限环境取样和审计；不复制 daemon/session 私钥、工作台访问令牌或完整环境。自有原生进程、终端与本地服务均已关闭；未提交或推送。完整清单见 [manifest](W01-05-worker-recovery-options/manifest.json)。

W01.04/W01.05 保持 in_progress，整体仍为 9/40 done、Goal active。下一步继续原前台工具回执/输出关联及实际输入交接，或前置独立的 W02 来源与配置工作；不要重复以关闭该环境变量解决原生后台重试的假设。独立前台、完整输入权、有效配置/能力、Windows 第二环境与其他首版硬要求全部保留。
