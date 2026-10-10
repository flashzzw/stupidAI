# W02.06 · 验收范围审计与剩余原生回归

2026-10-02。本片按 W02.06 正文、需求 §12.2、ACCEPTANCE 的 R22/R23/R25/P12/P13/P15 核对已交付行为。没有改变任务或需求，W02.06 仍 in_progress，累计 8/40，Goal active。

## 条件与证据

| W02.06 条件 | 已有依据 | 当前判定 |
|---|---|---|
| MCP 启动选择、真实工具清单 | [stdio](W02-06-stdio-native.json)、[来源矩阵](W02-06-sources-user-pass.json) | 来源矩阵近期通过；stdio 实际调用需补当前版本回归 |
| A/B cwd、凭据、状态与结果隔离 | [动态头 HTTP/SSE](W02-06-helper-user-full.md)、[OAuth HTTP](W02-06-oauth-http-user-recovery.md)/[SSE](W02-06-oauth-sse-user-recovery.md) | 用户终端分别通过，代理已复核落盘请求/结果 |
| 单次审批、拒绝/排除不调用 | 同上；[原 stdio](W02-06-stdio-native.json) | HTTP/SSE 近期通过，stdio 纳入回归 |
| 原 ID/原选择恢复，不串用 | 同上；[启停改配](W02-06-apply.md) | 有分层证据；变更过的公共 bridge 路径需资源回归 |
| settings 覆盖、继承与严格配置 | [16 场景原生来源](W02-06-sources-user-pass.md)、[资源/继承](W02-06-resources.md) | 来源检查通过；不把不可排除同名来源标为禁用 |
| 原 ID 改配、生效边界可见 | [启停](W02-06-apply.md)、[资源](W02-06-resources.md) | 生产改配模块哈希保持，公共 bridge 有后续修改，补多资源回归 |
| 操作界面与准备回执 | [启停浏览器](W02-06-apply.md)、[资源浏览器](W02-06-resources.md)、[准备/来源 UI](W02-06-auth-ui.md) | 保留原测试层次；不将受控浏览器称完整原生 UI 集成 |

[检查记录](W02-06-acceptance-audit-inspection.json)逐个读取原生结果断言并核对关键源文件历史哈希。早期 stdio、资源实验后 session/mcp/协议及投影已有变化；旧记录仍是历史证据，不能无条件证明当前所有路径。近期 HTTP/SSE 不能完全代替当前 stdio 和多资源改配。

## 原生登录接口边界

固定 2.1.280 的 `mcp login --help` 仅列 name 和 --no-browser，没有动态定义专用参数；旧[原生 OAuth 对照](W02-06-oauth-native.json)已实际记录 --mcp-config 下 login 找不到动态服务。安装 SDK 公共声明有 status/reconnect/toggle/setMcpServers，没有在本次检查中找到 OAuth 交互入口；这不是不存在其他原生入口的证明。

官方 [MCP 文档](https://code.claude.com/docs/en/mcp)本轮经 Context7 查询说明：返回 Authorization 的 headersHelper 使用自身认证，不回退 OAuth；`/mcp` 是交互认证入口。这个事实不证明非 Authorization helper 与 OAuth 可以在侧目录安全组合。上一片[原生 cwd/凭据对照](W02-06-login-helper.md)的失败结论保持，生产仍拒绝该组合。

完整有效来源/权限联合矩阵由 W02.09 正文定义，不能因其未开始而声称已通过，也不把它混同为单个连接探测。组合不可支持时原配置保持并展示限制，符合需求 §12.2 的显式失败要求；不把组合自身列为已支持，不删除原硬要求。

## 本轮验证与恢复入口

app/console 全包 [race](W02-06-acceptance-audit-app-console-race.txt)通过；[认证生命周期](W02-06-acceptance-audit-auth-contracts.txt)10 项及[来源契约](W02-06-acceptance-audit-source-contracts.txt)7 项通过。新增脚本 AST 检查通过。无产品源码或构建改动，未重跑浏览器。

新增 [claude_w0206_acceptance.py](../../tests/claude_w0206_acceptance.py)顺序复用现有 stdio 选配与多资源改配脚本，保存构建哈希、日志、原始目录和结果；失败、缺结果、空检查或非 true 检查均停止后序。它不重跑已通过的 OAuth/helper 矩阵。

当前代理环境的[实际运行](W02-06-acceptance-audit-runner-blocked.json)在 stdio 首个本地供应商 socket 处 [EPERM](W02-06-acceptance-audit-native-blocked.txt)，整体退出 1；resources 未启动。仅验证了当前失败/停止路径，未把它计为原生验收成功。

在已通过环境检查的用户 WSL 终端运行：

```bash
cd /home/zzw/codex-netcap
python3 tests/claude_w0206_acceptance.py
```

提供 Acceptance directory 后，代理可读取两个原始目录。脚本只用现有开发构建和隔离本地模拟服务；各子测试负责关闭自有进程，不操作共享后台或真实供应商。拿到当前版本原生结果后继续逐项收口；本片不提前提升任务状态。

历史证据与旧二进制保留。改动见 [diff](W02-06-acceptance-audit-diff.patch)，归档时点哈希见 [manifest](W02-06-acceptance-audit-manifest.json)。
