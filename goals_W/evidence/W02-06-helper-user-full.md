# W02.06 · HTTP/SSE 动态认证头完整请求验证

2026-10-02。用户独立 WSL 终端分别执行 `python3 tests/claude_remote_mcp_smoke.py --transport http --headers-helper` 与 SSE 分支，输出 `/tmp/netcap-console-wr9vszwf`、`/tmp/netcap-console-j680nt_6`。代理直接读取并复核文件，没有在代理受限环境重跑。

[HTTP](W02-06-helper-user-full-http.json)与[SSE](W02-06-helper-user-full-sse.json)各 45 项全部通过，共 90 项。固定 Claude 2.1.280/SDK 0.3.280，生产控制台、真实 CLI、隔离本地模拟供应商/MCP。每协议完成 3 次认证调用（A 1/B 2），各配置 helper 重复执行至少两次、cwd 正确。错误静态 Authorization 下动态头认证成功，错误 helper 分支被服务拒绝且不发模型/工具；同时覆盖单次审批、拒绝/排除、继承保留、原 ID/快照恢复和来源漂移保护。

[落盘复核](W02-06-helper-user-full-verification.json)保存 remote-calls、remote-requests、helper-invocations、最终快照的脱敏投影及供应商请求哈希：调用认证、profile/workspace/transport 正确；可见失效认证请求；A/B 命名空间独立；所有可识别远程身份工具结果回到对应配置的模拟供应商请求中。历史结果重复出现不计成新调用。不复制凭据或完整模型请求。

代理初次复核错误地要求所有最终会话都仍显示继承清单，在 HTTP A 上失败。检查脚本确认最后已故意修改来源，因此 A 最终为未核对清单且保留历史使用，B/disabled 仍有继承清单，disabled 无使用标记；修正的是后验检查假设，产品未修改。这也说明最终快照不能替代重启/审批/无额外调用的全部时序断言，前者直接复核，后者来自运行时测试结果。

本次补齐[连接前 helper 验证](W02-06-headers-helper.md)所缺 HTTP/SSE 动态头实际请求、失效认证和原 ID 恢复。helper 与 OAuth 登录侧目录组合仍被明确拒绝，完整权限/受管/插件冲突及 W01/W04 硬要求保留。W02.06 in_progress，8/40 done，未将整项标为完成。

同步更正 CONSOLE.md 中来源连接、原生 HTTP/SSE OAuth 恢复及动态头仍未验收的旧描述，保留代理执行限制与组合缺口。本轮仅文档与证据更新，未改产品源码/构建、未重跑 Go/浏览器测试、未操作用户或共享后台。历史证据不变；[diff](W02-06-helper-user-full-diff.patch)与[manifest](W02-06-helper-user-full-manifest.json)记录变更和归档时哈希，不冒充用户启动瞬间哈希。
