# W02.06 · stdio 初始化写入拒绝与环境诊断

记录日期：2026-10-02。此片完成本轮提前 EOF 的直接故障点定位、工作台环境诊断及展示；原生 MCP 连接仍失败，W02.06 保持 in_progress，累计 8/40 done，Goal active。

## 直接证据

固定 Claude Code 2.1.280、Agent SDK 0.3.280、Node 24.13.0。原始[基线](W02-06-stdio-startup-baseline.json)与最终[无观测器对照](W02-06-stdio-startup-final-comparison.json)均退出 1：相同 Python 服务在 Node 子进程中收到 initialize，在原生 CLI 下只收到 EOF；两者 stdin 都是阻塞匿名 Unix socket。没有提交模型输入，模型结果数为 0。

新增可选的被动 libc 观测器原样调用 `send`/`sendto`，不重试、不替代系统调用、不修改返回值或 errno，只记录 PID、fd、socket 标记、字节数、是否包含固定 initialize 方法名及返回结果。它在隔离实验中捕获到：

```json
{"pid":18,"call":"send","fd":21,"socket":1,"bytes":324,"initialize":1,"result":-1,"errno":1}
```

`errno=1` 为 `EPERM`。该次[观测结果](W02-06-stdio-startup-observed.json)仍有 `native_connected=false`、`native_eof_before_initialize=true`，退出 1。[原生日志片段](W02-06-stdio-startup-native-debug-excerpt.txt)记录默认 MCP v2 在 38ms 后 CONNECTION_CLOSED。故障发生在本地 initialize 写入，不能继续用认证重试解释该症状；未查明环境中具体哪条规则产生拒绝，也未在本片修改环境规则。

其他隔离对照：

- [自有 socketpair](W02-06-stdio-startup-socketpair-writes.json)：`os.write`、`sendmsg` 各写入/读取 1 字节成功，`send` 返回 EPERM。这里只比较操作；生产代码没有切换到其他写法。
- [原生 v1/v2](W02-06-stdio-startup-generation.json)都在 initialize 前 EOF；`MCP_SDK_GENERATION` 仅是本地二进制中发现的实验开关，未取得公开契约，未放入生产。
- 自有子进程 `PTRACE_TRACEME` 返回 `Operation not permitted`，故用被动 libc 元数据观测。
- [Bun CLI 模式尝试](W02-06-stdio-startup-bun-node.json)没有切换本机打包 Claude，仍运行 Claude 并在隔离未登录环境退出；不能将其作为 Bun 对照通过。官方 Bun 通用说明不能替代此二进制实测。

## 用户可见变化与边界

`Claude.Describe` 在固定版本校验通过后检查本工作台进程的匿名 Unix socket 写入。检查创建自有 socketpair、发送 1 字节并关闭 fd，不监听、不启动 MCP 或 query。`EPERM/EACCES` 显示环境限制，取消及其他错误显示未验证；不输出未知错误正文。版本未知/不可用时不生成此传输诊断。

现有 `Description.local_ipc` 可选字段经 Application 与 `/api/workspace`、`/api/execution-profiles/diagnose` 投影到“执行配置”的详情，包含操作、错误码、时间与说明。“重新诊断配置”重新采样；版本变更为未知时清除旧结果；旧响应无字段时不补造。

检查通过只说明工作台进程的该操作成功。CLI 包装器和子进程可能有不同限制，服务连接、认证、工具权限和输入准入继续由原有证据判断；本片不改变 capabilities 或授权。生产检查使用 Go 的 socket send 操作，精确原生故障来自上述 libc 实验，两者证据不混同。

## 验证结果

| 层次 | 实际结果 | 记录 |
|---|---|---|
| Runtime 定向 | 5 个结果分类 + 2 个版本子场景通过；固定真实 CLI Describe 返回 blocked/send/EPERM，未创建 query | [日志](W02-06-stdio-startup-diagnostic-tests.txt)、[Description](W02-06-stdio-startup-native-description.json) |
| Application / HTTP Handler | 鉴权、真实诊断字段投影、重新采样、能力不变、未知版本清旧证据、无会话/操作通过；另以固定真实 CLI 重跑并导出响应 | [默认夹具](W02-06-stdio-startup-http-tests.txt)、[原生版本运行](W02-06-stdio-startup-native-http-tests.txt)、[响应](W02-06-stdio-startup-http.json) |
| 浏览器 | 当前构建与上述 Handler 响应的 19 项检查通过；1440/768/390 无横向溢出，截图人工核对可读；重新诊断请求为空对象，未发送执行命令，无 JS 错误 | [结果](W02-06-stdio-startup-browser.json)、[脚本](W02-06-stdio-startup-browser-script.js)、[1440](W02-06-stdio-startup-browser-1440.png)、[768](W02-06-stdio-startup-browser-768.png)、[390](W02-06-stdio-startup-browser-390.png) |
| Runtime 回归 | race 62 个顶层、240 个子测试通过；排除需要监听的既有 OpenCode 测试；原生 opt-in 测试未在此全包命令中运行 | [日志](W02-06-stdio-startup-runtime-race.txt) |
| 应用/控制台回归 | app、console race 通过 | [日志](W02-06-stdio-startup-app-console-race.txt) |
| 静态/构建 | `go vet -tags=nativecodex ./...`、`make dev-build` 通过；包含 Web typecheck、Vite 与 Go 构建 | [vet](W02-06-stdio-startup-vet.txt)、[构建](W02-06-stdio-startup-build.txt) |
| 原生连接复验 | 基线、被动观测、最终无观测器均退出 1，真实连接断言失败；Node 对照通过 | [最终输出](W02-06-stdio-startup-final-comparison.txt) |

