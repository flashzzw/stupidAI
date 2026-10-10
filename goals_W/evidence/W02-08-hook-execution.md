# W02.08 · 原生 hook 执行、失败与恢复记录

日期：2026-10-02。W02.08 **in_progress**，整体 **9/40 done**，Goal active。本片增加实际 hook 运行记录，不将已登记组件清单当作执行成功。

## 本片行为

固定 SDK 的 `includeHookEvents` 接入原生 hook 生命周期。Session → Runtime → Application/SQLite → 执行详情展示调用 ID、事件、原操作、开始/结束时间、结果及退出码。启动 hook 没有模型操作时保持空关联；模型结果之后才到达的 hook 回执沿调用 ID 保留原操作。

新 bridge hook 事件在写日志前省略 `hook_name`、stdout、stderr 和 output 正文；读取旧 journal 和返回 hook 历史时同样在内存中省略这些字段，不重写旧文件。保留原生 ID、事件类型、终态与退出码。此规则只针对 hook 事件，不声称清理其他原生消息或用户已有日志。

未结束的 hook 阻止新输入和配置准备，关闭/恢复不将其视为已完成；缺少开始事件、断连或无法完整核对的生命周期显示终态待核对。成功、失败、取消均保留实际终态，结束后释放该项占用。启动 hook 失败不擅自改写为模型失败：此次原生 CLI 在退出 2 后仍允许保持查询，界面同时显示加载清单和执行失败。

**原生事件没有插件来源键**，只有调用 ID 和 `SessionStart:startup` 一类名称。产品保存 `source: unreported`，不根据同名事件或当前插件列表猜测来源。完整组件归属追踪仍是未完成要求。

## 原生实验及证据边界

CLI **2.1.280**、SDK **0.3.280**。使用独立临时配置、项目、状态和本地插件；SessionStart 命令仅写自己的临时记录并输出测试标记，成功分支退出 0，失败分支退出 2。未调用模型、未开启 HTTP 监听、未执行 MCP 工具。

最初夹具要求从 stdin 读取 JSON，实际收到空内容，因而两个已启用分支均退出 1。原始失败保留在 [首次结果](W02-08-hook-execution/native-initial-result.json)。这证明 hook 进程已执行，不能证明载荷交付。随后使用无需载荷的命令，仅记录 stdin 字节数与自身插件目录，不从命令参数补造原生 session/cwd 信息；[受控结果](W02-08-hook-execution/native-controlled-result.json)中 stdin 仍为 0，成功 0/失败 2/禁用无执行分别成立。

本片没有重复探测或绕过已知 IPC 限制，也没有把无需载荷的命令结果提升为完整 hook 输入契约。真实工具轮次中的 PreToolUse/PostToolUse、载荷、插件来源和 MCP 仍需独立验证。

## 实际验证

| 层次 | 结果 | 证据 |
|---|---|---|
| 原生 Session/CLI | 13 项：成功/失败/禁用、调用 ID 连续、零模型输入/结果、新输出脱敏 | [原生结果](W02-08-hook-execution/native-result.json) |
| Application/SQLite/Runtime/CLI | 17 项：A 成功、B 失败、退出码、来源未知、无原始输出、独立目录、重启保留 hook 历史与插件绑定、来源漂移拒绝 | [应用结果](W02-08-hook-execution/app-native-result.json) |
| Session SDK/内存进程契约 | 5 项：启用事件与脱敏、未结束占用、关闭/恢复、缺少开始、迟到回执归属 | [最终日志](W02-08-hook-execution/session-final.log) |
| Runtime | 8 场景：执行中、成功、失败、取消、断连、缺少开始、其他会话、重复回执 | [Runtime 日志](W02-08-hook-execution/runtime-green.log) |
| 浏览器 | 18 项：真实成功/失败/ID/退出码/来源说明、会话切换、移动布局、合成执行中/未知状态及占用 | [浏览器结果](W02-08-hook-execution/browser.json)、[实际应用响应](W02-08-hook-execution/app-browser-responses.json) |
| 回归与构建 | 原生插件选择 14、原生 Skill 改配 32、插件 SDK 15、认证恢复 10、hook 清单 7 项通过；app/console 全包 race、Claude Runtime race、vet、bridge/web/dev build 通过 | 本目录 `*-regression.log`、[全包 race](W02-08-hook-execution/app-console-race.log)、[Runtime race](W02-08-hook-execution/runtime-race.log)、[开发构建](W02-08-hook-execution/dev-build.log) |

SDK 的未结束、缺少开始和迟到事件测试都先复现失败再修复。一次测试清理顺序导致已删除目录上的 close 失败，调整为先关闭恢复对象后再清理；原日志保留。浏览器准备阶段修正了一次 Python 路径映射括号错误和执行环境缺少 `structuredClone`，随后使用现有 JSON 克隆方式完成测试；不计为产品失败。

浏览器成功/失败来源于真实应用导出；HTTP/EventSource 为受控回放，执行中与未知两种状态明确使用合成数据。没有将浏览器回放计为真实工具执行。截图：[1440](W02-08-hook-execution/failure-1440.png)、[390](W02-08-hook-execution/failure-390.png)。浏览器已关闭。

原始目录：

- 首次载荷失败：`/tmp/netcap-w0208-hook-lifecycle-6otznchq`。
- 受控退出码：`/tmp/netcap-w0208-hook-lifecycle-kfgt1p2a`。
- 最终原生：`/tmp/netcap-w0208-hook-lifecycle-tgl_bz52`。
- 最终 Application：`/tmp/netcap-w0208-plugin-app-1032114774`。
- 本片源码前置备份与日志：`/tmp/netcap-w0208-hooks-exec-work`。

## 复跑与产物

```bash
npm --prefix bridges/claude run build
python3 tests/claude_plugin_hook_lifecycle_native.py
node --experimental-test-module-mocks tests/claude_hook_execution_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags nativeclaude ./internal/app -run '^TestClaudeNativeApplicationHookExecutions$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器脚本 `tests/claude_hook_execution_browser.js` 使用应用导出的 `browser-responses.json` 替换 `__FIXTURE__`，使用本地 `web/dist` 路径映射替换 `__ASSETS__`，通过已配置 Playwright 执行。

开发二进制 SHA-256：`9d22a519e358ddea6a0f8169fd84a077a7b6dd1d1f4ce784fd0073c06724e8cd`。旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。

[最终差异](W02-08-hook-execution/changes.diff)按本片开始时源码备份生成；[manifest](W02-08-hook-execution/manifest.json)记录源码/构建/证据哈希，本片开始时纳入的 1079 个旧证据文件均未修改。未提交代码，未改共享后台或用户配置。

## 后续

继续真实工具轮次 hooks 和插件 MCP 调用/审批/拒绝/结果归属，补齐载荷及完整来源关联，再推进插件原 ID 版本事务与 Codex 选择。独立 hook 的直接停止、真实长运行/后台 hook、受管来源、原生事件不完整时的恢复均不能由本片合成测试外推。W01/W02 和完整 W 系列硬要求保持不变。
