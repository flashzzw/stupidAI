# W02.04 · 原 ID 显式应用与版本核对

日期：2026-09-30。完成本子任务限定的**原快照已有 Codex stdio MCP 启用/禁用**路径。W02 整包、W01 硬入口与整个 W 系列仍未完成。前一片的预览/待应用证据保留在 [原记录](W02-04-pending-change.md)，本记录补齐其应用缺口。

## 最终行为

`prepare_execution` 与 `apply_execution` 复用 Application 的持久 Operation、命令去重、代次、执行绑定检查及 Runtime。准备先阻止新的输入/自动恢复，等待已有观察退出，再读取原线程的完整空闲状态和原生历史指纹。指纹使用完整原生 turn JSON，包含显示层未使用的工具结果字段；没有历史或未完成轮次不能生成依据。

已加载线程不能热切换，明确返回 `not_sent` 并保留原 ID/原快照/待应用选择。只有原线程实际 `notLoaded`、历史与准备依据一致且队列/Goal/执行范围可核对时，才以同一 ID 恢复候选配置。工作台不停止共享后台；当前路径需在原 CLI 维护流程中安排后台重启。直接关网页或 proxy 不满足卸载前置。

应用回执先登记为 acknowledged/uncertain，随后通过只读核对确认实际的 MCP 清单、原 ID、路由、凭据来源与空闲范围；成功后候选快照原样成为当前版本，旧快照进入历史。旧 Operation 仍引用旧版本，新输入引用新版本。迟到核对可以记录真实生效版本，但不能撤销人工接管或解除自动化暂停。

回执丢失、控制台崩溃不会重发 `thread/resume`。如果原生后台也重启，无法读取应用时的实际版本，则保留未知、最后确认的旧版本、候选与输入阻断；没有把失败回执、断线或空清单解释为“未应用”。

## 验收逐项映射

| W02.04 条件 | 权威依据 | 结论 |
|---|---|---|
| 原空闲会话改配/重启边界 | [原生 15 项](W02-04-apply-native-boundary.json)：loaded resume 不切换，unsubscribe 不立即卸载，notLoaded resume 原 ID 生效；补充卸载状态下三种查询结果 | 通过限定版本边界；热切换不支持并明确拒绝 |
| 差异预览、待应用和持久变更操作 | 前片证据；[17 项待应用回归](W02-04-apply-pending-regression.json)；`TestExecutionApplyDurabilityCrashAndLateTakeoverRetainVersionHistory` 在 Runtime 写入前直接读取存储 | 通过；先有候选与 dispatching 记录再调用原生 |
| 新版本下一轮生效、旧快照/轮次不变 | [双向应用 18 项](W02-04-apply-normal.json)、[最终故障/双向 20 项](W02-04-apply-fault.json)，原 ID v1 enabled → v2 disabled → v3 enabled；[v1](W02-04-apply-before.json)、[v2](W02-04-apply-applied.json)、[v3](W02-04-apply-reenabled.json)、[Operation 引用](W02-04-apply-operations.json) | 通过；v2 实际工具注册表排除工具，v3 审批后调用一次 |
| 普通重连保持原绑定，在途只保存选择 | [待应用回归](W02-04-apply-pending-regression.json)、[既有选择回归 28 项](W02-04-apply-mcp-regression.json) | 通过；只有明确准备后才改变自动恢复策略 |
| 在途、unknown、审批/问答等待不会误应用 | Go 状态测试对保存后 prepare/apply 逐项拒绝；Runtime 对 loaded、无依据、历史变化、队列、终端查询失败逐项阻断；真实 HTTP 审批等待中保存后原工具仍按原审批执行 | 通过；unknown 不清除占用 |
| 丢回执/崩溃核对实际版本，不重复应用 | 真实透明 proxy 丢弃 `thread/resume` 成功回执，并注入不完整历史；控制台 crash 后解除故障，仅通过实际状态核对完成。[20 项结果](W02-04-apply-fault.json)及[原生写入序列](W02-04-apply-native-mutations.json) | 通过；首次故障应用核对完成前 resume 调用数始终为 1，之后的调用分别为独立恢复/新版本操作 |
| 实际版本不可读时保留未知 | [10 项联合故障](W02-04-apply-unknown.json)：丢回执＋控制台 crash＋原生 daemon restart，仍 unknown/hold，不发第二次 resume，不接受新 apply | 通过保守处理；不宣称能够恢复已经不可观测的事实 |
| 不支持的变更保留原会话并说明原因 | 已加载状态的真实 not_sent；来源/版本/路由/额外字段/未支持能力输入测试；UI 说明新 Backend/供应商/状态归属需要新尝试与交接 | 通过限定范围；完整 Attempt 关联仍属 W05 |
| 生产界面、历史版本与暂停状态 | [12 项浏览器](W02-04-apply-browser.json)，准备/禁用输入/等待卸载/明确应用/历史 v1/新轮次/刷新/1440 与 768 布局 | 通过；[1440 图](W02-04-apply-browser-1440.png)、[768 图](W02-04-apply-browser-768.png) |

