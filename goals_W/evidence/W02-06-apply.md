# W02.06 · Claude 原 ID MCP 启停改配

日期：2026-10-01（Asia/Shanghai）。在 [stdio](W02-06-stdio.md)和 [HTTP/SSE](W02-06-remote.md)启动选择上，复用 W02.04 的候选快照、持久 Operation、准备/应用及历史晋升。此片支持原快照已选择资产的启停；W02.06 仍为 **in_progress**，累计 done 7/40，Goal active。

## 已实现

- 原会话可预览、保存、撤销、准备及明确应用已选 MCP 的启停。一个资产有多个服务时，API 和界面按资产选择，预览逐个服务列出差异，全部服务一起改变。保存保留在途输入、原审批及原生选择。
- 准备阶段核对原身份、来源、MCP、消息/工具/后台占用，保存配置与官方 SDK 会话消息的摘要，保持暂停。消息读取包含系统消息；摘要保留 SDK 返回对象中的全部字段，未仅使用 Go 的显示投影。
- 明确应用时重新验证基线和空闲状态，持久登记 `applying`，仅关闭当前自有 query 并确认子进程退出；然后以原 ID/cwd/settings/凭据重启目标选择。其他会话保留原 query 和未决权限。没有使用原生 `setMcpServers` 动态重加。
- 成功加载的脱敏清单与新配置在同一 metadata 原子提交为 `verified`，工作台只读核对后晋升候选快照。旧快照、旧操作和轮次关联保持原样，应用不会重新授予自动化输入权。
- 有持久成功证据的丢回执可在控制台重启后确认最后生效版本；当前连接和当前 verified 状态另行判断。没有成功提交的中途崩溃保留未知，不重放改配或模型输入。候选失败只有在确认其进程退出后才持久恢复原绑定，保持暂停并等待显式重试/撤销。

