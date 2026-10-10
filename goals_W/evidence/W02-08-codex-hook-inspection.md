# W02.08 · 能力库的 Codex 插件 hook 定义读取

2026-10-05。能力库已接通已登记插件的独立原生定义读取，固定 Codex app-server 0.158.0。最终原生 26 项、浏览器 29 项、相关 Go race/vet 和开发构建通过。该片完成包定义读取；完整会话选配、信任绑定及成功执行仍未完成。W02.07/W02.08 in_progress，9/40 done，Goal active。

## 用户可见结果与边界

在能力库的 Codex 插件卡片展开“Codex 插件 hook 定义”，选择登记实例并手动读取。只展示本次完整包版本的原生 key/hash、相对入口、事件、匹配条件、期限与异步声明。结果标明包定义范围，不声称已授信、已选配或执行成功。实例切换、来源刷新、登记更新、失败和断线清除旧结果；迟到的其他实例回执及不匹配来源回执被丢弃。

认证后的 `POST /api/capabilities/codex-hooks` 仅接受 `asset_id`、`revision`、`source_sha256`、`instance_id`。路径、执行文件和信任覆盖不能由浏览器传入。Application 校验登记及完整资源版本；读取前后复核文件、权限和登记/实例配置。同资产/实例不能重复并发，最多两个读取；关闭 Application 会取消并等待读取结束。

Runtime 复用完整插件副本生成逻辑，独立 HOME/profile/工作目录下初始化 Git 边界，以免 TMPDIR 位于现有项目内时读取父配置。登记实例的 `server_executable --version` 和 initialize userAgent 均需匹配 0.158.0。仅发送 initialize、initialized、hooks/list，拒绝服务端执行请求、执行通知、错回执、超限或不完整清单。读取结束复核副本，清理自有进程组和临时目录，不连接原后台、不复制原认证/历史/信任。

当前准入限于旧 `.codex-plugin/plugin.json`、包内 hook JSON 文件、已知事件及 `command` handler。声明数量逐入口/事件与原生返回核对，空/部分清单不能通过。[通用格式发现缺口](W02-08-codex-hook-metadata.md)仍保留；没有转换包格式或放宽选配条件。临时读取的来源 key 属于包读取命名空间，不能作为真实会话授信 key。原生 handler hash 与完整包内容标识分别保存，脚本正文变化不能只靠原生 hash 检出。

## 实际验证

| 层次 | 结果及依据 |
|---|---|
| Runtime 协议与清理 | 正常读取、23 个协议/进程故障、启动前来源漂移、只读目录及正常退出后的子进程清理通过；[最终日志](W02-08-codex-hook-inspection/logs/cleanup-final.log) |
| Application | 15 个读取/归属/漂移场景、9 个启动前拒绝场景，以及重复请求和关闭取消通过；[日志](W02-08-codex-hook-inspection/logs/app-console-green.log) |
| HTTP | 9 个认证、Origin、结构、未知覆盖字段及未登记场景通过；同上日志，正常返回另由原生 Handler 纵切验证 |
| 最终真实原生链路 | Application → Runtime → Handler 26 项通过；[结果](W02-08-codex-hook-inspection/native-final/result.json)、[stdio 协议](W02-08-codex-hook-inspection/native-final/inspection-protocol.jsonl) |
| 复制逻辑受影响回归 | 既有 scoped 插件 28 项、通用根插件 116 项通过；[scoped 摘要](W02-08-codex-hook-inspection/regression-scoped/summary.json)、[通用摘要](W02-08-codex-hook-inspection/regression-portable-root/summary.json)，完整原始结果以 result.json.gz 归档 |
| 浏览器 | 构建 UI 回放真实原生 Handler 导出，29 项通过；[记录](W02-08-codex-hook-inspection/browser.json)、[1440px](W02-08-codex-hook-inspection/inspection-1440.png)、[390px](W02-08-codex-hook-inspection/inspection-390.png) |
| 静态/回归/构建 | Application/Console 全包 race、相关 Runtime race、vet、make dev-build 通过；[日志目录](W02-08-codex-hook-inspection/logs) |

