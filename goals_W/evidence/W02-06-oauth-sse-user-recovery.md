# W02.06 · 用户终端 SSE OAuth 原 ID 不重启重试

2026-10-02。用户在独立 WSL 终端运行 `python3 tests/claude_oauth_mcp_smoke.py --transport sse --recover-without-restart`，输出目录 `/tmp/netcap-console-aj0iwevb`。代理直接读取文件并复核；不是代理环境内重跑。固定 Claude 2.1.280，真实 CLI/生产控制台，隔离本地模拟供应商与 SSE OAuth/MCP 服务。

[结果](W02-06-oauth-sse-user-recovery.json)71 项全部通过，检查项与 [HTTP 分支](W02-06-oauth-http-user-recovery.md)一致，分别执行留证。覆盖准备幂等、单服务目录与原配置/快照绑定、A/B 登录隔离、单次审批及拒绝、并发轮次、刷新、撤销阻止输入、原 ID 重新认证重试及另一会话继续调用。实际获准调用 7 次（A 3/B 4），两次拒绝没有新增调用；两配置都有刷新，授权次数 A 2/B 1。

[落盘复核](W02-06-oauth-sse-user-recovery-verification.json)：两个 UUID 不同，原生日志输入为 A 4/B 5；模拟供应商请求认证匹配，全部可识别 OAuth 工具结果均匹配本 profile、workspace 与 sse。历史中的结果出现次数不是调用次数。仅归档脱敏投影及请求/日志哈希，不复制凭据或完整请求正文。版本见[记录](W02-06-oauth-sse-user-recovery-versions.json)。

“不重启”仅限重新认证重试阶段；前面刷新/撤销故障阶段有自有控制台重启。控制台存活 PID 与 sibling query 保持来自运行时断言，没有独立前后 PID 文件，不外推全程不中断。prepared_command_survives_restart 只检查最终计划数量；令牌排除只覆盖当时快照/A runtime 元数据与已知 fixture token。完整准备/来源界面的浏览器集成、所有文件凭据排除仍不能由这些断言证明。

HTTP 和 SSE 的原生 OAuth 准备/认证/刷新/撤销及原 ID 不重启重试现均有本地服务验收证据，补齐此前环境受阻部分。历史失败保持原样；W02.06 in_progress，8/40 done。动态认证头完整请求、helper/OAuth 目录组合、实际权限冲突及其他 W01/W04 硬要求不因本次通过而关闭。

下一项在同一用户终端分别运行：

```bash
python3 tests/claude_remote_mcp_smoke.py --transport http --headers-helper
python3 tests/claude_remote_mcp_smoke.py --transport sse --headers-helper
```

既有脚本使用本地模拟服务、隔离配置及 `.dev/bin/netcap`，检查错误静态认证头下实际 helper 认证、失效认证阻止输入、A/B 目录绑定及原 ID 恢复；本轮未运行。首条如失败，先处理其输出再扩大验证。

本轮仅归档与进度更新，未改产品代码、构建，未重跑 Go/浏览器测试，未连接或停止用户测试进程/共享后台。旧证据保持不变；归档时源码/二进制哈希见 [manifest](W02-06-oauth-sse-user-recovery-manifest.json)，不冒充用户启动瞬间哈希；进度变化见 [diff](W02-06-oauth-sse-user-recovery-diff.patch)。
