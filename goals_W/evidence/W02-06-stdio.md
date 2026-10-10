# W02.06 · Claude stdio MCP 启动选择与隔离

日期：2026-10-01（Asia/Shanghai）。W02.06 **in_progress**：本片完成 stdio 启动选择、单次权限、实际工具清单与原 ID 恢复；HTTP/SSE、已有会话版本改配、完整来源与权限冲突仍未完成。累计 done 保持 7/40，整条 Goal active。

## 交付行为

- 普通 Claude 新会话可选择能力库的共享/专属 MCP 定义；资产自动进入不可变引用，同一资产可展开多个服务。共最多 16 个服务，跨所选资产重名拒绝。来源摘要漂移阻止后续输入。
- bridge 仅在启动 query 时注入所选 stdio 定义，保留 user/project/local settings 和其他 MCP。环境占位符由原生 CLI 展开，资源相对路径相对定义文件，进程 cwd 保持项目目录。配置仅支持 type/command/args/env，未知字段不静默忽略。
- 实际清单只返回 name/status/source/tools。enabled 必须唯一同名项为 dynamic + connected；disabled 必须完整清单中缺席，或明确 disabled 且无工具。同名继承仍连接、失败或未知时不确认选择；输入前再核对。
- 所选 enabled MCP 每次调用经过原生 ask 规则；允许/拒绝复用原 connection/request ID 和持久 Operation。重复回复幂等。成功调用来自配对的原生 tool_use/tool_result，拒绝/失败不计成功，离线恢复保留历史成功记录但不声称当前已连接。
- 快照和 bridge metadata 只保存来源引用，不复制命令及展开后的 MCP 凭据。使用说明见 [CONSOLE](../../CONSOLE.md#claude-原生-mcp-启动选择)。

## 实际验证

运行环境为 Linux/WSL，固定 Claude Code **2.1.280**、Agent SDK **0.3.280**；开发二进制 `.dev/bin/netcap`，Node 和构建/源码哈希见 [manifest](W02-06-stdio-manifest.json)。所有 native/HTTP 验收使用 `/tmp/netcap-*` 内独立项目、profile、custom settings、控制台状态、自有 MCP 进程与本地合成供应商。没有真实供应商调用、共享后台停止、用户数据迁移或对外发布。

| 层次 | 实际结果 | 证据 |
|---|---|---|
| Go 准入/投影 | 定向测试通过；多服务引用、同名/类型/字段拒绝、清单准入、成功结果配对 | [定向日志](W02-06-stdio-targeted-tests.txt) |
| 全量 Go | `go test -race ./...`、`go vet ./...` 通过 | [race](W02-06-stdio-go-race.txt)、[vet](W02-06-stdio-go-vet.txt) |
| 构建 | bridge TypeScript、web typecheck/Vite、Go 开发二进制通过 | [bridge](W02-06-stdio-bridge-build.txt)、[开发构建](W02-06-stdio-build.txt) |
| 真实 Claude + 生产 HTTP | **39/39**；A/B 请求实际并发，cwd/MCP 凭据/模型认证/历史隔离、结果回到各自模型；单次 allow、deny、重启原 ID、禁用、来源漂移与连接失败阻断 | [生产结果](W02-06-stdio-native.json) |
| 浏览器 | **21/21**；默认继承、自动引用、实际清单、允许后再次审批与拒绝、重载快照、能力范围、1440/768 布局；无 JS pageerror | [浏览器结果](W02-06-stdio-browser.json)、[审批](W02-06-stdio-approval.png)、[1440](W02-06-stdio-1440.png)、[768](W02-06-stdio-768.png) |
| Claude 原路径回归 | 普通三轮/原 ID 恢复；Bash allow/deny、问题回答、后台占用、interrupt、控制台 crash 不盲发通过 | [普通恢复](W02-06-stdio-claude-console.txt)、[交互](W02-06-stdio-claude-interaction.txt) |
| Codex 回归 | **28/28** MCP 选择/单次审批/恢复/故障检查通过 | [结果](W02-06-stdio-codex-regression.json) |

生产测试还扫描了控制台 SQLite、JSON/JSONL 与公开快照，未发现合成 MCP key 的实际值。该结论不表示会自动清理资产文件里的明文密钥，或对工具主动返回的敏感内容进行通用脱敏。

## 本片发现和修复

首次真实 MCP 调用已执行成功，但 Go 观察报 `cannot unmarshal array ... tool_use_result ... struct`，使会话进入 unknown。旧 Adapter 将所有工具结果都当作 Bash 元数据对象。最小 JSON 回放在修复前重现同一错误：[red 日志](W02-06-stdio-array-result-red.txt)。现在保留工具结果的原始 JSON，只为 Bash 读取对象形式的后台/中断标记；MCP 数组结果可以完整投影。定向回归、原始 A/B 生产场景和原有 Bash/后台交互均通过，未把 unknown 条件放宽。

原生控制实验：[结果](W02-06-stdio-control-probe.json)。启动 mcpServers 使用 custom settings.env 的占位值正确，项目 MCP 和 CLAUDE.md 保留；toggle 只停所选项。但 `setMcpServers` 重加同一定义后，服务虽 connected，MCP key 校验变为 false。因此本片没有接入动态重加路径。实验对初始化前已有配置检查为未变化；未据此证明新建 `.claude.json` 无 toggle 持久副作用。

## 重跑入口与剩余条件

```bash
make claude-build dev-build
go test ./internal/app ./internal/runtime -run TestClaudeMCP -count=1
python3 tests/claude_mcp_selection_smoke.py
python3 tests/claude_mcp_control_probe.py
python3 tests/claude_interaction_smoke.py --netcap "$PWD/.dev/bin/netcap"
python3 tests/claude_console_smoke.py --netcap "$PWD/.dev/bin/netcap"
python3 tests/native_mcp_selection_smoke.py
go test -race ./...
go vet ./...
```

浏览器使用 `tests/claude_mcp_selection_smoke.py --hold --info-file <隔离文件>`，以该 fixture 的令牌登录后，通过固定 `/home/zzw/.local/bin/playwright-cli` 运行 `tests/claude_mcp_selection_browser.js`。截图写入 `.dev/w0206`，只关闭本次命名浏览器与 fixture。此次自有进程已退出。

- HTTP/SSE 认证、oauth/URL elicitation、已有会话改配尚未实现或验证。动态重加已有凭据展开失败证据，下一片需先解决该边界，或采用有版本事务的关闭/原 ID 恢复方式。
- 同名项目继承冲突拒绝已验证；任意插件、受管配置、权限叠加冲突和完整有效配置仍归 W02.06 后片/W02.09。当前覆盖保持 `recorded_sources_only`。
- 清单不可读的准入有 Go 状态测试，未注入真实 SDK status RPC 不可读故障；多服务展开有 Go 测试，完整多资产 native 组合仍待补。connected 仅证明原生连接就绪，不证明任意服务端每个工具都拥有正确授权。
- 原生工具工作进程故障、独立前台接管和 Windows 宿主生命周期不属于此验证结果，W01/W04/W07 原缺口保留。

原始生产记录：`/tmp/netcap-console-a8ycie6g`；浏览器：`/tmp/netcap-console-pavi1on5`；Claude 交互/恢复：`/tmp/netcap-console-h9vdftjt`、`/tmp/netcap-console-8taf7298`；Codex：`/tmp/netcap-console-emth_ly5`。首次失败：`/tmp/netcap-console-8gau_22b`。临时目录可被清理，仓库内脱敏结果用于长期复核。
