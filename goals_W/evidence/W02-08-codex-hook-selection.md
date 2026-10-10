# W02.08 · Codex 插件 hook 选配与版本信任

2026-10-05。旧格式本地插件的同步 command hook 已接入新建选择、原 ID 改配、明确版本信任及执行归属。最终混合包生产原生链路 40 项、纯 hook 包 34 项、浏览器 32 项通过；受影响的独立读取/通用插件原生回归 26/116 项通过。这里验证了明确超时执行及未信任/禁用时不调用，没有成功模型、工具或 hook 结果。W02.07/W02.08 保持 in_progress，9/40 done，Goal active。

## 用户可见行为

新建 Codex 会话选择插件后，可展开“此版本的 hook 信任”，手动读取当前完整包的审核依据。默认未信任，勾选后才允许该完整版本的同步 hook 命令执行；模型只读策略不能替代 hook 执行信任。新建和改配共用审核界面，展示资源版本、来源、完整内容标识、事件及相对入口。含 hook 的新绑定必须明确提供审核结果；原绑定的单纯启停可保持已保存的信任。

原会话沿用“预览 → 保存候选 → 准备并保持暂停 → 明确应用”，可以撤销信任或升级后重新审核。预览明确显示信任变化，保存不改变当前生效快照。库更新不升级旧会话，历史调用仍关联原操作的资产版本与完整内容标识。来源、版本、实例、断线和关闭表单会清除临时审核依据；来源变更同时清除改配表单保存的临时回执和旧预览。

已支持的包范围是旧 `.codex-plugin/plugin.json`、包内 hook JSON 文件、已知事件及同步 `command` handler；可与 Skill/MCP 共存。通用/内联/async hook 不在本片准入范围。界面的“本连接 hook 配置已核对”与调用结果分开，不能将包定义读取或信任等同于成功执行。

## 绑定与恢复契约

- 认证接口 `POST /api/capabilities/codex-hooks` 返回服务器生成的 `inspection_id`。最多缓存 64 份，15 分钟有效；绑定登记 revision、资产版本、实例配置及完整包文件清单/权限。浏览器只提交 `inspection_id` 和 `trusted`/`untrusted`，不能指定原生 key/hash、路径或内部绑定。
- 审核后快照保存完整资源、原生定义、信任和声明清单。已保存候选不依赖临时 receipt 存活；内部未导出的 `hookBinding` 仅从持久候选产生，重建时比较完整包，HTTP 不能输入它。升级必须取得新版本审核。
- Runtime 保留无关继承 hook 状态，把独立读取的来源前缀换为本会话私有来源，保留原生后缀和定义 hash；私有来源若已经出现在继承信任表则拒绝。脚本正文仍由完整资源哈希约束，不能仅靠原生 handler 摘要。
- 新建、明确改配和冷恢复先独立重读定义，再建立仅当前连接有效的记录：连接 ID、选择摘要和完整 `config/read` 指纹。JSON 数字用 `UseNumber` 保留精度。读取未应用候选只返回未核对，不删除当前选择的依据；同一选择出现实际配置漂移才使其失效。
- 已加载会话没有本连接建立依据时不获得输入资格；冷恢复重传原选择并重新核对。该记录不冒充原生逐项当前信任回读，也不构成多客户端输入独占。
- 原 ID hook 改配接受本连接明确的 `unsubscribed` 或 `notLoaded` 回执，并复核原历史及完整空闲范围；`notSubscribed` 不足以证明 hook 可改配。退订不等于全局卸载，不停止共享后台。
- 调用归属依据原操作、原快照及唯一匹配的缓存入口/事件。完整包标识、资产版本随历史保存；未报告的完整 pluginId、退出码不编造。未知、运行中或不完整终态仍阻止输入。

## 实际验证

| 层次 | 结果与证据 |
|---|---|
| Application | 11 类审核准入与漂移场景、显式信任预览、保存不改当前、receipt 消失后重建通过；[日志](W02-08-codex-hook-selection/logs/app-saved-green.log) |
| Runtime | 10 类信任/继承/候选读取/缺连接依据/配置与缓存漂移场景通过；[日志](W02-08-codex-hook-selection/logs/numeric-green.log) |
| HTTP | 既有 9 类审核准入及浏览器原生字段、内部绑定拒绝通过；[日志](W02-08-codex-hook-selection/logs/http-review.log) |
| 最终混合包 | Application → Runtime → 原生 CLI → Handler，40 项通过；[摘要](W02-08-codex-hook-selection/native-mixed/summary.json)，完整 result/browser-responses 以 gzip 无损保存 |
| 纯 hook 包 | 相同链路 34 项通过；[摘要](W02-08-codex-hook-selection/native-hook-only/summary.json) |
| 退订独立对照 | 既有 109 项契约继续通过，三次退订后仍为 idle 且在 loaded/list；[摘要](W02-08-codex-hook-selection/unsubscribe-readback/summary.json)、[实际读回](W02-08-codex-hook-selection/unsubscribe-readback/unsubscribe-readback.json) |
| 受影响回归 | 原生包定义读取 26 项、通用根插件 116 项通过；[读取](W02-08-codex-hook-selection/regression-inspection/summary.json)、[通用包](W02-08-codex-hook-selection/regression-portable/summary.json) |
| 浏览器 | 最终构建 32 项通过；[结果](W02-08-codex-hook-selection/browser.json)、[信任表单](W02-08-codex-hook-selection/review-1440.png)、[详情 1440px](W02-08-codex-hook-selection/execution-1440.png)、[详情 390px](W02-08-codex-hook-selection/execution-390.png) |
| 回归与构建 | Application/Console 全包 race、相关 Runtime race、vet、最终 make dev-build 通过；[日志目录](W02-08-codex-hook-selection/logs) |