原生测试使用自有 recording wrapper 转发固定二进制，两个登记实例别名读取同一个旧格式混合包，两个 hook JSON 入口含四个 handler（一个 async），另含 Skill/MCP。工作目录嵌在含无效父配置的 Git 项目中，隔离仍通过。旧哈希拒绝，新文件哈希不能冒充旧登记；显式升级至 asset-v2 后才能重新读取。只改脚本正文会改变完整包 key/内容标识，而原生 handler hash 保持，二者没有混为一个版本。

原 profile（含自有假 auth）文件哈希不变，未创建应用 Session/Operation，未出现 MCP/hook 标记，三个独立原生读取进程退出 0，临时目录均移除。最终原始目录 `/tmp/netcap-w0208-hook-inspection-580459582`。浏览器使用 `/tmp/netcap-w0208-hook-inspection-2705807450` 的实际导出，归档于 `native-browser-fixture/`；随后仅修改正常退出的进程清理，最终构建 JS/CSS 与浏览器被测资源相同。浏览器 HTTP/SSE 受控，409、错绑定、迟到及断线为合成故障；不将浏览器回放当作 Windows 或真实 hook 执行验证。

## 失败与修复记录

- 临时副本遭子进程修改时曾返回成功；增加读取后的完整复核。[原失败](W02-08-codex-hook-inspection/logs/cache-drift-red.log)与回归日志均保留。
- 只读根目录跨父目录 rename 失败；改为目标父目录内的临时 stage，保持权限后原子发布。[失败/诊断](W02-08-codex-hook-inspection/logs/readonly-debug.log)及 [通过](W02-08-codex-hook-inspection/logs/readonly-green.log)保留，临时 debug 已移除。
- 最终核对复现了父进程正常退出后、关闭 stdio 的自有子进程仍存活；正常与异常返回均清理进程组。[原失败](W02-08-codex-hook-inspection/logs/normal-exit-cleanup-red.log)及最终回归保留，原生读取重新通过。
- Application 权限测试先前设置了与原目录相同的 mode；改用真实权限变化。该次失败属于夹具，不宣称修复产品权限校验。
- 浏览器首轮受工具环境缺少 URL 全局影响，末轮曾在刷新响应提交前断言；使用既有 URL 解析方式、等待响应及渲染后 29 项通过。两份脚本诊断日志保留，没有将其记为产品缺陷。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^(TestCodexNativeCapabilityHookInspection|TestCodexNativeScopedPluginSelection|TestCodexNativePortableRootPlugin)$' -count=1 -v
```

[浏览器脚本](../../tests/codex_hook_inspection_browser.js)中的 `__FIXTURE__` 取原生测试的 browser-responses.json，`__ASSETS__` 映射当前 web/dist 的 HTML/JS/CSS；脚本经 Playwright MCP 执行。最终进程清理修改后重跑读取回归及原生读取、vet、构建；未重复未受影响的全部 Application 和插件选配回归。完整 Runtime 包既有 OpenCode listener 环境失败没有纳入通过声明。

旧 `bin/netcap` 保留为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。最终 `.dev/bin/netcap` 为 `d4dbf306c9c25d7dff08a6709338dffab464a66f6037c608ffe8791e5b699ca0`。2381 个旧证据保持不变，源码/构建/新归档哈希见[清单](W02-08-codex-hook-inspection/manifest.json)，修改边界见[审计](W02-08-codex-hook-inspection/audit.json)与[差异](W02-08-codex-hook-inspection/changes.diff)。未提交或对外发布。

下一片继续真实会话的 hook 组件选配、定义/完整资源版本与信任绑定，以及执行归属。通用 hook 原生发现、成功执行 WSL 对照、普通目录 Skill 恢复、方法/Gate、异步/受管来源、Windows/proxy 和输入独占仍待；四个既有终端请求未重复发送，不因本片通过改变硬要求。
