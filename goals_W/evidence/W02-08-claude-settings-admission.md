# W02.08 · 设置准入与混合资源

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 修复的行为

明确采纳本地设置并同时启用独立 Skill 与 CLI 插件时，原事务将已经选定要改变的本地 hooks 仍归入“继承组件必须不变”。真实候选已加载新资源并运行 v2 启动 hooks，但最终比较失败，保留 `uncertain/applying`。原始 [混合失败](W02-08-claude-settings-admission/mixed-red/result.json)与缩短后的 [3.43 秒复现](W02-08-claude-settings-admission/mixed-debug.log)保留。

临时诊断比较显示 Skill 和插件匹配均为 true，继承比较只有 `hooks` 不同，具体来源为 `localSettings`，其他字段和策略不变，见 [comparison.json](W02-08-claude-settings-admission/mixed-debug/comparison.json)。由此排除候选清单不匹配与历史变化，定位到被明确采纳的 hook 定义摘要。

生产按旧/候选文件差异识别实际采纳的 `projectSettings/localSettings` 来源组，仅从继承组件不变比较中排除这些普通 hooks。原生清单不提供文件路径，组内所有文件继续通过原文件版本绑定逐一核对。插件本身的准确组件校验不变，未选插件、用户、受管、未知来源 hooks 及原生策略继续严格比较；普通能力改配未传采纳来源时保持原行为。临时诊断已移除。

## 验证结果

| 场景 | 实际结果 |
|---|---|
| 无效设置 schema | [7 项](W02-08-claude-settings-admission/schema/result.json)通过。合法 JSON 中 `permissions.allow=42`，预览/准备通过，原生初始化跳过无效配置，apply 保持 `uncertain/applying`、原快照和候选，不声称已采纳；仅原 v1 启停 hook 实际执行。不是完整 schema 的启动前验证。 |
| 原查询 hooks 已关闭 | [8 项](W02-08-claude-settings-admission/initially-disabled/result.json)通过。原阻止机制不可用；修改文件后保存候选，prepare 被同步拒绝，零原生 apply、零 hooks。 |
| 明确采纳关闭 hooks | [15 项](W02-08-claude-settings-admission/disable-hooks/result.json)通过。保存/准备保留旧版本，原 ID 晋升目标并保留旧历史；新阻止机制显示不可用，新配置没有运行 hooks。不是受管策略试验。 |
| 设置 + 独立 Skill + Skill/hook 插件 | [18 项](W02-08-claude-settings-admission/mixed/result.json)通过。两种能力由禁用变为启用，准确原生 Skill/插件组件可见，插件启动实际使用 `NETCAP_GUARD_VERSION=v2` 且只执行一次，原 ID/历史/幂等保持。 |
| 浏览器 | [27 项](W02-08-claude-settings-admission/browser.json)通过，准确选择与设置差异共同展示，保存保留原快照，准备/应用只使用保存引用，断线/不可用准入、历史与应用后的模式正确；[桌面](W02-08-claude-settings-admission/preview-1440.png)/[窄屏](W02-08-claude-settings-admission/preview-390.png)已检查。HTTP/SSE/命令回放真实 Application 响应。 |
| 独立核对 | [77 项](W02-08-claude-settings-admission/audit.json)通过，依据 SQLite 只读投影、原生 metadata/journal、实际 hooks、文件 hash、原失败比较与浏览器命令体，不以 producer passed 字段为依据。 |
| 既有采纳回归 | 正常 14、丢应用层回执 15、无模型输入冷恢复 17、派发前漂移 9、启动后漂移 11，合计 **66 项**通过；见各 [normal](W02-08-claude-settings-admission/normal/result.json)、[lost](W02-08-claude-settings-admission/lost/result.json)、[cold](W02-08-claude-settings-admission/cold/result.json)、[before-dispatch](W02-08-claude-settings-admission/before-dispatch/result.json)、[after-startup](W02-08-claude-settings-admission/after-startup/result.json)目录。 |
| 其他原生回归 | [Skill 改配 37](W02-08-claude-settings-admission/skill-regression/result.json)、[插件选择 9](W02-08-claude-settings-admission/plugin-regression/result.json)、[guard 10](W02-08-claude-settings-admission/guard-regression/result.json)项通过。 |
| Node 与工程检查 | 新来源组/严格比较 **4**、原文件绑定 **5**、guard **6**、Session 插件 **17**、MCP 配置 **7** 项通过；bridge 构建、tagged app/runtime/console vet、Web typecheck/Vite 与开发 Go 构建通过。生产改动仅在 bridge，未重复无关 Go 全包测试。 |

四个新增原生场景合计 **48 项**，全部九类设置场景 **114 项**；重复运行不累加。固定 Claude **2.1.280**、SDK **0.3.280**，独立项目/profile/状态目录、自有 hooks、不可用 loopback 供应商，零模型输入和外部供应商调用。

中间失败按原因保留：[native-first.log](W02-08-claude-settings-admission/native-first.log)包含真实混合缺陷；[native-final.log](W02-08-claude-settings-admission/native-final.log)中丢回执案例被加速后的测试在只读核对前过早判失败，恢复等待后 [lost-final.log](W02-08-claude-settings-admission/lost-final.log)通过。Session 插件首轮缺少 `--experimental-test-module-mocks` 未运行到断言，补齐后 17 项通过。浏览器最初把包的原生名称 `adoption-pack` 误写为库显示名 `Pack`，修正断言后通过。没有用这些测试问题掩盖原生产失败。

## 复跑与产物

```bash
npm --prefix bridges/claude run build
node tests/claude_settings_admission_test.mjs
node tests/claude_settings_sources_test.mjs
node tests/claude_settings_guard_test.mjs
node --experimental-test-module-mocks tests/claude_plugin_selection_test.mjs
node --experimental-test-module-mocks tests/claude_mcp_configuration_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app -run '^TestClaudeNative(SettingsAdoption.*|Application(SkillChanges|PluginSelection|SettingsGuard))$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 goals_W/evidence/W02-08-claude-settings-admission/audit.py
```

浏览器复用[模板](../../tests/claude_settings_adoption_browser.js)，[fixture](W02-08-claude-settings-admission/browser-responses.json)增加明确的两个 `selections`；[已执行脚本](W02-08-claude-settings-admission/browser.js)记录当前构建映射。[原目录](W02-08-claude-settings-admission/original-directories.json)、[source.diff](W02-08-claude-settings-admission/source.diff)、[manifest](W02-08-claude-settings-admission/manifest.json)保存本次版本，数据库只归档必要投影。

3804 个旧证据、全部 Web 源码/构建与两个二进制逐文件 hash 不变。开发二进制仍为 `8c11137dcfa315928495989c3f3ea68582239c1facaebb227cfe472d18c8b31e`，旧 bin 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。bridge 的新 JS 版本单独在 manifest 记录，不能仅以相同 Go 二进制 hash 代表相同完整执行版本。执行器/浏览器已收尾，无提交或发布。

## 保留边界与下一步

受管环境、全部有效设置/权限/安装来源、全部策略与插件组合、原子文件读取、真实方法/Gate、原生进程内丢确认恢复、输入独占和 Windows/proxy 仍未完成。本轮仅验证普通项目/本地来源与当前固定版本；原生策略变化没有从继承比较中排除，未知状态仍不能自动重试。五项既有 WSL 请求继续分别待结果，不重复索取旧插件证据。

失败后的原快照与候选虽已保留，用户目前仍主要看到泛化的“待核对”原因。下一片补持久化候选失败阶段和不含敏感数据的具体依据，核对离线状态展示，保持原事务与不重放原则；不靠减少未知保护来改善界面。
