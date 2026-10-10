# W02.08 · 插件 MCP 使用归属与完整工具轮次

日期：2026-10-03（用户原生日志为 2026-10-02 UTC）。W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 用户可见变化

已选 CLI 插件带入的 MCP 现在进入公共 `capability_evidence`，记录资产 ID、原生插件来源、工具清单及历史成功调用。执行详情单列插件带入的 MCP，与继承服务分开。原生服务 `plugin:fixture:identity` 的工具名转换为 `mcp__plugin_fixture_identity__whoami`；原实现只匹配未转换的服务名，并且插件单独选择时没有生成 MCP 使用投影，导致实际调用没有对应证据。

Runtime 使用插件整包核对时的 MCP 清单，并同时要求整包来源核对通过。只有唯一对应的工具调用及非错误结果记为历史成功；待完成和错误结果不算成功。离线保留历史记录，不能据此认定当前服务已连接。Runtime 和 bridge 均拒绝转换后重名的命名空间，禁用插件也不能由其他来源补位。

## 真实 CLI 与本地供应商验收

用户在可正常监听和通信的 WSL 终端执行 `python3 tests/claude_plugin_execution_smoke.py`，原始目录 `/tmp/netcap-console-bp2rp6xu`。使用 Claude Code **2.1.280**、项目 SDK **0.3.280**、隔离 A/B profile 和两个自有本地插件：正常 stdio MCP + PreToolUse/PostToolUse，以及 PreToolUse 故意退出 2 的失败插件。只访问本地模拟供应商，不调用真实供应商、不修改共享后台或用户配置。

[原生结果](W02-08-plugin-execution/native-result.json)的 **33 项**检查全部通过，`completed_matrix` 和 `expectations_met` 均为 true。新增脚本在异常中途退出时不会因已经通过的部分检查而误报完成；禁用分支要求实际存在模型请求，避免空集合误通过。

代理另从原生 journal、供应商请求、hook 载荷和只读 SQLite 独立核对，**53 项**通过，见[审计](W02-08-plugin-execution/native-audit.json)与[审计脚本](W02-08-plugin-execution/audit.py)。保留脱敏的输入/操作/调用 ID、结果身份、hook 生命周期和请求文件索引，未复制认证文件、完整供应商正文或 SQLite。

| 场景 | 原始记录核对结果 |
|---|---|
| A 正常调用 | 一次真实 MCP；`tool_1` 的 profile/cwd/credential_matches 与 A 对应，结果回到 A 供应商；Pre/Post 各一次 |
| B 正常及恢复后调用 | 两次真实 MCP；`tool_1`、`tool_3` 的结果均回到 B 供应商，Pre/Post 各两次；A/B 认证和请求历史隔离 |
| 审批 | allow-A、allow-B、allow-restored 各一个持久操作/回执；脚本重复同 command_id 得到同回执；后一次调用仍要求新审批 |
| 拒绝 | A 第二次 `tool_3` 有 PreToolUse，无 PostToolUse、无额外 MCP；SQLite 记录 deny，模型收到原生拒绝错误 |
| 整包禁用 | 有实际模型请求且不暴露插件工具；对应原生会话无插件工具调用、无该插件 hook 载荷 |
| hook 失败 | `tool_6` 的 PreToolUse 收到真实载荷，原生日志退出 2/error，模型收到 hook 错误，无额外 MCP、无 harness deny |
| 原 ID 恢复 | A/B/disabled 的原生 ID 和执行快照不变；B 重开后重新审批，继续相同绑定并保留历史 |
| 来源漂移 | 故意追加 `.mcp.json` 内容后 409；文件 hash 确有变化，无新模型请求、无新原生 input 或持久 send 操作 |

hook 载荷中的 `session_id`、`tool_use_id`、cwd、工具名与原生历史相符，夹具也记录其 `CLAUDE_PLUGIN_ROOT`。**这不意味着产品 hook 事件报告了插件来源**：原生事件仍不带来源键，产品继续使用 `source: unreported`，不按事件名猜归属。命令及输出仅在新的 bridge hook 事件中省略；原生工具错误文本属于另外的消息类型，不宣称全部日志已移除命令。

