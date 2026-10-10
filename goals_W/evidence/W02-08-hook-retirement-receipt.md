# W02.08 · 最后一个 hook 撤去的回执与核对边界

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 修复的行为

撤去最后一个本地 hook 时，如果退订或恢复回执不完整，系统现在保留旧快照、准确候选和暂停，不再把整包禁用记录当作撤去成功，也不会开放普通输入。操作记录显示缺少本连接的有效配置建立依据。原生已经确认新配置、仅 Runtime→Application 结果丢失时，仍能只读确认原候选，按原 ID 完成，不重放改配。

原实现只在目标仍含 hook 时校验 hook 配置建立依据。目标变为空后，核对只剩原 ID、历史、禁用策略和 MCP 缺席；然而禁用策略在退订/恢复之前就可能写入成功，不能证明已切换到继承配置。现在为 hook→空目标同样采样应用前配置指纹，在有效原生恢复回执及应用后配置核对成功后保存依据；核对阶段验证同连接、目标选择和配置指纹。普通无 hook 会话不因此增加建立依据要求。

生产修改仅涉及 `codex_plugin_change.go`、`codex_hook_selection.go`。复用既有候选事务、暂停、原 ID/完整历史和输入归属检查，没有增加盲目重试或用户权限入口。

## 失败复现与通过证据

[单元原始失败](W02-08-hook-retirement-receipt/unit-red.log)直接调用公共 Reconcile：原包已被禁用，但没有目标配置建立依据，返回了 `finished`。随后用真实固定 app-server 0.158.0 和生产 Application/Runtime/SQLite/Handler 复现：实际退订返回正常回执，测试边界向 Runtime 交付缺少必需字段的 `{}`；原配置 resume 尚未发送，Application 仍提升了候选，并错误接受一次输入。该次 `turn/start` 仅指向夹具的不可用本地供应商，不存在成功模型结果。

[原生失败](W02-08-hook-retirement-receipt/native-red/summary.json)保存四项失败：核对未保持未知、未阻止输入、出现模型输入请求、SQLite 未保留待核对候选。失败日志、完整协议及原生实际回执均保留，未用后续通过覆盖。

| 验证 | 实际结果 |
|---|---|
| 退订回执不完整 | [45 项通过](W02-08-hook-retirement-receipt/native-detach/summary.json)。真实退订已发生，但 Runtime 未取得可接受回执；原包策略写入一次，**零次配置 resume**，旧快照/候选/暂停保留。 |
| 恢复回执不完整 | [45 项通过](W02-08-hook-retirement-receipt/native-resume/summary.json)。真实配置 resume 一次，Runtime 未取得身份/路由完整的回执；原操作未知，输入、重复应用和候选丢弃受阻。 |
| 应用层结果丢失 | [42 项通过](W02-08-hook-retirement-receipt/native-application/summary.json)。Runtime 已取得真实配置确认并保存连接依据，再向 Application 返回 unexpected EOF；只读核对提升准确候选，配置 resume 仍只有一次。 |
| Runtime 边界 | [12 类 race 检查](W02-08-hook-retirement-receipt/unit-matrix.log)通过：缺依据、有效依据、旧选择、换连接、配置漂移/读取失败、notLoaded、未决审批、运行/未知 hook、策略缺失、历史变化；重复核对不发送原生写入。 |
| 浏览器 | [退订 15 项](W02-08-hook-retirement-receipt/browser-detach.json)、[恢复 15 项](W02-08-hook-retirement-receipt/browser-resume.json)、[应用层 9 项](W02-08-hook-retirement-receipt/browser-application.json)通过。真实 Handler 导出和当前构建的 HTTP 回放，核对原 ID/快照/候选、具体原因、暂停/输入/重复应用限制；已查看桌面与窄屏截图。 |
| 独立审计 | [27 项](W02-08-hook-retirement-receipt/audit.json)重新比较三份原始记录的 ID、暂停、当前/候选快照、状态、原生策略写入/退订/配置请求数及零模型输入。 |
| 回归与构建 | 组合资源/回执故障原生 90 项、正常禁用/撤去原生 41 项通过；相关 Runtime race、app/console 全包 race、tagged vet、gofmt、typecheck/开发构建通过。 |

“配置 resume”只统计带 `config` 的请求。既有权限核对也会调用不带配置覆盖的 resume；本片没有把它们漏报成零次原生调用，也没有把它们当作配置重放。

三个最终新流程、正常撤去回归各两个自有后台，组合回归四个自有后台均退出 0。两个失败夹具流程也已退出。最终新流程均为零模型/工具/hook 执行；沿用两个明确合成的 paused Goal 让日志落盘，不把它们称为真实模型历史。受管限制仍是更严格的响应注入，不是实际 `requirements.toml`/MDM 强制执行。

## 复验与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestCodexHookRetirement' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeHookRetirement' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

[浏览器脚本](../../tests/codex_hook_retirement_browser.js)分别使用三个原生目录的 `browser-responses.json` 替换 `__FIXTURE__`、`detach/resume/application` 替换 `__MODE__`，以当前 web/dist URL→文件映射替换 `__ASSETS__` 后由 Playwright MCP 执行。没有启动新 listener、Windows interop 或重复环境探测。浏览器已关闭。

原始目录依次为 `/tmp/netcap-w0208-hook-retirement-receipt-2796589900`、`-2590455995`、`-2371025872`；原生失败为 `-1665669512`。组合回归 `/tmp/netcap-w0208-combined-resources-1586800594`，正常撤去回归 `/tmp/netcap-w0208-hook-deactivation-1632574567`。充分复核所需材料已复制到[归档](W02-08-hook-retirement-receipt/)，较大的 result.json 无损压缩为 result.json.gz，另保留摘要和 Handler 导出；[源码差异](W02-08-hook-retirement-receipt/source.diff)与[清单](W02-08-hook-retirement-receipt/manifest.json)对应本片结束状态。

开发二进制 `.dev/bin/netcap` SHA256：`a8297d483a1a6c0144e0f609d2f3b2e712a9e254fbae89fcb4178c32bc2e9ceb`。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。Web 内容未变，仍为 `index-tdHkyIue.js` / `index-DSkOCaGd.css`；2954 个旧 W 证据未改，未提交或推送。

测试准备中的目录/Cwd/command ID、Playwright 的 URL 全局对象及 workspace 摘要缺少详细字段等错误分别保留。修正为实际 session detail 接口后取得上述浏览器结果，没有更改产品接口或放宽断言。OpenAI Docs 官方页读取因配置的代理不可连接失败，没有取得新官方正文；复用[既有固定版 ThreadResumeResponse](W02-08-codex-scoped-hooks/schema/ThreadResumeResponse.ts)与原生对照，仍未找到可代替连接建立依据的逐线程当前 hook 信任回读。

## 未完成范围与下一步

本片关闭“误报撤去成功并开放输入”的缺口，保留已确认配置的应用层漏回执恢复；**没有完成原生确认丢失后的安全恢复**。不完整回执只证明结果待核对，不能用禁用策略、旧正文、磁盘目录或原 ID 推断完整配置已生效。

下一步继续研究无需重放的原生确认与恢复条件；仍存活共享后台/多客户端输入独占、实际受管/完整来源与权限、方法/Gate、成功/异步/通用 hook、Windows/Unix proxy/peer 及四个已请求的 WSL 结果继续保留。未修改首版硬要求或整项完成条件。
