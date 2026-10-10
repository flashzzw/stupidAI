# W02.08 · Claude 插件原 ID 改配

日期：2026-10-03。W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 行为与边界

原会话能力改配支持 CLI 插件新增、整包启停、明确资产版本替换与恢复继承。复用既有预览、保存候选、准备并暂停、应用和持久结果核对；保存不修改生效快照，未选择的 MCP/Skill/插件维持原绑定。库更新不升级旧执行，重新启用已停用或登记版本已变化的资产需要明确选择可用版本。

Session 在已确认空闲的自有查询边界按原 ID 关闭/重开，检查历史未变化、插件包及所有声明组件与目标一致、撤下的名称与别名消失。原生 MCP、Skill、Agent、hook 与插件来源的其他继承项参与前后比较，不因一次插件选择被顺带删除。混合 Skill 改配时使用包含插件影响范围的继承比较，避免把预期改变的插件 Skill 误判成无关来源变化。

目标/原插件选择、加载清单与结果和生效配置原子持久化。候选失败后只有自有进程退出才记为 not_sent 并保留原绑定；未知结果仍保持暂停，重复 operation 不重新应用。Runtime 依具体状态核对原/目标插件绑定，不能把“正在变更的插件必须已经等于原配置”作为前提；同时补齐插件 MCP 持久清单的恢复核对。已撤下组件再次出现在清单时不授予输入。

界面沿用原会话能力改配，显示 CLI 插件资产、显式资源版本和候选差异，保存/准备/应用分开。插件组件清单继续在执行详情展开。来源仍按当前支持的独立本地整包绑定，不能据此宣称受管/同名安装来源均可控制。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| 原生 Session/SDK/CLI | **30 项通过**：禁用、启用、恢复继承、重新添加、版本 1→2、移除旧命令、同 operation 不重开、候选组件不匹配保留原配置、失败后释放及原 ID 重开；零模型输入/工具调用 |
| Application/SQLite/Runtime/CLI | **45 项通过**：候选不生效、原 ID/历史版本/暂停、插件差异、精确候选提交、库更新不自动升级、A/B 各自版本、应用重启保存历史、冷启动改配；零模型输入 |
| SDK/受控进程契约 | 插件选择 **17 项通过**，新增无关 Agent 丢失时拒绝候选；保留原有加载/组件/源漂移/命名空间冲突与权限 ask 检查 |
| Runtime | 新增 **7 场景**：正常释放、包重现、别名重现、hook 重现、MCP 重现、清单缺失、未提交；相关 Claude 测试通过 |
| Application 故障契约 | 插件改配及普通 MCP 改配分别在丢回执后核对一次持久结果、保持其他绑定并跨重启恢复；使用既有 Runtime 夹具，不算原生故障注入 |
| 浏览器 | 新增 **14**、升级 **16**、继承 **15** 项通过，验证请求、版本选择、预览 hash、候选、暂停、原 ID、历史和移动布局；使用真实 Application 转换导出，HTTP/SSE 为受控回放 |
| 回归/构建 | 原生 Skill 改配 **32**、hook SDK **5**、认证恢复 **10**、planner-only **6** 项通过；app/console 全包 race、Claude Runtime race、nativeclaude 标签 vet、开发构建与 Python AST 通过 |

新路径先在真实 Session 复现“CLI 插件改配尚未接通”，Application 预览也先失败后修复。继承 Agent 丢失及撤下组件重新出现均先观察错误接纳，再补拒绝检查。原始红灯日志保留。

补测应用重启时初始断言错误地认为 `resume` 立即开启原生查询，真实结果为身份/版本恢复但当前插件未核对。保留 [首次重启补测](W02-08-plugin-change/app-native-final.log)，按已有契约改为检查“不冒充已加载”，再通过冷启动改配实际开启原查询并核对目标；[最终 45 项](W02-08-plugin-change/app-native-cold.log)通过。没有为满足测试而修改 resume 行为。一次 TS 检查指出可选 plugin_name，补上空值处理后构建通过，首次失败日志也保留。

## 证据位置

- 原生 Session 红灯：`/tmp/netcap-w0208-plugin-change-fqbc40oa`；最小禁用绿灯：`/tmp/netcap-w0208-plugin-change-wbxai_6k`；完整 30 项：`/tmp/netcap-w0208-plugin-change-qhmnvl6x`。
- Application 首次 37 项及浏览器转换：`/tmp/netcap-w0208-plugin-app-860245702`；重启补测失败：`/tmp/netcap-w0208-plugin-app-2193091129`；最终 45 项：`/tmp/netcap-w0208-plugin-app-1841448488`。
- 源码前置备份与本轮日志：`/tmp/netcap-w0208-plugin-change-work`。

归档：[Session 结果](W02-08-plugin-change/session-result.json)、[Application 结果](W02-08-plugin-change/app-native-result.json)、[真实界面转换数据](W02-08-plugin-change/change-browser-responses.json)、[新增界面](W02-08-plugin-change/browser-add.json)、[升级界面](W02-08-plugin-change/browser-upgrade.json)、[继承界面](W02-08-plugin-change/browser-inherit.json)。截图：[升级 1440](W02-08-plugin-change/upgrade-1440.png)、[继承 390](W02-08-plugin-change/inherit-390.png)。浏览器已关闭，所有本片执行句柄已收取。

## 复跑与未完成验收

```bash
npm --prefix bridges/claude run build
python3 tests/claude_plugin_change_native.py
node --experimental-test-module-mocks tests/claude_plugin_selection_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags nativeclaude ./internal/app -run '^TestClaudeNativeApplicationPluginChanges$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 tests/claude_plugin_change_smoke.py --planner-only
```

浏览器脚本 `tests/claude_plugin_change_browser.js` 以 `change-browser-responses.json` 替换 `__FIXTURE__`，`web/dist` 文件映射替换 `__ASSETS__`，以 `add`、`upgrade`、`inherit` 替换 `__CASE__` 后通过配置的 Playwright 执行。回放不新增原生工作。

完整探针 `python3 tests/claude_plugin_change_smoke.py` 已准备并请求用户在可监听的 WSL 终端执行：两套隔离配置、本地模拟供应商、真实 MCP 返回插件版本、Pre/Post hook 目录、逐次审批、禁用/释放、原 ID 带模型历史恢复，以及当前二进制运行前后 hash。**目前只有 planner 的 6 项通过，完整探针没有结果，不算已验收**。没有重试或绕过代理的已知监听/IPC 限制。

本片源码与构建见 [manifest](W02-08-plugin-change/manifest.json)，[差异](W02-08-plugin-change/changes.diff)相对本片前置备份生成；旧 `bin/netcap` 和旧证据保持不变，未提交。

下一步收到终端结果后独立核对实际版本化调用、审批、hook 和原 ID 历史，补齐改配的完整工具轮次。完整受管/已安装来源、产品 hook 来源关联、Codex 插件、更多后台/故障恢复及其他 W 系列硬要求均保留。