最终原生目录 `/tmp/netcap-w0208-hook-selection-555041424`。A 信任、B 未信任、C 整包禁用，各自使用同资产的私有副本。A 明确超时调用保留 v1 归属；撤销后不调用，升级 v2 后恢复调用；B 保持 v1 未信任。后台冷恢复仍为原 ID/v2，历史 v1 不重标。Skill/MCP/hook 三类组件保留，MCP 连接但不调用。原 profile 不变，三个自有后台退出 0。纯包目录 `/tmp/netcap-w0208-hook-selection-2457254581` 的三个后台也已退出。

固定 app-server 0.158.0，受控生产测试连接通过独立 stdio wrapper 转发原生协议；不等于生产 Unix proxy/peer 验收。供应商为不可用本地地址，轮次明确中止，hook 使用 2 秒命令和 1 秒期限。浏览器使用该原生测试真实快照、审核回执、预览及 Handler 导出，HTTP/SSE、command ACK、迟到/故障/来源变化为受控传输；不算浏览器真实原生执行或 Windows 验收。

## 失败与修复

1. 保存后的准备丢失 hook 信任，误报没有差异：改为从已保存候选恢复完整内部绑定；[原失败](W02-08-codex-hook-selection/failed-saved-review/summary.json)。
2. 预读未应用候选错误清掉当前依据：先比较选择摘要，再判断实际配置漂移；[原失败](W02-08-codex-hook-selection/failed-candidate-witness/summary.json)。日志名 `native-saved-green.log` 实际失败，不按名称判定通过。
3. 误要求退订后必须全局 notLoaded：独立 readback 证实三次均 `unsubscribed`、仍 idle/loaded，但新信任正确生效。修正为本连接回执与完整空闲/历史复核；[原失败](W02-08-codex-hook-selection/failed-global-unload/summary.json)及原假设 [plan-initial.md](W02-08-codex-hook-selection/plan-initial.md)保留。`native-candidate-green.log` 同样实际失败。
4. 大整数 9007199254740992 → 9007199254740993 曾被 float64 合并，配置漂移未检出：改用 RawMessage/UseNumber；[失败](W02-08-codex-hook-selection/logs/numeric-red.log)及上表通过日志保留。
5. 混合夹具 MCP 参数 `${PLUGIN_ROOT}/mcp.py` 与 `${CLAUDE_PLUGIN_ROOT}/mcp.py` 均未展开，原生启动失败并正确阻止输入。改用项目已验证的 `python3 -u -c` 本地模拟服务；[第一次](W02-08-codex-hook-selection/failed-mcp-plugin-root/summary.json)、[第二次](W02-08-codex-hook-selection/failed-mcp-claude-root/summary.json)及各自 native-mcp-error-excerpts.json 保留。不声称产品支持这两个 MCP 参数变量。
6. 来源变化后改配父表单仍保留旧审核回执：复现失败后按连接和资产来源上下文清掉回执/预览；[失败](W02-08-codex-hook-selection/logs/browser-source-red.log)、最终浏览器两项新增检查通过。服务器原本已拒绝陈旧审核，未把它提升为有效信任。
7. 构建初次有已收窄分支的 TS2367，删除死分支后通过；浏览器初次按钮全角/半角不匹配、生成 helper 漏括号属于夹具问题。信任 checkbox 被全局样式拉宽已局部修整，最终截图确认。原日志均保留。

早期三份生产失败各只完成 16 项断言，后续流程中断，整体 `passed: false`；两份 MCP 夹具在首组检查前失败。不能因为已记录 checks 都为 true 将它们称作整体通过。

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
  -run '^(TestCodexNativeHookSelectionAndTrustChanges|TestCodexNativeCapabilityHookInspection|TestCodexNativePortableRootPlugin)$' -count=1 -v
```

[浏览器模板](../../tests/codex_hook_selection_browser.js)的 `__FIXTURE__` 使用最终原生 browser-responses.json，`__ASSETS__` 映射 web/dist。模板经 Playwright MCP 执行，归档 [prepare_browser.py](W02-08-codex-hook-selection/prepare_browser.py)记录本次路径替换。退订对照 [readback_probe.py](W02-08-codex-hook-selection/readback_probe.py)复用 `tests/codex_scoped_hook_selection_probe.py`。最终 Go 回归后仅修整界面并增加 HTTP 拒绝测试；对应 HTTP、构建和浏览器已补跑。完整 Runtime 的既有 OpenCode listener 环境失败未纳入通过声明，没有重复环境探测。

旧 `bin/netcap`：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，不变。最终 `.dev/bin/netcap`：`7f327efe8df6e1571d833df31ad66b8b4f7aadbd0c9d303a6185a768648fb268`。Web：`index-Clf7_BWK.js` / `index-DSkOCaGd.css`。2523 个旧 W 证据未改；具体源码、构建及本片归档哈希见 [manifest](W02-08-codex-hook-selection/manifest.json)，边界见 [audit](W02-08-codex-hook-selection/audit.json)与 [diff](W02-08-codex-hook-selection/changes.diff)。未提交或对外发布。

剩余：成功 hook 生命周期的既有 WSL 对照、其他事件与 async、通用/受管/已安装来源、普通原生目录 Skill 恢复、真实方法/Gate、Windows、Unix proxy/peer 及输入独占。四个已有终端请求继续待返回，不重复要求用户运行。下一可执行片继续普通原生目录 Skill 的生产绑定与连接内恢复依据，沿已验证热/冷契约推进；未获得逐项原生回读的已加载会话继续保留未知。
