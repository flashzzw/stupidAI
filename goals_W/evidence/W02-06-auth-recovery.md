# W02.06 · OAuth 启动失败后的原 ID 重试

日期：2026-10-01。延续 [OAuth 认证准备](W02-06-oauth.md)，修复已有会话重新认证后仍被旧 `SDK initialization failed` 阻断的问题。**生产恢复逻辑和受控生命周期测试已完成；真实 CLI 的不重启恢复验收受环境阻断。** W02.06 保持 in_progress，done 7/40，Goal active。

## 复现与实现

通过生产 `Session.send/open/snapshot` 建立反馈，SDK 传输使用可控替身，不调用模型或真实 OAuth。已有初始化与已完成输入的原会话在恢复时收到 `needs-auth`，新输入未入日志；query 关闭后模拟认证完成，再对同一 Session 发送。原实现仍抛出 `NotSent: SDK initialization failed`，没有创建第二个 query。[原始失败](W02-06-auth-recovery-red.txt)中 1 项失败、2 项保护检查通过，复现命令为 `node --experimental-test-module-mocks tests/claude_mcp_auth_recovery_test.mjs`。

原因是初始化 catch 永久保留 `fault`；即使旧 query 已退出，后续 open 在启动前仍被该标记拒绝。修复新增内部 `MCPAuthenticationRequired` 类型，保持 wire 上的 NotSent 语义。只有已有原生历史和初始化记录、非候选改配、无未完成输入/队列/后台任务/权限请求且没有其他错误时，才能释放本次认证失败。

释放之前关闭此次自有 query，等待事件读取器与子进程均退出；等待期间 fault 保持，关闭期间到达的后台占用再次核对。全部通过后清除本次初始化错误、更新连接标识并丢弃已过期权限句柄。被拒的输入从未入日志，不自动重放；重新认证后的下一次正常输入沿原 ID、cwd、配置和单次 ask 规则重新启动。首次创建、其他初始化错误、未完成工作及配置事务仍走原来的阻断/回退路径。

`hasUnfinishedWork` 提取自原改配空闲条件，两处复用同一组约束。此片没有修改 Application 的输入权、原生状态投影、快照/配置版本或共享后台控制。

## 验证范围

固定 Node **24.13.0**、SDK **0.3.280**、Claude **2.1.280**、Go **1.26.2**。通过 `find-docs`/Context7 核对 Node 24 的 [mock.module](https://nodejs.org/docs/latest-v24.x/api/test.html) 文档；受控 SDK 在测试进程中替换，生产依赖与入口保持原样。子进程退出测试运行自有本地 Node 子进程，不使用 socket、真实供应商或共享进程。

| 层次 | 实际结果 | 证据 |
|---|---|---|
| Session 生命周期 | **10/10**：同 Session 原 ID 重试/不重放；其他错误与未完成输入阻断；读取器和真实自有子进程退出等待；已有/关闭期间后台任务和权限请求阻断；首次创建不晋升；sibling query/审批保持；候选认证失败维持原绑定和 not_sent | [最终测试](W02-06-auth-recovery-contracts.txt) |
| MCP 诊断与来源回归 | OAuth 诊断/未选服务/已认证选择通过；**7 项**继承与动态来源核对通过 | [诊断](W02-06-auth-recovery-contracts.txt)、[来源](W02-06-auth-recovery-configuration.txt) |
| Go 原控制路径 | Application/Runtime 中 Claude、MCPLogin、Handoff、Resume 定向 **race 通过** | [Go 回归](W02-06-auth-recovery-go-race.txt) |
| 工程 | bridge TypeScript、web typecheck/Vite、Go 开发构建、全包 vet 和 Python 脚本语法检查通过 | [bridge](W02-06-auth-recovery-contracts.txt)、[开发构建](W02-06-auth-recovery-build.txt)、[vet](W02-06-auth-recovery-go-vet.txt) |
| 真实 CLI 不重启恢复 | **受阻，未执行完成**：入口创建第一个 loopback 供应商 socket 即报 `PermissionError: [Errno 1] Operation not permitted`，尚未到达 CLI | [原始受阻日志](W02-06-auth-recovery-native-blocked.txt) |
| 浏览器、SSE 完整准备 | 本片未重跑，保持前片未验证状态 | [前片限制](W02-06-oauth.md) |

这 10 项是生产 Session 与受控 SDK 传输的生命周期证据，不能代替真实 OAuth、Go/HTTP 全链路或界面验收。原生回归入口新增 `--recover-without-restart`：重新认证后保留原控制台 PID、B 的 query PID，再核对 A 原 ID 调用和 B 后续调用；入口已完成语法检查，但这些断言尚未实际通过。没有把旧 HTTP 64 项当作新路径的通过证据。

源码、构建与附件 SHA256 见 [manifest](W02-06-auth-recovery-manifest.json)。使用 `.dev/bin/netcap` 和自有临时目录；未提交、调用真实供应商、停止共享后台或更改浏览器配置。

## 复现与下一步

```bash
make claude-auth-contracts
node tests/claude_mcp_configuration_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/runtime -run 'TestClaude|TestMCPLogin|Test.*Handoff|Test.*Resume' -count=1 -timeout=120s
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 tests/claude_oauth_mcp_smoke.py --transport http --recover-without-restart
python3 tests/claude_oauth_mcp_smoke.py --transport sse --recover-without-restart
```

条件可用时优先验证两协议不重启恢复、SSE 生产认证准备和新增准备 UI。动态认证 helper、完整来源/权限冲突与其余 W 系列硬要求保持。原生完整验收之前，使用说明保留已经验证的“重新认证后重启自有控制台、按原 ID 恢复”路径。
