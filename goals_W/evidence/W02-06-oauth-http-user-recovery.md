# W02.06 · 用户终端 HTTP OAuth 原 ID 不重启重试

2026-10-02。用户执行 `python3 tests/claude_oauth_mcp_smoke.py --transport http --recover-without-restart`，输出目录 `/tmp/netcap-console-epr0j26a`。代理读取结果与原始记录；没有在受限代理环境重跑。固定 Claude 2.1.280，使用已有 `.dev/bin/netcap`、真实原生 CLI、生产控制台和隔离本地模拟供应商/OAuth/MCP 服务。

## 通过范围

[结果](W02-06-oauth-http-user-recovery.json)共 71 项检查全部通过：未认证时阻止模型输入、准备命令幂等与单服务/配置绑定、A/B 认证隔离、并发轮次、单次审批/重复答复、拒绝工具、过期刷新、撤销后 not_sent、原 ID 与执行快照保留、重新认证后 A 重试及 B 后续调用。

工具实际调用共 7 次（A 3 次、B 4 次），另有 A/B 各一次拒绝；拒绝不计作实际调用。每次实际调用绑定对应 workspace/HTTP/OAuth。授权事件 A 两次、B 一次，两份配置都有刷新事件。

“不重启”仅指撤销后重新认证与重试阶段：脚本保存控制台 PID、B 最新 query PID，A 重新认证/原 ID 重试后核对它们保持，再执行 B5。前面的刷新/撤销故障阶段显式重启过自有控制台，不能描述成整个测试从未重启。该结果补齐 [认证恢复片](W02-06-auth-recovery.md)的 HTTP 原生缺口。

## 落盘复核与证据限度

代理的[复核](W02-06-oauth-http-user-recovery-verification.json)核对两个原生 UUID 不同，运行日志各有 4/5 条输入；直接读取本地供应商请求，认证匹配均为真，所有可识别的 OAuth 工具身份结果均归属对应配置/workspace，A/B 均有结果返回。历史重复携带的结果不计成新工具调用。原始请求/日志仅保存哈希及脱敏投影，不复制认证文件或完整请求正文。

控制台存活 PID 一致与 sibling query 不变由运行时断言报告，目录中没有单独的前后 PID 对照文件；归档不夸大为代理重放证明。令牌未泄露检查仅覆盖脚本当时检查的工作台快照及 A runtime 元数据，不能外推所有日志或历史令牌。prepared_command_survives_restart 检查最终登录计划数量，不单独证明重新认证后再重启；此前准备回执持久性证据仍保留。

## 下一步与收尾

下一项为 `python3 tests/claude_oauth_mcp_smoke.py --transport sse --recover-without-restart`，在同一具备前置的用户终端执行；本轮未运行。HTTP 成功不替代 SSE 完整准备/认证/恢复。动态头完整请求、权限冲突矩阵、其他 W01/W04 硬条件仍保留，W02.06 in_progress，8/40 done。

本轮仅归档和更新进度，未修改产品源码或构建，未运行 Go/浏览器测试；旧证据原样保留。源码与二进制的归档时点哈希见 [manifest](W02-06-oauth-http-user-recovery-manifest.json)，不是用户启动瞬间的完整哈希证明；变更见 [进度 diff](W02-06-oauth-http-user-recovery-diff.patch)。未操作用户测试进程、共享后台或真实供应商。
