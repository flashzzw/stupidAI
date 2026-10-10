# W02.06 · Claude MCP 原生 OAuth 与认证准备

日期：2026-10-01（Asia/Shanghai）。在已有远程 MCP 路径上接通原生 OAuth 凭据复用、刷新、认证诊断和独立登录命令准备。W02.06 保持 **in_progress**，累计 done **7/40**，Goal **active**；新增准备界面和 SSE 完整准备流程仍有实际验证缺口。

## 实现与契约

- 选中的启用服务报告 `needs-auth` 时，在模型输入前明确指出需要 OAuth 认证的服务；原始 config、错误和凭据不进入该诊断。未选服务不阻断已选能力，认证后仍保持原生单次工具审批。
- `prepare_mcp_login` 只接受会话、当前/候选快照 ID 和服务名。沿用原路由、执行配置与资源来源校验，拒绝陈旧快照、未知服务、stdio、来源漂移和额外命令字段。
- 在工作台状态目录建立私有 `mcp-login/login-*`，仅复制所选 HTTP/SSE 定义到 `.mcp.json`；独立目录内的 `.claude/settings.local.json` 批准该项目定义。文件为 0600、目录为 0700；不修改用户原项目或 settings，不启动 CLI、OAuth 或模型请求。
- 生成的命令固定原 `CLAUDE_CONFIG_DIR`、可执行程序、custom settings 和所选服务，ShellQuote 保护路径。原生用户/管理策略继续生效，环境引用需在用户执行命令的终端可用。工作台不接收授权回调或令牌；原生 CLI 完成授权、存储与刷新。
- 目录和文件落盘后持久化 CommandReceipt，不新增状态表或 schema 版本；失败仅删除此次目录。重复 command ID 返回原回执且不暴露内部指针；执行详情 API 可读回历史准备记录。准备不改变执行快照、候选、原 ID、输入权或工具审批。
- UI 提供当前/候选服务选择、准备、复制与找回命令；长命令采用换行样式。新增 UI 已通过 typecheck/构建，尚未通过浏览器实际操作。说明原生终端授权及失败 query 的恢复边界，不能将“已准备”理解为“已认证”。

