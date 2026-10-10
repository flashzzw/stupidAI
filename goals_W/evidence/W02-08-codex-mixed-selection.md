# W02.08 · Codex 本地 Skill/MCP 混合插件选配

2026-10-05。新建与原 ID 改配现可选择同时包含标准 Skill 目录和 MCP 的本地 Codex 插件。Application/快照、Runtime、原生线程及界面贯通；固定 app-server 0.158.0 的最终 **57 项原生检查通过**，混合插件界面 **17 项通过**。W02.08 仍为 in_progress，整体 9/40 done，Goal active。

## 用户可见行为

插件仍作为一个能力资产整体引用，保存完整文件集合、权限、资源摘要及各会话独立缓存来源。新增支持 `skills/<名称>/SKILL.md`，目录名与 frontmatter name 一致，描述需有效；同包至少包含一个可核对 MCP。仅含 Skill、非标准 Skill 路径、hooks 和其他尚未核对组件继续拒绝，不将这一片称为完整 Codex 插件支持。

可以新建启用/禁用、原 ID 启停、明确升级、恢复继承和重新添加。保存候选不改变当前快照；准备阶段保持只读并暂停输入；应用阶段更新整包策略、取消当前连接订阅，再按原 ID 恢复目标配置。共享资产更新不自动升级旧执行，另一会话继续保留自身版本。恢复继承撤去显式资产引用，已撤下的私有版本 ID 保留禁用，防止仍持有旧配置的客户端重新带入该私有包；自然继承来源不按同名误删。

界面分别显示 Skill 与 MCP 组件，混合包的核对文案为“固定插件包、MCP 与整包策略已核对”。新增 `PluginEvidence.coverage=cached_package_and_thread_policy`，明确它是输入前的选择准备依据；不再从 MCP 清单声称所有 Skill 已加载。某轮实际准备的 Skill 描述/正文仍由已有“Skill 上下文记录”显示，方法使用和成功工具调用分别需要真实结果。

## 原生策略与恢复

沿用[原生策略契约](W02-08-codex-plugin-policy.md)，start 回执保存真实 `disabledPluginIds`，缺失、null、重复或无效列表不会按空列表处理。新建混合包在需要时明确保存整包禁用列表；未物化线程只可使用同连接的真实创建/设置回执，并跟踪 `thread/settings/updated`。连接、身份或权限不一致即放弃此依据。

已物化线程从原日志按顺序读取 `turn_context` 和 `thread_settings_applied` 的保存策略。与 Skill 上下文共用受限读取入口：限定保存状态目录的 sessions/archived_sessions，核对原生 ID/cwd/provider、固定日志版本、普通文件、目录与文件身份、完整末行和大小上限。日志一旦物化，缺失或损坏时不会回退到旧创建回执。未物化路径也重新核对原生路径、目录身份及仍未出现日志，防止采样期间路径变化后使用旧回执。

普通发送和异步回答开启新轮次时，都显式传递经核对的完整禁用列表，保留无关插件 ID；进行中的异步回答只在当前选择策略仍匹配时 steer，不借回答修改策略。即使旧 MCP 仍 connected，只要混合包保存策略不匹配或缺失，也不放行输入。

原 ID 应用先更新保存策略，再退订/resume；恢复前后核对目标列表。过渡期间继续检查身份、历史和全部执行范围，不用旧包的清单要求否定刚应用的新策略。设置请求已发出后，后续失败均保留“可能已改变”的未知状态；丢失设置或恢复回执时，只读核对日志、目标 MCP 及撤下来源，不重放。仅含 MCP 的包若已有可读保存策略，也会处理旧禁用 ID，避免从混合包升级为 MCP 包后无法重新启用；旧 MCP 记录缺少策略时仍使用既有 MCP 清单契约。

这些核对不是跨客户端原子锁；独立客户端仍可能并发改变原生执行，完整输入独占继续保留为未完成条件。禁用也不会删除历史中已载入的正文，不能替代文件访问权限。

## 验证与实际结果

