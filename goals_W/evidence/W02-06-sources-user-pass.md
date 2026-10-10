# W02.06 · 用户 WSL 终端来源与连接矩阵通过

日期：2026-10-02。用户执行 `python3 tests/claude_mcp_sources_probe.py`，报告目录 `/tmp/netcap-claude-sources-k_xoptrt`。代理直接读取结果与各场景原始文件；不是代理命令环境内重新启动 CLI。固定 Claude 2.1.280，见 [版本](W02-06-sources-user-pass-versions.json)。

[结果](W02-06-sources-user-pass.json)：16 场景的 44 项来源/无输入/诊断检查及 13 项非空连接检查全部通过，整体 expectations_met=true。13 项是场景级检查，不是服务或工具总数；其余三场景预期空清单。

代理核对 [逐场景原始数据](W02-06-sources-user-pass-raw.json)：汇总 snapshot 与独立文件一致，服务名称/来源/数量匹配该固定版本的既有预期，每个可见服务为 connected 且工具名来自对应来源；进程 trace 与原始 startup.jsonl 一致，没有 tools/call，模型结果为零、tool-calls.log 不存在、stderr 为空。

覆盖 user/project/local 优先级、dynamic 覆盖、禁用与继承、strict 模式、允许/拒绝策略合并、managed 设置注入及插件来源并列。disabled_with_inheritance 保留 local identity，selection_matches=false 并明确报告无法确认禁用；拒绝/允许列表排除 identity 时给出原生清单缺席诊断。这些负向结果是预期保护行为，不能被解释成禁用已生效。managed 条件由测试传入，不代表真实企业管理配置已经验收。

本次补齐 [原来源诊断](W02-06-source-diagnostics.md)中失败的 13 项连接检查。permission deny/ask 两场景只检查启动清单；没有发送模型提示或调用工具，不能证明工具执行权限冲突已正确处理。OAuth、HTTP/SSE、动态头完整认证、原 ID 不重启重试、后台控制及六入口等硬要求仍保留。W02.06 in_progress，8/40 done。

下一项：在同一用户终端运行 `python3 tests/claude_oauth_mcp_smoke.py --transport http --recover-without-restart`，验证生产控制台、原生 OAuth 存储与重新认证后原 ID 重试，核对控制台及另一会话的进程保持。脚本仅使用隔离配置、本地模拟供应商与本地 OAuth/MCP 服务，清理自有资源；不调用真实供应商或停止共享后台。本轮未执行此项。通过后再按 SSE 分支推进，不能复用 HTTP 结果代替。

代理核对 `.dev/bin/netcap` 与上一原生采样归档一致，旧 `bin/netcap` 未变。本轮没有修改产品源码或构建，没有重跑 Go/浏览器检查。已有证据原样保留；[manifest](W02-06-sources-user-pass-manifest.json)标记归档时哈希，[进度 diff](W02-06-sources-user-pass-diff.patch)记录更新。源码哈希不是用户启动瞬间的文件证明；用户终端成功不解除代理命令环境的限制。