使用入口与 API 见 [CONSOLE](../../CONSOLE.md#claude-mcp-原生-oauth-认证)，bridge 行为见 [说明](../../bridges/claude/README.md)。

## 原生边界与修复

固定 Claude Code **2.1.280**、Agent SDK **0.3.280**、Node **24.13.0**、Go **1.26.2**。此前使用 `find-docs`/Context7 核对的[官方 SDK MCP 文档](https://code.claude.com/docs/en/agent-sdk/mcp)说明 SDK 不运行交互式 OAuth，未认证服务为 `needs-auth`，见[查询摘录](W02-06-oauth-sdk-docs.txt)。原生登录的具体入口以固定 CLI 实测为依据。

真实 `claude --mcp-config … mcp login identity --no-browser` 无法识别动态定义；独立 `.mcp.json` 项目可识别，但需要项目批准和 TTY。即使指定 `--no-browser`，pipe 输入仍被原生拒绝。自有 PTY 内的独立项目命令可完成 PKCE 与回调。[原始边界记录](W02-06-oauth-native-login-boundaries.txt)已隐藏授权 URL 和回调内容。生产准备沿用这个经过验证的入口，授权仍由用户在原生终端完成。

原生产实现只返回泛化 MCP 验证错误；真实未认证创建的 `missing-A_explains_oauth` 断言失败，见[原始失败](W02-06-oauth-diagnostic-red.txt)。增加 `needs-auth` 专用诊断后，HTTP/SSE 同一场景均通过；认证错误阻断输入，不把工具缺失当作成功启动。

## 验证结果

以下协议检查使用真实 CLI/SDK、`.dev/bin/netcap`、自有 `/tmp/netcap-*` 项目与状态，以及 loopback OAuth/MCP/模型供应商、合成账号。未调用真实供应商、停止共享后台、迁移用户配置、提交或发布。原始目录、归档时源码/构建/附件哈希见 [manifest](W02-06-oauth-manifest.json)。

| 层次与被测阶段 | 实际结果 | 证据 |
|---|---|---|
| 原生 OAuth 边界 | **11/11**：动态定义不可见、非 TTY 拒绝、PTY 登录、SDK 复用、A/B 隔离、刷新、原生 logout A 不影响 B、凭据文件私有 | [原生结果](W02-06-oauth-native.json) |
| 生产 HTTP，加入准备接口之前 | **51/51**：使用测试构造的独立登录目录；缺少凭据阻断、A/B 并发、各自刷新、单次允许/拒绝、撤销和重新认证后原 ID 恢复 | [HTTP](W02-06-oauth-http-before-preparation.json) |
| 生产 SSE，加入准备接口之前 | **51/51**：与上述相同范围；证明 SSE 原生凭据复用与隔离，不覆盖生产准备接口 | [SSE](W02-06-oauth-sse-before-preparation.json) |
| 生产 HTTP，完整准备流程 | **64/64**：通过生产 `prepare_mcp_login` 获取命令并在自有 PTY 中原样执行；含绑定、幂等、单定义、无原生副作用、刷新/撤销/重认证和重启找回命令 | [HTTP prepared](W02-06-oauth-http-prepared.json) |
| 生产 SSE，完整准备流程 | **受阻，未执行完成**：创建第一个本地供应商 socket 时 `PermissionError: [Errno 1] Operation not permitted` | [受阻日志](W02-06-oauth-sse-prepared-blocked.txt) |
| 浏览器，加入准备 UI 之前 | **9/9**：已登录 A 的调用/审批/原 ID、B 未认证诊断、reload 不重试、1440/768 与无 pageerror；不覆盖准备/复制/找回 | [结果](W02-06-oauth-browser-before-preparation.json)、[当时执行脚本](W02-06-oauth-browser-before-preparation-script.txt)、[连接 1440](W02-06-oauth-browser-before-preparation-connected-1440.png)、[缺少认证 1440](W02-06-oauth-browser-before-preparation-needs-auth-1440.png)、[768](W02-06-oauth-browser-before-preparation-needs-auth-768.png) |
| 浏览器，新增准备 UI | **受阻，未执行**：固定 Playwright CLI 无法写入自身 daemon 缓存，报 `EROFS`，浏览器未启动；没有切换配置或浏览器 | [受阻日志](W02-06-oauth-browser-prepared-blocked.txt) |
| 状态与请求边界，最终源码 | Go 两包定向通过：绑定/私有权限、Shell 特殊字符和原 settings 的实际 argv、无原生请求、回执隔离/重启、候选与当前区分、stdio/额外字段/漂移拒绝、持久化失败只清理新目录 | [定向测试](W02-06-oauth-targeted-tests.txt) |
| 工程，最终源码 | 相关 Application/Runtime 测试 **race 通过**；全包 **vet 通过**；bridge TS、web typecheck/Vite、Go 开发构建通过；JS/Python 脚本语法检查通过 | [定向 race](W02-06-oauth-targeted-go-race.txt)、[vet](W02-06-oauth-go-vet.txt)、[构建](W02-06-oauth-build.txt)、[认证诊断断言](W02-06-oauth-bridge-auth-tests.txt) |
| 受影响生产回归 | HTTP 头认证 **43/43**；资源升级认证失败后回退 **6/6** | [头认证](W02-06-oauth-header-regression.json)、[回退](W02-06-oauth-rollback-regression.json) |

各矩阵有重叠，不累加成不同验收条件数。最终完整构建与定向 race 在受限环境中执行；此前全量 race 发生在新增准备模块之前，不将其标作最终模块的全量验证。历史浏览器截图的文案和控件属于当时版本，不证明最新 UI。最新 HTTP 64 项之后的产品改动仅为长命令 CSS 换行；后续还补充了测试和说明，manifest 记录归档时点。

自有临时认证状态文件 `.dev/w0206-auth/browser-auth.json` 已删除。扫描此次日志所列临时目录，没有匹配的存活进程，原 browser fixture PID 也不存在；此次续接未停止任何其他进程，见[收尾](W02-06-oauth-process-cleanup.json)。

## 重跑与剩余

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache make claude-build dev-build
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test ./internal/app ./internal/runtime -run 'TestMCPLogin|TestClaude.*MCP' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/runtime -run 'TestMCPLogin|TestClaude.*MCP|Test.*Execution|Test.*Capability|Test.*Command' -count=1 -timeout=120s
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
node tests/claude_mcp_auth_test.mjs
python3 tests/claude_mcp_oauth_probe.py
python3 tests/claude_oauth_mcp_smoke.py --transport http
python3 tests/claude_oauth_mcp_smoke.py --transport sse
```

浏览器在允许本地 socket 和固定浏览器正常运行的环境中，启动 `tests/claude_oauth_mcp_smoke.py --hold --info-file <自有文件>`，使用自有本地令牌登录固定 Playwright CLI 的命名浏览器，运行 `tests/claude_oauth_mcp_browser.js`。最新脚本含准备、复制、无原生 Operation、刷新找回和宽窄布局断言，当前只完成语法检查。

- 优先补齐新增准备 UI 和 SSE 完整准备流程的实际验证；当前限制来自工具环境，不代表产品检查通过或失败。
- 认证失败退出后的原 query 尚无透明热恢复；已验证重新认证后重启自有控制台并恢复原 ID。原生 logout 的隔离由原生实验覆盖，工作台没有新增撤销凭据按钮。
- 动态认证 helper、额外 `oauth` 配置、受管/插件/账号来源与完整权限冲突仍待实施；原生 `setMcpServers` 动态重加的环境展开差异仍保留。原生政策继续传入不等于完整冲突矩阵已经验证。
- 任意历史资源版本回退、漂移来源的独立恢复、`recorded_sources_only`、独立前台/worker 结果、Skill/CLI 插件、Windows 与真实角色协作等既有硬条件保持。