| 层次 | 结果与范围 |
|---|---|
| Application | 新增混合资产整体绑定测试红→绿；既有创建拒绝测试改为非标准 Skill 入口，hooks/重复 MCP 等继续拒绝。Application 全包 race 通过 |
| Runtime | 23 个新增场景覆盖创建策略、MCP 与策略不一致、普通输入、异步回答、无关列表保留、设置/恢复丢回执、纯 MCP 遗留禁用、日志缺失及 9 类无效/外来记录、新建设置事件及采样路径变化；相关 Codex/异步回归 race 通过 |
| 原生混合插件 | 最终 57 项通过；两个会话新建、启停、继承、重新添加、资产更新不升级旧执行、明确升级、SQLite/后台冷恢复后原 ID 继续输入；十轮逐轮核对实际描述与 interrupted 终态 |
| 既有原生回归 | 本地 MCP 插件改配 63 项、新建选择 28 项通过；此两组仍使用合成历史、零模型输入的原有契约 |
| 界面 | 当前构建回放实际 Application/Handler 输出：混合插件 17 项、既有 Codex 插件 18 项、Claude 插件 16 项通过；桌面/移动宽度、选配请求、来源、覆盖说明及断连状态核对 |
| 其他 | Console 全包 race、相关 vet、开发构建、文档引用/差异/归档检查通过 |

原生混合插件 **十轮**均只尝试不可用的 `127.0.0.1:9`，观察连接错误后由 Application 显式中止；没有成功模型响应、工具执行或 hook。这里实际准备了 Skill 描述，没有证明正文被模型使用或方法/Gate 已执行。三个自有后台退出 0；没有改用户 profile、安装/卸载插件、切换共享后台或打开测试 listener。浏览器 HTTP/SSE 和创建回执受控，界面回放不算另一次原生调用。

保留的失败包括：Application 原先拒绝混合包；Runtime 原先只看 MCP、没有保存/清除策略；已物化日志缺失和新建日志路径变化错误回退到创建回执；初版原生测试仅等待 idle 而未等待插件观察回写，导致两个创建断言过早。另有一次新增测试括号错误，已修正。Runtime 全包 race 触及已有 OpenCode listener 测试，在当前代理环境因 `socket: operation not permitted` 失败；随后仅重跑相关 Codex/异步测试并通过，不将全包 Runtime 标为通过，也未重试该监听限制。

最终原生目录：`/tmp/netcap-w0208-mixed-plugin-2550416634`。界面使用同一产品实现较早一轮 `/tmp/netcap-w0208-mixed-plugin-2839073045` 的真实 Handler 导出；之后补了冷恢复输入和未物化路径检查，前端产物未变。原生最终验证后仅移除了未使用的私有 Source 字段，新增 Runtime 场景及构建再通过，无新增行为。

## 复现与附件

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^TestCodexNativeMixedPluginSelectionAndChanges$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime \
  -run '^(TestCodex|TestAsyncQuestion)' -count=1
```

附件包含[最终结果及原生协议](W02-08-codex-mixed-selection/native/result.json)、[原始日志](W02-08-codex-mixed-selection/native/rollouts/)、[界面结果](W02-08-codex-mixed-selection/browser-mixed.json)、[桌面截图](W02-08-codex-mixed-selection/desktop.png)、[移动截图](W02-08-codex-mixed-selection/mobile.png)、[差异](W02-08-codex-mixed-selection/changes.diff)、[审计](W02-08-codex-mixed-selection/audit.json)和[清单](W02-08-codex-mixed-selection/manifest.json)。测试入口为 [Runtime/Application 原生测试](../../internal/runtime/codex_mixed_plugins_native_test.go)与[浏览器脚本](../../tests/codex_mixed_plugin_browser.js)。归档中的 installationId/宿主标识脱敏。

1810 个既有证据文件与旧 `bin/netcap` 保持不变；开发构建 SHA256 为 `35a8bc4d1c9f8fe66baa3253f5e00e6b398903f460902aaaaa3cc7a840d6f92f`。未提交、推送或发布。下一片继续完整组件、Skill 方法/实际使用关联和权限；仅含 Skill/自定义根、hook 成功及异步、受管/安装来源、Windows、生产 proxy/peer 与跨客户端输入独占仍未完成，既有 WSL 待返回结果不重复请求。
