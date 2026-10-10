# W02.08 · Claude 恢复条件只读核对

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 交付范围

未完成的 Claude 本地设置采纳增加“核对恢复条件”，按原快照、候选和操作 ID 读取。原生层分别检查原失败事务/诊断绑定、自有查询已退出、记录内无未决工作、原生历史与原准备基线一致、原本地文件版本以及读取期间依据稳定；Application 同时检查其他快照来源，读取前后复核会话身份、候选、输入归属与来源。

结果明确标注 `owned_query_and_recorded_sources`，`satisfied` 只表示这些列明条件通过。它不打开原生查询、执行模型输入、改变配置回执、提升候选、清除未知或解除暂停。Runtime 必要时可建立只读 bridge 来读取既有记录，不能把这解释为已恢复原 CLI 查询。**明确恢复执行尚未接入**。

界面显示未恢复的原文件及各项条件，失败后清除旧成功结果；断线或其他操作开始时撤下旧核对结果，迟到响应不能重新显示成功。请求只接受三个保存引用，不允许外部路径、源配置或新能力选择覆盖原绑定。既有鉴权、同源限制和未知字段检查保持。

## 验证

| 层次 | 结果 |
|---|---|
| 真实 schema 失败后核对 | [23 项](W02-08-claude-recovery-check/schema/result.json)通过。App 重启后读取原失败记录，原文件仍无效时 `original_settings` 未满足；测试显式恢复原文件后列明条件全部通过，原操作仍 uncertain、候选 applying。 |
| 真实启动后漂移核对 | [26 项](W02-08-claude-recovery-check/startup/result.json)通过。候选 v2 hook 曾将文件改 v3，恢复 v1 文件前后有准确差异，既有 v2 副作用保留；只读核对没有新增 hooks、查询或输入。 |
| 原生只读性 | 两个样本保存读取前后的 metadata、journal 与 hooks 原始字节，完全相同；设置文件由测试显式恢复，不是核对入口修改。原生历史本轮为空，另从原配置和空历史重算原始基线吻合。 |
| Session 边界 | [7 项](W02-08-claude-recovery-check/session-node.log)通过：匹配、文件漂移、模拟外部历史变化、未决输入、退出未确认、操作错配及读取中 metadata 变化。使用公开 Session 方法与受控 SDK 历史读接口，query 调用计数为零；不冒称真实模型历史或并发外部 CLI 已验收。 |
| Application | [16 类](W02-08-claude-recovery-check/app-http.log)通过：只读、原文件漂移、引用/原生身份/基线/来源错配、版本、只读标记、缺失/重复/未知检查以及读取期间接管和源变化；候选/快照及执行次数保持。 |
| HTTP | [最终全包检查](W02-08-claude-recovery-check/app-console.log)包含新增 5 类鉴权/来源覆盖/null/缺会话测试。首轮缺令牌夹具同时漏 Origin，先被同源检查返回 403；修正夹具后通过，没有修改鉴权逻辑。原始 [app-http.log](W02-08-claude-recovery-check/app-http.log)保留该失败。 |
| 浏览器 | [20 项](W02-08-claude-recovery-check/browser-final.json)通过：不自动核对、准确保存引用、具体缺口、条件通过仍暂停、错误/错配/未知版本清除成功、断线迟到响应、窄屏及零执行命令。HTTP/SSE 与故障响应为真实 App 数据回放。 |
| 独立核对 | [33 项](W02-08-claude-recovery-check/audit.json)通过。直接比较原生文件字节、恢复前后设置正文 hash、重算基线、SQLite/原生事务状态及浏览器请求，不使用 producer passed 字段作为依据。 |
| 回归与工程检查 | 混合设置/资源 [18 项](W02-08-claude-recovery-check/mixed-regression/result.json)、应用层丢回执 [15 项](W02-08-claude-recovery-check/lost-regression/result.json)通过；app/console 全包 race、Claude Runtime race、tagged vet、bridge、Web typecheck/Vite 与开发 Go 构建通过。 |

两类主样本合计 **49 项**，不累计重复运行。[最后一轮](W02-08-claude-recovery-check/native-sources-final.log)增加恢复前后原始设置字节，供独立核对；[前轮及回归](W02-08-claude-recovery-check/native-final.log)另行保留。

## 复跑与产物

```bash
npm --prefix bridges/claude run build
node --experimental-test-module-mocks tests/claude_configuration_recovery_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app -run '^TestClaudeNativeSettings(Recovery.*|Adoption(MixedResources|LostReceipt))$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 goals_W/evidence/W02-08-claude-recovery-check/audit.py
```

固定 Claude **2.1.280**、SDK **0.3.280**，独立 profile/项目/状态、自有 hooks 与不可用 loopback 供应商，零模型输入；未改变用户配置、共享后台或旧 bin。原始位置见 [original-directories.json](W02-08-claude-recovery-check/original-directories.json)，SQLite 只保留必要投影。

浏览器[模板](../../tests/claude_recovery_check_browser.js)、[实际 fixture](W02-08-claude-recovery-check/browser-responses-final.json)、[执行脚本](W02-08-claude-recovery-check/browser.js)、[桌面缺口](W02-08-claude-recovery-check/blocked-1440.png)、[窄屏通过](W02-08-claude-recovery-check/passed-390.png)已归档，截图已查看。所有命令句柄和自有查询/浏览器结束。

- 开发二进制：`4ffb76d037d632bc70f0b630c3b8ab3f7717299f4b7bf980124dfd358d7878eb`。
- 旧 bin：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，未改。
- **4043** 个旧 W 证据逐文件 hash 不变。
- [source.diff](W02-08-claude-recovery-check/source.diff)与[manifest](W02-08-claude-recovery-check/manifest.json)标记本轮源码/构建/证据时点；无提交、推送或发布。

## 未完成与下一步

核对通过不代表完整有效配置、受管/服务端来源、全局输入独占或真实模型历史已验证，也不撤销已经执行的 hooks。跨来源读操作不是原子锁；读取期间发现变化时返回失败。没有持久失败诊断、退出未确认、来源缺失或历史不一致的情形仍不能据此继续。

下一片在这些可观测条件上验证显式结束失败改配、保留失败候选并按原 ID 恢复原配置的事务。恢复时将重新核对，不把本次结果当长期许可，不自动重放失败候选。完整策略/方法/Gate、原生进程内丢确认恢复、Windows/proxy 和五项既有 WSL 请求继续保留。
