# W02.08 · Claude 配置失败诊断

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 用户可见结果

未确认的 Claude 配置应用可在候选旁显示“配置应用失败记录”：失败阶段、固定原因、记录时间、关联操作和自有查询退出核对结果。无效设置 schema 显示原生解析错误；候选启动期间来源变化显示启动候选配置失败。原 ID、原快照、候选与暂停不变，退出确认不表示已撤销启动 hook 等副作用。

浏览器只显示与当前候选的操作/历史基线匹配、版本和阶段合法的记录。空绑定、错配、未知版本/阶段/原因/退出状态均不冒充当前证据。断线时保留已保存诊断并注明状态仍待核对；原查询离线时不显示 guard 已注册。该入口不提供重试、回滚或清除未知操作。

## 实现与边界

Session 在配置事务的关闭、历史检查、候选启动、设置/组件/空闲/来源复核及提交阶段维护固定阶段码。捕获失败时先追加 `configuration_failure` journal 记录，确认自有查询退出后再追加退出已确认记录。诊断使用原 journal 的 fsync；独立于 `_configuration` 成功/待核对回执，不为记录错误原因改写成功结果或重新执行候选。若诊断写入失败，继续原清理流程，结果仍按原事务核对。

诊断字段限于版本、操作、历史基线、阶段、固定原因、退出状态和时间，不复制异常原文、配置/脚本正文、工具输出或凭据。读取时再检查格式并仅复制白名单字段。Runtime 同时核对原生身份、原操作、历史基线、目标及原版本绑定、当前原生配置是否对应原版本或候选；只有准确匹配的未完成事务才附带具体诊断。返回状态仍为 `unknown`，不提升 `ExecutionClear` 或完整性。中文原因由本地固定映射产生。

本轮发现并修复了新增诊断校验的一处遗漏：如果持久事务缺少目标设置数组，但当前原生配置恰似候选，原校验会接受诊断。新增负例先失败，现要求原设置采纳操作具有完整目标绑定。见 [missing-target-red.log](W02-08-claude-configuration-failure/missing-target-red.log)及最终 Runtime 测试。

## 实际验证

| 对象 | 结果 |
|---|---|
| 原始症状 | [native-red.log](W02-08-claude-configuration-failure/native-red.log)：启动后漂移与无效 schema 均缺少失败阶段，运行中和 App 重启后的诊断断言失败。原 ID/未知保护原已存在，不将其描述为本轮新增。 |
| 候选启动后来源漂移 | [15 项](W02-08-claude-configuration-failure/startup/result.json)通过。v2 启动 hook 已执行并将文件改 v3，持久诊断为 `open_candidate/stage_failed`，退出确认后仍 `uncertain/applying`，重启不晋升或重放。 |
| 候选 schema 无效 | [12 项](W02-08-claude-configuration-failure/schema/result.json)通过。`check_settings/settings_parse_errors`，仅原 v1 hooks 实际执行，保留候选和未知状态，App 重启保留同一诊断。 |
| 界面 | [schema 22 项](W02-08-claude-configuration-failure/schema-browser-final.json)、[startup 22 项](W02-08-claude-configuration-failure/startup-browser-final.json)通过。真实 App 恢复后数据回放，覆盖绑定/空绑定/版本/阶段/原因/退出状态、断线历史、禁止输入/应用/撤销以及窄屏。HTTP/SSE 与异常 DTO 变化为回放，零执行命令。 |
| 独立核对 | [44 项](W02-08-claude-configuration-failure/audit.json)通过：SQLite 投影、原生 metadata/journal、实际 hooks、修复前记录和浏览器命令重新比较；不以 producer passed 字段为依据。 |
| Runtime 边界 | [16 类](W02-08-claude-configuration-failure/runtime-binding-final.log)通过，包含原/候选配置、操作/历史基线/空值、版本/原因/退出状态/时间、目标/原绑定与原生身份错配；诊断不能授予输入权。 |
| Node | [诊断格式 2 项](W02-08-claude-configuration-failure/diagnostic-node.log)、[Session 插件及诊断 19 项](W02-08-claude-configuration-failure/session-node.log)通过，含公开 Session 操作抛出含敏感标记的错误后不入库、恢复保留诊断、journal 路径不可写时不误报安全未发送。后者为受控文件路径故障，不是实际磁盘损坏或断电演练。 |
| 回归 | 其他七类设置场景 **96 项**；Skill 改配 **37**、插件选择 **9**、guard **10** 项通过，见 [native-final.log](W02-08-claude-configuration-failure/native-final.log)与对应目录结果。九类设置场景合计 **123 项**，不累计重复运行。 |
| 工程检查 | app/console 全包 race、Claude Runtime race、tagged app/runtime/console vet、bridge 构建、Web typecheck/Vite 和开发 Go 构建通过。最终空绑定防护另跑 Runtime 16 类及最终构建，浏览器用最终 Web 资源重新验收。 |

旧记录没有诊断时保留待核对，不补造历史。Node 测试另验证原有 capability-only `not_sent` 事务仍保留其结果；本片生产界面主要覆盖仍为 `applying` 的设置事务，没有据诊断扩张其他失败的恢复能力。

## 复跑与产物

```bash
npm --prefix bridges/claude run build
node tests/claude_configuration_failure_test.mjs
node --experimental-test-module-mocks tests/claude_plugin_selection_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app -run '^TestClaudeNative(SettingsAdoption.*|Application(SkillChanges|PluginSelection|SettingsGuard))$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 goals_W/evidence/W02-08-claude-configuration-failure/audit.py
```

固定 Claude **2.1.280**、SDK **0.3.280**，独立项目/profile/状态目录、自有测试 hooks、不可用 loopback 供应商，零模型输入。原始目录在 [original-directories.json](W02-08-claude-configuration-failure/original-directories.json)。SQLite 仅保留必要 JSON 投影，不复制完整数据库或凭据文件。

浏览器[模板](../../tests/claude_configuration_failure_browser.js)分别使用 [schema fixture](W02-08-claude-configuration-failure/schema-browser-responses.json)和 [startup fixture](W02-08-claude-configuration-failure/startup-browser-responses.json)；[schema 桌面](W02-08-claude-configuration-failure/schema-1440.png)、[schema 窄屏](W02-08-claude-configuration-failure/schema-390.png)、[startup 桌面](W02-08-claude-configuration-failure/startup-1440.png)、[startup 窄屏](W02-08-claude-configuration-failure/startup-390.png)均已查看。查询/命令句柄和浏览器全部结束。

- 开发二进制：`a52afa0b7f884897df170b356ee91024a12ca98cfeb337cb0f802b284676db81`。
- 旧 bin：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，未改。
- **3927** 个旧 W 证据逐文件 hash 不变。
- [source.diff](W02-08-claude-configuration-failure/source.diff)及[manifest](W02-08-claude-configuration-failure/manifest.json)记录本轮源码、构建和证据时点。未提交、推送或发布。

## 剩余范围

诊断只解释已捕获的失败，不能证明所有副作用已撤销。进程在写诊断前崩溃时仍可能没有阶段记录；真实磁盘故障、通道失联/超时、原生进程内丢回执和显式失败恢复尚未据本轮收口。原子文件冻结、全部策略/受管/安装来源、真实方法/Gate、输入独占、Windows/proxy 等硬条件继续保留。五项既有 WSL 请求仍分别待结果。

下一片继续验证失败后的原生只读核对与明确恢复条件，保留原操作、历史和未知状态，不以清空候选或自动重试代替恢复。
