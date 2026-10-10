# W02.08 · Claude 本地插件整包选择与原 ID 查询恢复

日期：2026-10-02。本片完成新建时的本地插件选择、保存绑定、原生清单核对与查询重开。W02.08 仍为 **in_progress**，整体 **9/40 done**；插件原会话改配、真实组件执行和 Codex 路径不在本片完成范围内。

## 用户可见行为

新建 Claude 会话可为已登记的 `cli_plugin` 资产选择继承、启用整包或禁用整包。继承保留原生配置；显式选择记录资产版本、manifest 版本、完整目录文件/权限摘要和组件。执行详情显示预期来源键、版本、组件及最近原生核对结果；断开时明确提示重新核对。

沿现有创建入口贯通 Application → ExecutionSnapshot/Scope → Runtime → Session → SDK。使用原生本地插件加载入口；查询显式开启时调用 `reloadPlugins()` 取得包来源，并核对 Skill/命令、Agent、hook 和 MCP。只读 snapshot/catalog 不重载插件。原 ID 查询重开重新读取来源并验证保存的选择，目录漂移不自动升级。

`plugins_applied: true` 不作为组件加载成功依据。同名已安装来源、同名选配资产、额外或缺失组件、别名冲突、已禁用 hook、不可读策略、未连接 MCP 以及无法核对的自动组件均不能取得输入准入。新增源码仅保存文件摘要及组件元数据，不把 hook 命令和方法正文放入选择配置。

本片明确拒绝插件原会话新增/启停/版本切换/恢复继承事务。MCP/Skill 改配保留插件选择，并在操作、候选快照、原生请求和恢复核对中比较完整绑定。

## 实现中捕获的失败

- 原生 Session 测试沿上一阶段真实 red 继续；初始夹具 state 目录冲突及修正后的 red 日志均保留。
- Application 原生测试初次因缺少插件证据字段编译失败；接通 Runtime/投影后通过。
- 混合改配复现三个失败：请求插件选择同时修改普通 MCP 时，插件部分被漏处理。现在预览明确拒绝插件改配，原快照不变。
- Session SDK 契约捕获三个失败：普通 MCP ask 覆盖插件 MCP ask、冷查询 prepare 错误要求已加载插件、后续 `system/init` 版本变化仍沿用旧包证据。合并 ask、按冷/热状态核对并使不匹配的包缓存失效后均通过；核对跨查询变化时拒绝输入。
- Node `--test` 子进程入口未给出具体用例输出；改用项目已有的直接执行方式取得上述三个可定位失败。没有把首次 runner 失败计为产品缺陷或通过。
- 首轮 browser 断开断言误将“最近核对”历史说明算作当前状态；页面已显示断开状态，修正为检查当前状态行。未借此改写历史证据。
- 首轮全包 race 暴露新增改配测试在创建完成前预览；测试改为等待调用方可见 idle。针对性 race 和随后全包 race 均通过。

## 实际验证

固定 Claude Code **2.1.280**，SDK **0.3.280**。独立临时 profile、项目、SQLite 和状态目录；合成凭据，模型端点为不可用的本地地址。没有向模型发送输入，没有执行插件 hook 或 MCP 工具，没有修改共享后台或用户原生配置。

