# W02.08 · 普通本地设置自动应用阻止机制

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 生产结果

新创建的普通 Claude 执行记录 `claude_local_settings.guard_version=1`，同时绑定到 Scope 和持久原生配置的 `settings_guard_version`。创建、观察和改配核对该版本；旧记录省略字段时保留原行为，不补造阻止机制。执行配置快照和原文件版本检查继续保持原先语义。

生产 Session 使用公开 SDK `ConfigChange` hooks，仅匹配 `project_settings|local_settings`，返回 `decision:block`。记录来源、路径、时间及 `block_requested`，不复制配置正文或回调任意字段。阻止决策不依赖诊断持久化：日志写入抛错会设置当前查询故障，仍返回阻止；连诊断错误处理本身抛错也不会传播成放行。记录写入沿用 fsync journal，在恢复时保留历史。

原生 hook 策略与当前连接单独采样。策略可用时显示“已注册”，全部关闭、managed-only、plugin-only、安全/bare 模式、来源解析/读取问题或日志失败时显示不可用；断线显示未核对。界面展示最近 16 条历史决策，错误版本的记录不显示。**“已请求阻止”不等于收到全部配置冻结确认**，没有用注册状态放宽任何文件来源检查或改配准入。

最终真实 Application/SQLite/Runtime/Session 路径中，主仓库 local 从 v1 改为 v2 后写入具体路径的决策记录，Scope/快照/ID 不变，来源核对仍为 drifted，send 被同步拒绝；正常关闭实际执行 v1 SessionEnd，且没有模型输入。此前无阻止机制的生产 Session 长窗口会执行 v2，失败依据保留在[上一片](W02-08-claude-settings-lifecycle.md)。

## 边界与验证

| 对象 | 实际结果 |
|---|---|
| 真实生产链路 | [最终 10 项](W02-08-claude-settings-guard/native-final.json)通过，包含原生关闭后的独立标记。首轮 fixture 在首次观察前读取空 View，`guard_registered` 断言失败；[初轮](W02-08-claude-settings-guard/native-initial.json)保留，等待首次证据后通过。 |
| 删除、原子替换、链接目标变化、新增缺失目录/文件 | 各 **6 项**真实 SDK 对照通过；准确 local callback、原 hook 和原环境保持。见 [delete](W02-08-claude-settings-guard/boundaries/delete/result.json)、[replace](W02-08-claude-settings-guard/boundaries/replace/result.json)、[symlink](W02-08-claude-settings-guard/boundaries/symlink/result.json)、[add](W02-08-claude-settings-guard/boundaries/add/result.json)。这些边界仍是 SDK 候选探针，不冒称全部经过 Application。 |
| hooks 全部关闭 | [5/6、退出 1](W02-08-claude-settings-guard/boundaries/disabled/result.json)：没有 callback。该样本的 hooks 关闭状态保持，但不能据此声称已收到阻止回调；生产状态展示为不可用。 |
| 受管 only 注入尝试 | [4/6、退出 1](W02-08-claude-settings-guard/boundaries/managed-only/result.json)：flag 层 `allowManagedHooksOnly` **没有建立原生 managed_only 策略**，启动 hook 仍运行。不是实际企业受管环境验收，也不据此断言其忽略限制。 |
| 普通 callback 抛错 | [5/6、退出 1](W02-08-claude-settings-guard/boundaries/callback-error/result.json)：原生继续采用 v2 并执行新版 end，证明不能让诊断异常从阻止 callback 逃出。 |
| 生产 callback 的诊断失败 | [真实 SDK 6 项](W02-08-claude-settings-guard/boundaries/diagnostic-error/result.json)通过：调用新生产模块，注入诊断记录异常，错误处理标记发生且 v1 end 保留。没有使用 Session 私有字段注入。 |
| 单元与契约 | [Node 6 项](W02-08-claude-settings-guard/callback-final.log)通过，覆盖诊断及其错误处理抛错、身份异常、不复制额外载荷、策略不可用、probe/未知版本拒绝。[Application 3 个顶层](W02-08-claude-settings-guard/app-binding-final.log)覆盖 guard 绑定、旧记录与旧文件版本不补造、错配拒绝输入。 |
| 浏览器 | [15 项](W02-08-claude-settings-guard/browser.json)通过：真实 Application 响应回放，策略/版本/断线变化为受控界面输入；桌面和窄屏截图已检查，无横向溢出，无执行命令。 |
| 独立核对 | [27 项](W02-08-claude-settings-guard/audit.json)通过，从 SQLite 投影、bridge metadata、journal、原生脚本标记及原始 SDK 边界结果重新核对；不以 producer 的通过字段充当依据。 |
| 回归与工程检查 | Skill 原 ID 改配 [37 项](W02-08-claude-settings-guard/skill-regression.json)、插件选择 [9 项](W02-08-claude-settings-guard/plugin-regression.json)通过；app/console 全包 race、Claude Runtime race、tagged nativeclaude vet、bridge TypeScript 构建、Web typecheck/Vite 与 Go 开发构建通过。 |

