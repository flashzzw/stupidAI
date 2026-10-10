# W02.06 · HTTP/SSE 认证与原生清单故障

日期：2026-10-01（Asia/Shanghai）。在 [stdio 首片](W02-06-stdio.md)上扩展 HTTP/SSE 启动与原 ID 恢复，补齐真实 SDK 清单 RPC 不可读的故障注入。W02.06 保持 **in_progress**，累计 done 仍为 7/40，Goal active。

## 交付

能力库 MCP 定义可使用 `type: "http"` 或 `"sse"`、HTTP/HTTPS `url` 和字符串映射 `headers`。Go 在创建前验证协议、字段、URL 和头值；bridge 使用启动 `mcpServers`，把 `${ENV_NAME}` 留给固定原生 CLI 展开。stdio 与远程字段不能混用，未知字段不静默丢弃。快照仍只记录资产、服务名、模式和来源路径，没有 schema 升级、命令/认证头复制或凭据存储新增。

所选启用项必须实际 connected 且来源为 dynamic，禁用必须缺席或 disabled；其他项目 MCP 和 CLAUDE.md 保留。远程调用同样逐次审批，拒绝不调用、不计历史成功；控制台重启保持原 ID、原快照、原账号和环境绑定。清单 RPC 返回错误时保留 unknown，不接受输入；恢复清单后重新核对原连接，调用仍需单次审批。