浏览器 HTTP/EventSource 使用受控传输，没有启动 HTTP listener。blocked 数据来自本轮真实版本/环境检查；passed、unverified 和缺字段是明确的展示变体，不能作为环境已修复或真实服务已连接的证据。该片未验收 OAuth 准备新控件或“原生来源”旧片的新界面。

## 重跑

从仓库根目录执行；所有路径指向自有证据或隔离临时目录：

```bash
export GOCACHE=/tmp/netcap-w0206-auth-go-cache
python3 tests/claude_mcp_stdio_diagnosis.py --output /tmp/netcap-stdio-comparison.json
python3 tests/claude_mcp_stdio_diagnosis.py --observe-send --output /tmp/netcap-stdio-observed.json
NETCAP_NATIVE_CLAUDE_IPC_TEST=/home/zzw/.local/share/claude/versions/2.1.280 \
  go test -race ./internal/runtime -run 'TestLocalIPCResult|TestClaudeDescribeReports|TestClaudeNativeLocalIPCDescription' -count=1 -v
go test -race ./internal/console -run TestClaudeLocalIPCDiagnosticHTTP -count=1 -v
NETCAP_NATIVE_CLAUDE_IPC_TEST=/home/zzw/.local/share/claude/versions/2.1.280 \
  NETCAP_CLAUDE_IPC_HTTP_OUTPUT=/tmp/netcap-ipc-http.json \
  go test -race ./internal/console -run TestClaudeLocalIPCDiagnosticHTTP -count=1 -v
go test -race ./internal/runtime -skip '^TestOpenCode' -count=1
go test -race ./internal/app ./internal/console -count=1
go vet -tags=nativecodex ./...
make dev-build
```

`--observe-send` 仅适用于有 `cc` 的 Linux/glibc 环境；编译的 `.so` 与 `LD_PRELOAD` 仅用于该次隔离探测。实际 MCP 连接失败仍退出非零。浏览器脚本通过配置的 Playwright MCP `browser_run_code_unsafe` 运行，使用归档 Handler 响应及本片构建资源；重跑前需核对资源哈希，脚本截图输出在 `.dev/w0206-stdio-startup/`，不覆盖正式证据。

## 来源、清理与下一步

官方文档通过 Context7 查询，见[摘录](W02-06-stdio-startup-docs.json)：[Agent SDK MCP](https://code.claude.com/docs/en/agent-sdk/mcp)、[troubleshooting](https://code.claude.com/docs/en/agent-sdk/troubleshooting)、[Bun standalone](https://github.com/oven-sh/bun/blob/main/docs/bundler/executables.mdx)。安装的 SDK 0.3.280 `sdk.d.ts` 2210/2215/2220 行另确认 `debug`、`debugFile`、`stderr`；本片只在测试探测支持可选 debugFile。未归档完整原生 debug 日志，仅保留无凭据的定向连接片段。

最终[差异](W02-06-stdio-startup-diff.patch)与[manifest](W02-06-stdio-startup-manifest.json)记录归档时源码、构建和证据哈希。[清理核对](W02-06-stdio-startup-cleanup.json)：639 个既有证据与 `bin/netcap` 未变，当前工具 PID 命名空间内未见自有探测进程；该扫描不证明共享后台完整生命周期。未停止共享后台，未调用真实供应商、未提交或发布。

恢复原生验收需要运行环境允许 CLI 所需的匿名 socket `send`。条件变化后先跑最小对照，成功后补 OAuth/SSE、原 ID 认证重试、动态头实际请求和完整来源/权限冲突矩阵。条件未变化时可独立推进准备与来源诊断的 UI 验证；W02.07/08 的前置仍未完成。W01 的生产 proxy/peer、原权限与输入独占、断电及 Windows 条件仍保留。