`node --test` 的首个运行在此进程隔离环境下只返回 test runner 启动失败，未执行到断言，保存在 [callback-red.log](W02-08-claude-settings-guard/callback-red.log)；改为 `node tests/claude_settings_guard_test.mjs` 后实际运行 6 项。不把该启动失败当作生产回调的原始红灯；真正的失败对照是上述 SDK callback-error 与上一片无 guard 的原生行为。

## 尚未完成

仍未实现同 ID 明确采纳新本地设置；普通能力候选仍保留旧本地版本。注册状态不提供解除漂移或恢复输入的依据。受管文件策略、注册表/服务端策略、hooks 被限制的真实环境、SDK/CLI 通道失联与超时、在途模型任务、全部设置字段、源文件关联脚本及其他资源都没有因此固定。callback 可以与原有 ConfigChange hook 并存，原用户 hook 仍会执行；不会取消其已发生副作用。

当前代理的外部 command hook stdin 仍为空，边界脚本只凭实际标记核对作用；SDK callback 有准确原生输入。没有声称独立 command hook 的完整载荷问题已修复，五项既有 WSL 请求继续分别待结果。完整方法/Gate、原生丢确认恢复、输入独占、Windows/proxy 和其他 hook 硬要求不变。

## 复跑与产物

```bash
npm --prefix bridges/claude run build
node tests/claude_settings_guard_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app -run '^TestClaudeNativeApplication(SkillChanges|PluginSelection|SettingsGuard)$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 tests/claude_settings_lifecycle_probe.py --case local --sdk-hook block --guard-case diagnostic-error
python3 goals_W/evidence/W02-08-claude-settings-guard/audit.py
```

其他 `--guard-case` 为 `disabled / managed-only-flag / callback-error / delete / replace / symlink / add`。前三个保留未满足的前置断言，退出 1；其余本轮退出 0。固定 CLI **2.1.280**、SDK **0.3.280**，未换依赖。

浏览器 [模板](../../tests/claude_settings_guard_browser.js)的 `__FIXTURE__` 使用 [browser-responses.json](W02-08-claude-settings-guard/browser-responses.json)，`__ASSETS__` 映射当前 `web/dist`；[已执行脚本](W02-08-claude-settings-guard/browser.js)、[桌面](W02-08-claude-settings-guard/guard-1440.png)、[窄屏](W02-08-claude-settings-guard/guard-390.png)分别归档。没有生产 HTTP listener 或 Windows 验收。

最终生产原始目录 `/tmp/netcap-w0208-settings-guard-app-4226569621`，其他目录见[记录](W02-08-claude-settings-guard/original-directories.json)。自有配置/项目/状态与测试凭据隔离，loopback 供应商不可用；没有用户输入或模型结果。所有命令句柄和浏览器均已结束，没有共享后台、项目提交或对外发布。

- 开发二进制：`a588ac53586d0335c6641fff6f11d3e710868329c968e54aae54239f6f4c9b3a`。
- 旧 bin 未改：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。
- **3653** 个既有 W 证据逐文件 hash 不变。
- [source.diff](W02-08-claude-settings-guard/source.diff)和[manifest.json](W02-08-claude-settings-guard/manifest.json)标记本次源码、构建与归档版本；不复制完整数据库或凭据文件。

下一片以当前绑定的普通本地设置为范围，继续明确采纳的差异预览、候选与原 ID 应用，保留上述真实受管和故障准入条件，不将其缩减为仅改保存 hash。