远程服务不存在本地子进程 cwd。验收分别核对 Claude 的项目/状态目录，以及显式请求头里的工作区绑定，不将后者宣称为远程服务器进程目录。使用说明见 [CONSOLE](../../CONSOLE.md#claude-原生-mcp-启动选择)。

## 官方与原生依据

- [官方 Agent SDK MCP 文档](https://code.claude.com/docs/en/agent-sdk/mcp)：HTTP/SSE 的 `url`、`headers` 配置，以及 `.mcp.json` 的 `${VAR_NAME}` 运行时展开。通过 Context7 查询；固定本地 SDK **0.3.280** 的 `sdk.d.ts` 另行核对两种配置类型、状态枚举与 control response。
- [四组合原生实验](W02-06-remote-probe.json)：HTTP/SSE × A/B，真实 Claude **2.1.280** 与 SDK；6/6 汇总检查，四次实际工具调用、header 环境展开、工作区绑定、模型认证和权限回调通过。
- 沿用 stdio 首片的 `setMcpServers` 动态重加环境差异结论，本片不调用动态重加，不把启动认证通过外推为热改配成立。

## 验证与隔离

开发二进制 `.dev/bin/netcap`；Node 24.13.0、Go 1.26.2。CLI、项目、profile、custom settings、访问令牌、服务端和状态均位于自有 `/tmp/netcap-*`。MCP 使用 Python 标准库实现的真实 loopback Streamable HTTP JSON 响应与 SSE 通道；模型是本地合成供应商。没有真实外部账号、供应商请求、共享后台切换、用户迁移、提交或发布。构建与源码归档哈希见 [manifest](W02-06-remote-manifest.json)，旧首片哈希保持不变。

| 验证层次 | 结果 | 证据 |
|---|---|---|
| Go 协议边界 | 新旧 MCP 定向测试通过：远程双协议引用、无效 scheme/缺 type/混用字段/错误 headers/换行/未知 oauth 拒绝 | [定向](W02-06-remote-targeted-tests.txt) |
| 完整工程检查 | `go test -race ./...`、`go vet ./...`、bridge TS、web typecheck/Vite、Go 开发构建通过 | [race](W02-06-remote-go-race.txt)、[vet](W02-06-remote-go-vet.txt)、[bridge](W02-06-remote-bridge-build.txt)、[构建](W02-06-remote-build.txt) |
| 生产 HTTP MCP | **43/43**，A/B 实际并发与认证、工具结果、单次权限、拒绝/禁用、继承、原 ID 重启、来源漂移和失效认证阻断 | [HTTP](W02-06-remote-http.json) |
| 生产 SSE MCP | **43/43**，同一矩阵；SSE GET 与后续 POST 均经过账号认证 | [SSE](W02-06-remote-sse.json) |
| 真实 SDK status RPC 故障 | **7/7**，未知/清单不误报、输入不发往模型、不调用工具、清单恢复后原 ID/绑定/逐次权限保持 | [故障](W02-06-remote-status-fault.json) |
| stdio 原路径 | **39/39** 回归通过 | [stdio](W02-06-remote-stdio-regression.json) |
| 浏览器 | HTTP **21/21**、SSE **21/21**、认证失败 **5/5**；1440/768 布局与页面错误检查通过 | [HTTP](W02-06-remote-http-browser.json)、[SSE](W02-06-remote-sse-browser.json)、[认证失败](W02-06-remote-auth-failure-browser.json) |

RPC 故障通过自有 CLI NDJSON proxy 仅将 `mcp_status` 请求返回 control error。实际 SDK 消费该错误，真实 CLI/query/MCP 继续运行；不是把 Go View 直接替换成模拟状态。去掉故障 marker 后，原查询上的调用完成。故障代理仅存在于 fixture，没有进入生产 bridge。

所有成功远程调用通过服务端认证。失败 fixture 返回 403 且不提供 OAuth discovery，观察到无效凭据后生产不会发送模型输入或调用工具。公开快照、控制台 SQLite、bridge metadata/journal 未出现合成 MCP key 值；只对这些样本成立，不声称会脱敏工具主动返回的任意敏感内容。

## 浏览器发现与修复

HTTP 初次脚本在流式草稿与最终 SDK 消息短暂并存时触发 strict locator 错误。轮次结束后只剩一条结果；验收脚本改为等待空闲，再确认唯一完成消息。没有放宽为任取第一条，也没有改生产流式行为。

认证失败样本的实际 `closed`、`archived=false` 曾被 UI 标为“已归档”。现在 `closed` 显示“已关闭”，归档仍由独立标记表达。失败页显示未取得 native ID、加载未确认与具体未发送原因，填入非空消息后发送仍禁用。新增浏览器断言和最终截图确认此修复。失败前 [截图](W02-06-remote-closed-label-before.png)，修复后 [截图](W02-06-remote-auth-failure.png)。

其他截图：[HTTP 审批](W02-06-remote-http-approval.png)、[HTTP 1440](W02-06-remote-http-1440.png)、[HTTP 768](W02-06-remote-http-768.png)、[SSE 审批](W02-06-remote-sse-approval.png)、[SSE 768](W02-06-remote-sse-768.png)。本次自有浏览器和 fixture 已关闭。

## 重跑

```bash
make claude-build dev-build
go test ./internal/app ./internal/runtime -run 'TestClaude.*MCP' -count=1
python3 tests/claude_remote_mcp_probe.py
python3 tests/claude_remote_mcp_smoke.py --transport http
python3 tests/claude_remote_mcp_smoke.py --transport sse
python3 tests/claude_mcp_status_fault_smoke.py
python3 tests/claude_mcp_selection_smoke.py
go test -race ./...
go vet ./...
```

浏览器分别启动 `tests/claude_remote_mcp_smoke.py --transport http|sse --hold --info-file <自有文件>`，用该 fixture 的本地令牌登录，通过固定 `/home/zzw/.local/bin/playwright-cli` 运行 `tests/claude_mcp_selection_browser.js`。HTTP 额外运行 `tests/claude_remote_mcp_failure_browser.js`。脚本截图路径可在运行副本中指向独立目录，不改浏览器全局配置。

原始 HTTP：`/tmp/netcap-console-zfr05htv`；SSE：`/tmp/netcap-console-qyktj9r6`；stdio：`/tmp/netcap-console-pidq48th`；RPC 故障：`/tmp/netcap-console-exlwjgaf`；四组合原生实验：`/tmp/netcap-claude-remote-mcp-nf8wpo6_`。浏览器目录见归档 manifest。临时目录可清理，仓库结果保留长期复核依据。

## 仍未完成

- 已有会话的版本事务、原 ID 应用/回滚和同名继承来源的完整处理。普通重连只恢复原选择，不应用新版本。
- OAuth/浏览器登录、动态 header helper、完整权限/插件/受管来源冲突；本片仅核对预配置头认证。未测试真实远程服务、TLS 故障或外部账号。
- 多资产、多服务与混合 transport 的完整 native 组合矩阵；本片有每协议正反例和 Go 多服务展开验证，不能据此声称全部组合已验收。
- `coverage` 继续为 `recorded_sources_only`；W01 前台/worker、W03 用户试用、Windows 与后续协作硬条件保持原样。