| 层次 | 实际结果 | 可复核记录 |
|---|---|---|
| 原生 Session/SDK/CLI | 14 项通过：启用/禁用、保留项目 Skill、来源版本、hook 来源键、原 ID 重开、漂移拒绝、零输入/结果及正文不外传 | [原生结果](W02-08-plugin-selection/native-result.json) |
| Application/SQLite/Runtime/CLI | 9 项通过：A/B 选择、固定来源、独立状态目录、零模型消息、应用重启后保存绑定、源漂移拒绝 | [应用结果](W02-08-plugin-selection/app-native-result.json) |
| 内存 SDK/进程契约 | 15 项通过：ask 合并、冷 prepare、init 失效、组件和文件故障；不算真实原生权限执行 | [SDK 日志](W02-08-plugin-selection/sdk-green.log) |
| Runtime 投影 | 15 场景：包版本/路径/来源、全部组件、别名、策略及失败 MCP 决定输入准入 | [契约日志](W02-08-plugin-selection/plugin-contracts.log) |
| Application | 11 场景：保存、无效来源、改配拒绝、MCP 改配丢回执/重启保留插件 | [最终插件 race](W02-08-plugin-selection/plugin-race-final.log)、[混合改配](W02-08-plugin-selection/plugin-mixed-change.log) |
| 浏览器 | 16 项：A/B 详情、创建选项/请求、版本分列、组件、断开及 390/1440 布局 | [浏览器结果](W02-08-plugin-selection/browser.json)、[原生应用响应](W02-08-plugin-selection/app-browser-responses.json) |
| 回归 | 原生 Skill 改配 32 项、Skill 资源 18 项、认证恢复 10 项、hook 通道 7 项通过 | 同目录 `*-regression.log` 和 [Skill 原生结果](W02-08-plugin-selection/skill-change-result.json) |
| Go/构建 | app/console 全包 race、Claude Runtime race、相关带 nativeclaude 标签 vet、bridge build、web typecheck、`make dev-build` 通过 | [全包 race](W02-08-plugin-selection/app-console-race-final.log)、[Runtime race](W02-08-plugin-selection/runtime-race.log)、[构建](W02-08-plugin-selection/dev-build.log) |

原始目录：

- Session：`/tmp/netcap-w0208-plugin-selection-4z2fla__`。
- Application：`/tmp/netcap-w0208-plugin-app-3125077450`。
- 开发过程与之前源码备份：`/tmp/netcap-w0208-plugin-selection-work`。

Application 的重启测试只验证保存的身份和绑定；原生 Session 单独验证无模型历史的原 ID 查询重开。浏览器使用实际 Application 响应，但 HTTP、EventSource 和创建回执为受控回放，不计为新增真实会话。Agent 组件按原生报告名称核对，hook 按事件和来源键/数量核对；不能外推为实际组件执行来源或结果验证。

## 复跑入口与产物

```bash
npm --prefix bridges/claude run build
python3 tests/claude_plugin_selection_native.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags nativeclaude ./internal/app -run '^TestClaudeNativeApplicationPluginSelection$' -count=1 -v
node --experimental-test-module-mocks tests/claude_plugin_selection_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run 'TestClaude|TestCapabilityCatalog' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器入口为 `tests/claude_plugin_selection_browser.js`，替换 `__FIXTURE__` 为本片应用导出的 `browser-responses.json`，`__ASSETS__` 为本地 `web/dist` 路径映射后通过已配置 Playwright 执行。截图：[1440](W02-08-plugin-selection/selection-1440.png)、[390](W02-08-plugin-selection/selection-390.png)。本次浏览器已关闭。

开发二进制 `.dev/bin/netcap` SHA-256：`65feda93f466b245d6c54a7603d3c7d705e6428d259021aabb09079946a51beb`。

旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，本片未提交。按开始时源码备份核对 [最终差异](W02-08-plugin-selection/changes.diff) 与 [manifest](W02-08-plugin-selection/manifest.json)；原 1008 个证据文件均未改动。

## 剩余硬要求

插件原 ID 改配事务、实际 hook 成功/失败、插件 MCP 完整调用/单次审批/拒绝与结果归属、真实模型/工具在途与带历史恢复、安装/受管来源的独立控制、Codex 插件选择仍需实现或验收。当前 hook 和 Agent 清单检查不能代替执行来源追踪。源文件树检查没有冻结原生运行环境，原生未报告的加载变化不能被当作已验证。

下一片继续接插件组件实际执行证据与失败路径，再推进原会话版本事务；保留 W02.08 和完整 W 系列的全部硬要求。此前等待的 W02.07 方法探针没有新增结果，本片未重复执行已知受限的宿主/监听探测。
