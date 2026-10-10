# W02.04 · 原生边界与待应用选择

记录时间：2026-09-30。子任务仍为 **in_progress**；本记录证明原生边界与“预览 → 保存待应用 → 保留旧执行 → 撤销”纵切，不证明显式原生应用已经实现。

## 实现与隔离

- 复用 Application 的持久命令去重、Session JSON 存储、来源/路由/状态归属检查与现有 Runtime。新增 `execution_change.go`，未修改原生 Adapter 的恢复语义。
- 候选快照使用独立 ID、第 2 版与 `previous_snapshot_id`，放在 `pending_execution_change`；当前 `execution_snapshot`、Scope、Generation、原操作快照引用均不变。对旧数据不补造 revision。
- 同一预览同时绑定当前快照 ID、待应用 ID 和候选摘要。保存重验来源，重复 command_id 返回原回执；旧页面不能覆盖/撤销新选择。只接入原快照已选择的 Codex stdio MCP 启用/禁用。启用要求登记仍有效且版本相同。
- 浏览器沿用详情面板；显示差异、未生效状态、边界及撤销。修复本地改配回执被上一条原生操作终态遮住的问题。
- 所有 CLI/供应商/MCP 使用 `/tmp/netcap-console-*` 独立 home、profile、cwd、状态目录、loopback endpoint 与 fixture 凭据。只重启/停止测试创建的原生后台；无真实供应商调用、用户配置修改或共享后台切换。开发输出为 `.dev/bin/netcap`，旧 `bin/netcap` 未替换。

## 版本与原生边界

实际执行版本：Codex proxy **0.157.1**、app-server **0.158.0**。当前 PATH 的 0.159.2 未替换既有固定夹具，也未据此宣称验证通过。

已获取 [官方 App Server 文档](https://developers.openai.com/codex/app-server)，并用目标 app-server 的 `generate-json-schema --experimental` 核对 `thread/resume`、`thread/settings/update` 与 MCP 方法。官方文档允许 resume 配置覆盖、描述 unsubscribe 后的无订阅宽限期及全局 MCP reload；它没有证明已加载线程的 MCP 可以逐会话热切换。最终以下结论来自真实 CLI 实测：

| 条件 | 当前证据 |
|---|---|
| 已加载、idle 的原线程收到禁用 MCP 的 resume | 回执原 ID 正确，但 MCP 仍 connected；再执行一轮仍保持旧选择 |
| unsubscribe | 返回 unsubscribed，但 thread/read 仍为 idle，没有立即卸载 |
| 自有 daemon 重启后的原线程 | thread/read 为 notLoaded；带覆盖 resume 后 identity 为 disabled，未选择的 inherited 仍 connected |
| 其他线程与原历史 | sibling 的选择不变；恢复后仍为原 ID，原两轮内容完整保留 |
| profile 与副作用 | config.toml 字节未变化，边界探针没有 MCP 工具调用；请求均发往本地 fixture |

原生 [14 项结果与清单](W02-04-native-boundary.json)，完整 [变更前轮次](W02-04-native-history-before.json) / [变更后轮次](W02-04-native-history-after.json)。原始日志目录 `/tmp/netcap-console-017ybet8`。文档/schema 原始缓存位于 `.dev/w02-reconfigure/`，关键来源与摘要另存 [版本清单](W02-04-manifest.json)。

## 实际验证

| 层次 | 命令/入口 | 结果与证据 |
|---|---|---|
| 状态、竞争、授权 | `go test -race ./...` | 全包通过，[输出](W02-04-go-test.txt)。覆盖候选/旧版本分离、无原生副作用、在途回执、unknown/审批/问答等待保持、预览过期/来源漂移、替换/撤销 CAS、重启和命令幂等。 |
| 新增启用登记测试 | `go test -race -count=1 ./internal/app -run TestExecutionChange` | 通过，[输出](W02-04-change-tests.txt)。在全包检查后补充启用前登记停用/升级/移除拒绝路径；测试源改变后重新执行相关集合。 |
| 静态检查 | `go vet ./...` | 通过，[输出](W02-04-go-vet.txt)。 |
| 构建 | `make dev-build` | TypeScript/Vite/Go 开发构建通过，[输出](W02-04-build.txt)。 |
| 真实 CLI 原生边界 | `python3 tests/native_mcp_reconfigure_probe.py` | 14/14，通过；不依赖生产改配接口。 |
| 生产 HTTP | `python3 tests/execution_change_smoke.py` | [17/17 通过](W02-04-http-pending.json)：审批等待中保存、原工具按原审批执行一次、控制台 crash/daemon restart 后保留候选而继续旧选择、后续轮次引用旧快照、撤销与幂等。原始目录 `/tmp/netcap-console-tnfiyob9`；归档 [预览](W02-04-http-preview.json)、[保存](W02-04-http-saved.json)、[重启后](W02-04-http-restored.json)。 |
| 既有 MCP 回归 | `python3 tests/native_mcp_selection_smoke.py` | [28/28 通过](W02-04-native-selection-regression.json)，含原生启用/禁用、实际工具注册表、单次审批/重复拒绝、重启及不可用服务阻断。原始目录 `/tmp/netcap-console-c6vy58om`。 |
| 生产浏览器 | `playwright-cli -s=w0204 run-code --filename tests/execution_change_browser.js` | [12/12 通过](W02-04-browser.json)。真实 CLI＋本地供应商，检查预览/保存/回执/刷新/撤销与 1440/768 宽度无横向溢出。截图：[1440](W02-04-browser-1440.png)、[768](W02-04-browser-768.png)，已人工查看 768 图。 |

浏览器先复现 `save_receipt_is_truthful` 失败：保存后候选已持久化，composer 仍展示 create 的“已完成”。本轮增加与 Session 绑定的本地命令回执优先展示；修复后上述浏览器脚本通过。初次授权测试因未设置同源 Origin，先被既有 CSRF 检查拒绝；修正用例使其单独验证未登录访问后通过。首次浏览器下拉定位由 label 文本改成 snapshot 中实际 combobox 的 accessible name；未修改产品以迁就定位。

最终浏览器及构建在回执修复后执行；HTTP/原生回归测的是相同 Go 生产逻辑，发生在仅前端回执修复前。对应归档时点的源码、最终二进制、web/dist、证据哈希见 [manifest](W02-04-manifest.json)，不将该哈希外推到后续修改。

## 保留条件与下一步

1. **显式应用尚未实现**。当前没有 apply 操作，待应用版本不会因重连、恢复或下一轮自动生效。下一片复用持久 Operation/Runtime，解决自动 Observe 恢复旧选择与显式应用的竞争；仅在原线程真实卸载、执行范围清楚后恢复同 ID。不得自动停止共享后台。
2. **应用故障未验证**。应用前持久意图、丢回执后的实际版本核对、崩溃不重复应用、应用后旧快照/旧轮次留存及新轮次关联均待完成。当前只证明 stage/discard 的持久化和幂等。
3. 新增 MCP、恢复继承、Claude、Skill/CLI 插件及完整来源/权限合并未接入，coverage 仍为 `recorded_sources_only`。Backend/供应商/状态归属仅说明新尝试与交接，Attempt 关联待 W05。
4. W01 的严格独立前台入口、Claude worker 结果连续性、W03 用户试用、Windows 宿主、真实角色协作等硬条件均未在本轮通过；需求未调整。W02.04、W02 工作包及整个 W 系列均未标为 done。