协议复用既有 NDJSON，新增 `prepare_execution` / `apply_execution` 方法。Go State schema 不变，bridge 的平面 SessionConfig metadata 兼容旧格式，增加可选 `_configuration` 字段；不写入展开的凭据、命令或 SDK status 原始 config。使用方式见 [CONSOLE](../../CONSOLE.md#claude-原会话-mcp-启停改配)。

## 依据与实测

固定 Claude Code **2.1.280**、Agent SDK **0.3.280**、Node **24.13.0**、Go **1.26.2**。[官方 Query 文档](https://code.claude.com/docs/en/agent-sdk/typescript)通过 Context7 核对 `close()` 终止底层进程及 `getSessionMessages`/resume 接口；本地锁定 `sdk.d.ts` 核对 `includeSystemMessages`。

[原生关闭/恢复实验](W02-06-apply-native-restart.json)用同一 UUID 完成 enabled→disabled→enabled，三次关闭均确认子进程退出，SDK 消息在启动/关闭前后摘要相同；同 profile 的 sibling query 持续存活并随后完成调用。MCP 与模型认证正确，重新启用再次审批。该实验使用包含系统消息的读取方式。

生产和浏览器全部使用 `.dev/bin/netcap`、自有 `/tmp/netcap-*` profile/cwd/settings/状态、真实 CLI 与 MCP、loopback 合成供应商；未调用真实供应商，未停止共享后台、迁移用户数据或提交/发布。源码与构建归档哈希见 [manifest](W02-06-apply-manifest.json)。

| 层次 | 实际结果 | 证据 |
|---|---|---|
| Go 状态/契约 | 资产多服务同时变更、来源漂移拒绝、未知改配阻断、既有版本/CAS/持久操作测试通过 | [定向](W02-06-apply-targeted-tests.txt) |
| 工程 | 全量 Go race、vet、bridge TS、web typecheck/Vite、Go 开发构建通过 | [race](W02-06-apply-go-race.txt)、[vet](W02-06-apply-go-vet.txt)、[bridge](W02-06-apply-bridge-build.txt)、[构建](W02-06-apply-build.txt) |
| stdio 原生/生产 HTTP | **23/23**，双服务资产 v1→v2→v3、原 ID/历史、单次权限、sibling 待审批不受影响、重启恢复 | [stdio](W02-06-apply-stdio.json) |
| HTTP MCP 原生/生产 HTTP | **23/23**，同一矩阵与固定账号/工作区绑定 | [HTTP](W02-06-apply-http.json) |
| SSE MCP 原生/生产 HTTP | **23/23**，同一矩阵与固定账号/工作区绑定 | [SSE](W02-06-apply-sse.json) |
| 丢回执 | **7/7**，真实应用成功后丢弃线上的成功回复、临时隐藏成功记录；重启后只读核对晋升，人工控制不被覆盖，应用仅一次 | [drop](W02-06-apply-drop.json) |
| 提交前 crash | **8/8**，真实 metadata 出现 applying 后暂停自有 bridge 并结束其进程组；重启保留旧/候选快照、unknown 和输入阻断，无重放 | [crash](W02-06-apply-crash.json) |
| 候选连接失败 | **6/6**，服务端拒绝认证，确认候选退出并保留原绑定；重启后显式重试同一候选成功并需要审批 | [rollback](W02-06-apply-rollback.json) |
| 浏览器 | **22/22**，单个资产控件控制两服务，预览/保存/准备/应用、版本与历史、单次审批、1440/768 布局；无 JS pageerror | [浏览器](W02-06-apply-browser.json)、[1440](W02-06-apply-browser-1440.png)、[768](W02-06-apply-browser-768.png) |
| 受影响原路径 | Codex 原 ID 应用 **18/18**；Claude Bash allow/deny、问题、后台、interrupt/crash 和 idle 回收恢复通过 | [Codex](W02-06-apply-codex-regression.json)、[Claude 交互](W02-06-apply-claude-interaction.txt)、[idle](W02-06-apply-claude-idle.txt) |

故障代理只包裹生产 bridge 的 NDJSON；不替换 SDK、CLI 或 Runtime。它记录 apply operation ID/次数，不记录包含 api_key 的请求正文。crash 测试仅结束通过 Popen/实际 PID 验明归属的自有进程组。

## 验收中修复的问题

1. 新建改配 Operation 的 evidence 尚为空时，界面直接 `.slice()` 会抛错并白屏。已按 API 的可空数组契约处理，最终浏览器全流程通过。[原始错误](W02-06-apply-browser-render-failure.txt)保留。
2. 固定 Playwright CLI 的 `waitForFunction(async () => false)` 在实测中约 4ms 就返回。最初浏览器在 apply 尚未完成时读取到 v1，并误把等待结束当作 v2 已生效；增加 revision 输出后确认该读取仍没有 v2 revision。当前脚本显式 await 每次 API 查询并有界轮询，再严格断言 operation succeeded、revision、两服务实际选择和原 ID；没有放宽为任一旧清单，也没有修改浏览器全局配置。生产三协议测试不使用该等待器。

## 重跑与限制

```bash
make claude-build dev-build
go test ./internal/app ./internal/runtime -run 'TestClaude.*(ExecutionChange|Configuration)|TestExecution(Change|Apply)' -count=1
python3 tests/claude_mcp_restart_probe.py
python3 tests/claude_execution_apply_smoke.py --transport stdio
python3 tests/claude_execution_apply_smoke.py --transport http
python3 tests/claude_execution_apply_smoke.py --transport sse
python3 tests/claude_execution_apply_fault_smoke.py --case drop
python3 tests/claude_execution_apply_fault_smoke.py --case crash
python3 tests/claude_execution_apply_fault_smoke.py --case rollback
python3 tests/execution_apply_smoke.py
go test -race ./...
go vet ./...
```

浏览器使用 `tests/claude_execution_apply_smoke.py --transport http --hold --info-file <自有文件>`，以该状态目录令牌登录固定 CLI 的自有命名浏览器，运行 `tests/claude_execution_apply_browser.js`。本次 fixture、代理和浏览器已关闭；最终执行句柄均以 0 退出，按自有临时目录扫描未发现残留进程，见 [收尾检查](W02-06-apply-process-cleanup.json)。原始目录由 manifest 记录。

剩余范围：

- 这里只改变原快照中的 enabled/disabled。新增资产、恢复继承、能力资源版本替换及权限策略调整仍未接入，不以执行快照 v1/v2/v3 代替资产版本升级。
- 双服务同资产已验；多资产、混合 transport、插件/受管/完整同名来源与权限冲突仍需矩阵验证。OAuth 与动态 header helper 保留未接入状态。
- 成功提交前崩溃会保持未知，尚未提供自动猜测回退或清除未知的按钮。当前流程不能从缺失证据推出“没有应用”；需独立核对后处理。
- 完整有效配置仍为 `recorded_sources_only`；独立前台、Claude worker 工具结果、Windows 宿主及真实角色协作等原硬条件不变。