## 执行与被测产物

固定 CLI：proxy `0.157.1` / app-server `0.158.0`，React 19.3.0。所有运行均用自有 `/tmp/netcap-console-*` home/profile/cwd/状态目录、真实 CLI 与 stdio MCP、本地模拟供应商；无真实模型调用、共享后台切换、用户数据迁移或外发。只替换开发构建 `.dev/bin/netcap`，旧 `bin/netcap` 未替换。当前官方文档与目标版本 schema 的来源仍见 [前片 manifest](W02-04-manifest.json)，本次复用其结论并补做卸载查询实验。

| 命令 | 实际结果 |
|---|---|
| `go test -race ./...` | [全包通过](W02-04-apply-go-test.txt)；后续完整历史指纹/错误提示调整后，再执行相关三包，见下行 |
| `go test -race ./internal/app ./internal/runtime ./internal/console` | [最终相关三包通过](W02-04-apply-targeted-tests.txt)，包含历史未投影字段、等待旧观察退出、持久写入前置与迟到人工接管测试 |
| `go vet ./...` | [通过](W02-04-apply-go-vet.txt) |
| `make dev-build` | [最终 TypeScript/Vite/Go 构建通过](W02-04-apply-build.txt) |
| `python3 tests/native_mcp_reconfigure_probe.py` | 15/15 原生边界通过 |
| `python3 tests/execution_apply_smoke.py` | 18/18；普通双向版本链，原始记录目录见结果 JSON |
| `python3 tests/execution_apply_smoke.py --fault` | 20/20；包含全部双向应用行为及真实回执丢失/控制台 crash，最终完整历史指纹实现后执行 |
| `python3 tests/execution_apply_smoke.py --unload-after-drop` | 10/10；原生后台再重启时保留未知，不重放 |
| `python3 tests/execution_change_smoke.py` | 17/17 待应用回归 |
| `python3 tests/native_mcp_selection_smoke.py` | 28/28 既有选择/单次审批/重启/不可用能力阻断回归 |
| Playwright CLI 独立 `w0204apply` 会话 | 最终开发构建 12/12；夹具/两阶段脚本见 [使用说明](../../CONSOLE.md#原会话-mcp-改配与版本历史) |

原始 `/tmp` 目录可能清理，表中关键响应、工具调用、Operation 关联和截图已归档。最终源码/构建/证据哈希见 [本轮 manifest](W02-04-apply-manifest.json)。普通双向 18 项发生在完整历史指纹加固之前，最终的 20 项覆盖同一路径并增加故障；浏览器针对最终前端及完整应用链路；之后仅补强 Runtime 对未知原生状态的拒绝，最终相关三包及 20 项故障验证覆盖该变化。

## 本轮发现与修复

- 浏览器准备动作曾因后台观察锁直接被拒绝。改为持久登记后等待旧观察退出，再核对代次及来源并调用 Runtime；加入并发状态测试，浏览器复测通过。
- 反向启用实验曾读取重启前缓存的 idle 状态，且没有检查准备操作的 not_sent 终态就继续重启。原实现正确拒绝了缺少准备依据的应用；测试改为读取重启后时间戳的新原生观察，并检查每个准备终态。没有放宽原生空闲规则。
- 既有 MCP 回归曾在会话显示 idle、原 send Operation 仍 running 时发送下一轮，收到 409。测试现在先等待原轮次及审批 Operation 终态，仍要求下一次调用重新审批。
- 浏览器测试改用新会话唯一标题、明确选中后再输入；新增历史 `<summary>` 后使用直接子元素定位。`TURN_0` 的本地供应商响应是 `auxiliary`，已按实际 fixture 契约断言；未修改产品内容来迁就测试。

## 保留的硬要求

本项只关闭指定 Codex 路径的版本切片；不等于完整 W02。新增能力、恢复继承、远程 MCP、Claude/Skill/CLI 插件选配、完整权限/来源合并、多配置联合验收仍在 W02.01/05–09，coverage 仍为 `recorded_sources_only`。未物化历史的原线程、无法验证的子会话或后台执行仍可返回不支持/未知；不会为提高通过率创建新会话或停止共享后台。

严格独立前台入口、Claude worker 工具结果连续性、用户试用、Windows 宿主链路及真实角色协作等未在本轮通过，需求未调整。下一项按进度进入 W02.01 生产多配置登记与隔离。