最终 snapshot 在故意漂移之后采集，里面仍可能保留先前成功的 MCP 观察；不将它作为漂移后当前加载已验证的证明。来源检查与最近加载观察是不同证据。完整插件来源追踪、动态依赖冻结及任意 hook 载荷契约不由此夹具证明。

## 本地回归与界面

| 层次 | 实际结果 |
|---|---|
| Runtime 回归 | 新增 5 场景（成功、错误、待完成、离线、转换后重名）先红后绿；相关 Claude/MCP 测试通过 |
| SDK 契约 | 插件选择 16 项通过，含新命名空间冲突回归；该项先红后绿 |
| 浏览器 Runtime 夹具 | 14 项通过，使用实际 Runtime 测试投影与受控 Application 响应外壳，覆盖使用分组、来源、错误/离线/冲突与移动布局 |
| 浏览器真实快照回放 | 15 项通过：真实资产/来源/使用、Pre/Post 成功、四条工具 hook 的实际操作与调用 ID、失败退出 2、禁用无使用、来源未知说明、移动布局；HTTP/SSE 为受控传输，不增加 CLI/模型执行 |
| 原生回归 | 插件选择 14、Skill 原 ID 改配 32 项通过；这些回归没有模型输入 |
| 其他检查 | hook SDK 5、认证恢复 10、planner-only 6 项通过；app/console 全包 race、Claude Runtime race、nativeclaude 标签 vet、bridge 构建及 `make dev-build`（含 web typecheck）通过；新 Python 脚本 AST 解析通过 |

初始 Runtime 使用证据失败和 SDK 冲突失败保留在 `runtime-red.log`、`sdk-collision-red.log`。首次 Runtime 绿尝试因新冲突提示覆盖既有“重复”诊断导致旧测试失败，修复后 `runtime-green-final.log` 通过，首次日志 `runtime-green.log` 不改写。所有未结束的本地执行句柄已收取，最终退出码均为 0。

浏览器结果：[Runtime 夹具](W02-08-plugin-execution/browser-fixture.json)、[真实快照](W02-08-plugin-execution/browser-native.json)。截图：[真实使用 1440](W02-08-plugin-execution/native-1440.png)、[真实失败 390](W02-08-plugin-execution/native-failure-390.png)。两套结果边界分开，浏览器已关闭。

## 复跑与产物绑定

```bash
python3 tests/claude_plugin_execution_smoke.py --planner-only
python3 tests/claude_plugin_execution_smoke.py
node --experimental-test-module-mocks tests/claude_plugin_selection_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test ./internal/runtime -run '^TestClaudePluginMCPUsageKeepsSelectedSourceAndSuccessfulHistory$' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

完整脚本仍需可用的本地 IPC/监听环境；当前代理没有重复探测或绕过已知限制。离线审计：`python3 goals_W/evidence/W02-08-plugin-execution/audit.py /tmp/netcap-console-bp2rp6xu /tmp/netcap-plugin-audit`。

浏览器脚本 `tests/claude_plugin_execution_browser.js` 使用归档的 `native-browser-responses.json` 替换 `__FIXTURE__`，`web/dist` 文件路径映射替换 `__ASSETS__`。回放响应外壳从用户最终 Application snapshot 组装，session/view 未合成；没有声称重新运行了 Handler。Runtime 夹具脚本 `tests/claude_plugin_mcp_usage_browser.js` 的 `__VIEWS__` 来自 `NETCAP_PLUGIN_USAGE_EVIDENCE` 导出。

本片开发构建 SHA-256：`393a947a4bd1951eb7a8f211f386febbca4fa65ca10fe3583ecfd698ad95285a`。用户执行前已构建，之后未再修改生产代码或重建；**用户脚本没有在运行时采集二进制 hash**，此值是代理归档时的构建标识，不伪称来自用户测试。bridge 的构建文件另列 hash。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。

[源码差异](W02-08-plugin-execution/changes.diff)相对 `/tmp/netcap-w0208-plugin-execution-work/before` 生成；[manifest](W02-08-plugin-execution/manifest.json)记录本片源码、构建、原生文件及归档证据 hash。旧证据按前置清单校验不变；未提交。

下一片推进插件原 ID 改配事务。完整受管/已安装来源控制、产品 hook 来源键关联、Codex 插件、更多长运行/后台及故障恢复条件仍需完成；W02.08 与完整 W 系列均未收口。
