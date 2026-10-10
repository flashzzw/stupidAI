# W02.08 · Codex 成功 hook 的生产记录与版本归属

日期：2026-10-07。沿用已有能力库审核、会话选配、原 ID 改配和 hook 执行记录路径，补齐真实成功事件贯穿 Application、SQLite、Runtime、Handler 与界面的验收。本片只有测试和证据变更，没有修改生产代码或重新构建。

## 行为与结果

带旧格式同步 UserPromptSubmit hook 的本地插件在明确授信后执行成功；未信任、整包禁用或撤信任不执行。原生输入经自有 loopback 文本供应商完成七个轮次，不依赖取消轮次来结束本次成功路径。

四次真实成功记录分别关联原轮次、操作、插件资源版本、完整内容标识和执行快照。库更新不升级当前执行，原 ID 明确升级后新调用使用 v2；两条 v1 历史仍保留原版本。冷重开 Application/SQLite/自有 stdio 后台不发送模型输入，保留原 ID、快照、消息和成功记录；再次输入生成新的 v2 记录。

| 验证 | 结果 |
|---|---|
| 生产原生纵切 | **81 项通过**；3 个会话、7 个实际完成的模型轮次、4 次成功 hook，零 turn/interrupt |
| 真实脚本作用 | 4 条带原 session_id/prompt/version 的输入标记；hook stdout 的测试上下文到达本地供应商 |
| 版本与持久状态 | 两条 v1、两条 v2，原操作/快照归属不变；SQLite 与实际 Handler 记录一致 |
| 既有超时回归 | **12 项通过**；真实 sleep 超时仍为 error/timeout，恢复后保留失败结果 |
| 浏览器 | **59 项通过**；重开前后、原/新版本、原生 ID/轮次/来源/耗时、无额外退出码、无跨会话串用、断连历史与移动布局 |
| 独立原始证据审计 | **69 项通过**；原生开始/完成事件、请求、脚本标记、SQLite、Handler 与浏览器核对 |
| 静态检查 | tagged vet 通过 |

本轮所有测试首次运行即通过，无生产缺陷修复。浏览器使用真实 Handler 响应回放，只发送 GET，模拟断连与真实原生重开分开记录；不将回放当作浏览器实际派发原生输入。

## 原生结果与边界

固定 app-server 0.158.0、声明的 proxy 0.157.1；测试通过现有 opt-in 原生 stdio 夹具接入生产 Runtime，未测试实际 serve/proxy 多客户端连接。配置、账号、项目、资产、SQLite、HTTP 供应商和原生进程均为测试自有。供应商返回固定文本 `OWNED_HOOK_SUCCESS_REPLY`；完成轮次证明协议和生命周期，不证明真实模型任务、Skill 方法或 Gate。

成功回执提供原生状态 completed、开始/结束时间及耗时，没有退出码，产品不补造退出码。原生 context 中包含测试 stdout `PRIVATE_HOOK_OUTPUT`，执行记录不显示该正文或命令。供应商历史中的上下文出现次数为 1/2/0/0/2/3/4：撤信任后不增加新的输出，先前合法执行留下的历史继续保留，不声称排除资源会清除历史。

前一片已验证同步 SessionStart 的原生成功契约，本片验证 UserPromptSubmit 的生产成功链路。成功/失败按实际原生终态分开，过去受限环境超时的根因、其他事件、异步/通用格式、真实受管强制策略及完整来源控制未因本片通过而完成。

## 复现与归档

```bash
export GOCACHE=/tmp/netcap-w0206-auth-go-cache
export NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
export NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeSuccessfulHookRecords$' -count=1 -v
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeHookExecutionRecords$' -count=1 -v
go vet -tags=nativecodex ./internal/... ./cmd/...
```

浏览器模板为 `tests/codex_hook_success_browser.js`，注入实际结果中的 sessions/responses 和当前 Web 构建路径。

原始成功目录 `/tmp/netcap-w0208-hook-success-1133193260`，超时回归目录 `/tmp/netcap-w0208-codex-hook-records-373769717`。完整测试结果、原生轨迹及自有供应商请求以 gzip 留存；其中仅有测试配置和合成凭据。[目录索引](W02-08-codex-hook-success/original-directories.json)、[执行结果](W02-08-codex-hook-success/command-results.json)、[独立审计](W02-08-codex-hook-success/audit.json)、[审计脚本](W02-08-codex-hook-success/audit.py)、[浏览器结果](W02-08-codex-hook-success/browser.json)、[桌面截图](W02-08-codex-hook-success/success-1440.png)、[移动截图](W02-08-codex-hook-success/success-390.png)、[源码差异](W02-08-codex-hook-success/source.diff)、[清理](W02-08-codex-hook-success/cleanup.json)和[哈希清单](W02-08-codex-hook-success/manifest.json)保存在同名目录。

5923 个旧证据、全部基线生产源码/构建和两个二进制保持原 hash。7 个自有原生进程退出，本地 HTTP 监听关闭，浏览器与执行句柄收尾，未提交或推送。开发二进制仍为 `26a3b6257e4bcc3998d5c78c514025eb761ccf34e0bdf6910494513141321b58`；本片原生纵切使用当前源码编译的测试程序，不伪称重跑开发二进制入口。

下一片回到 Claude 已安装插件的启动前排除与配置来源绑定，复用既有原型及版本事务。完整一般配置/四配置、受管/权限/外部依赖、真实方法/Gate、其他或异步 hook、共享后台/输入独占、硬崩溃、Windows 完整链路与 W04–W07 等硬要求保留。W02.07/08/09 仍 in_progress，9/40 done，Goal active。
