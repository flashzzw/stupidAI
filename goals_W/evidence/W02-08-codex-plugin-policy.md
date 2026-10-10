# W02.08 · Codex 混合插件的原生禁用策略

2026-10-05。固定 Codex app-server 0.158.0，使用隔离 profile、两个原生会话和预置的 Skill/MCP/hook 插件。新增[可重跑探针](../../tests/codex_plugin_policy_probe.py)，最终 **112 项检查通过**。本片确认逐轮排除、保存策略、原 ID 恢复顺序及只读日志依据；未修改产品实现或构建。W02.08 in_progress，整体 9/40 done，Goal active。

## 原生契约与实际行为

完整 experimental 类型仍在 [ThreadResumeResponse](W02-08-codex-plugin-policy/schema/ThreadResumeResponse.ts) 中注释 `Saved list of disabled plugin IDs. Does not yet filter plugin capabilities.`。本次真实 0.158.0 已有过滤行为，不能依据这条旧注释判定接口无效；也不将固定版本的观察扩展到其他版本。

| 操作 | 本轮取得的依据 |
|---|---|
| `turn/start` 显式传禁用列表 | 同一轮排除插件 Skill 描述、新的显式正文载入、MCP 以及首轮 SessionStart hook；配置中的插件仍为 enabled |
| 下一轮从列表撤去 fixture ID | 原 ID 恢复 fixture 的描述、正文载入与 MCP；保留 retained 插件禁用 |
| `thread/settings/update` 保存列表 | 原生回执成功，日志保存新策略；此时当前 MCP inventory 仍包含刚被禁用的 fixture |
| 随后 `turn/start` 省略列表 | 采用保存的列表，新一轮排除 fixture 描述、正文与 MCP |
| 保留禁用列表，退订后按原 ID 显式 resume config | fixture MCP 仍被排除；传入启用配置不能清掉保存的禁用策略 |
| 先从保存列表撤去 fixture ID，再退订/resume | fixture MCP 在没有新增输入的情况下恢复，retained 仍禁用 |
| 两线程及后台冷恢复 | A/B 保存各自列表，B 的变更不影响 A；原 ID 和各自策略保持，省略列表仍生效 |

[TurnStartParams](W02-08-codex-plugin-policy/schema/TurnStartParams.ts) 与 [ThreadSettingsUpdateParams](W02-08-codex-plugin-policy/schema/ThreadSettingsUpdateParams.ts) 明确规定该列表是**整体替换**：省略/null 保留，空数组清空。本轮实际对照传入两项、再只传 retained，验证 fixture 被撤去而 retained 保留；未单独测试显式 null 和清空数组。生产合并必须保留不属于当前选择的 ID，不能用 `[]` 代替未知或未报告的列表。

`ThreadResumeParams` 不接收此字段，start/resume 回执报告保存列表。`skills/list` 和 `hooks/list` 仍不能提供逐线程加载依据，`config/read` 也没有 threadId。本片保留相应固定二进制生成类型；这些附件是独立类型摘录，不是可单独编译的 SDK。

## 无输入的持久恢复依据

原生 rollout 的 `turn_context.disabled_plugin_ids` 记录该轮策略；`event_msg` 中 `thread_settings_applied` 的 `thread_settings.disabled_plugin_ids` 保存后续设置。探针按文件顺序读取最后一项，不把缺失字段当空列表。正式六轮的日志策略与随后 resume 回执一致。

另在 settings/update 成功后、任何 resume 或下一轮输入之前，仅调用 `thread/read` 取得已物化线程的日志路径，检查自有日志中的 `thread_settings_applied`：立即禁用和解除禁用两条路径都能取得新列表。因此恢复核对有可用的只读证据来源，无需为了查询列表而重新加载线程或重放设置。记录可能异步写入，探针最多等待 2 秒；这不是跨客户端原子策略锁。生产读取仍需沿用已实现的文件范围、身份、版本、完整性和并发变化检查，不能只复制本探针的自有目录读取方式。

附件保留[设置后尚未输入的策略](W02-08-codex-plugin-policy/final/queued-before-turn-policy.json)、[对应旧 MCP 清单](W02-08-codex-plugin-policy/final/queued-before-turn.json)、[解除禁用后的日志](W02-08-codex-plugin-policy/final/cleared-before-resume-policy.json)、[带旧禁用恢复](W02-08-codex-plugin-policy/final/resume-with-saved-disabled.json)和[解除禁用后恢复](W02-08-codex-plugin-policy/final/resume-after-policy-clear.json)。保存策略、当前 MCP 连接和某轮准备的 Skill 上下文有不同的生效时点，不能互相替代。

## 验证强度与限制

fixture 包有一个 Skill、一个 stdio MCP 和可信 `/usr/bin/true` SessionStart hook；retained 包有 Skill/MCP，并在全部输入中保持禁用。项目 Skill 始终保留。所有 plugin config 均保持 enabled，排除由 disabledPluginIds 驱动；插件在首次禁用输入前可能已经初始化 MCP，本片不证明禁用时完全没有启动副作用。

最终六轮均尝试不可用的 `http://127.0.0.1:9/v1`，观察到原生连接错误后显式 interrupt，全部终态为 interrupted。没有成功模型响应、工具执行或方法/Gate 验收。MCP 仅 initialize/list。两个自有后台退出 0，没有安装、卸载、全局配置写入、监听器或共享后台切换。

启用和恢复 A 的 hook 均触发并报告超时失败，禁用首轮无 hook；`hook_success_observed=false`。**112 项通过仅覆盖策略过滤和可观察终态，不代表 hook 成功执行**，此前已定位的成功执行缺口仍保留。新禁用阻止新的 Skill 正文载入，不删除历史中已经载入的正文，也不阻止其他获准文件读取路径；完整文件访问权限需独立验收。

## 复现、归档与下一片

```bash
python3 tests/codex_plugin_policy_probe.py
```

最终原始目录：`/tmp/netcap-w0208-plugin-policy-h8qaf999`。附件包含[最终结果](W02-08-codex-plugin-policy/final/result.json)、[初始协议](W02-08-codex-plugin-policy/final/initial-protocol.json)、[恢复协议](W02-08-codex-plugin-policy/final/restored-protocol.json)、六轮原始 rollout/上下文与策略摘要、MCP 记录、[差异](W02-08-codex-plugin-policy/changes.diff)、[审计](W02-08-codex-plugin-policy/audit.json)及[清单](W02-08-codex-plugin-policy/manifest.json)。归档中的 installationId/宿主标识脱敏。

前期直接 turn 策略、settings 策略、resume 顺序探索，及正式 84 项与 95 项中间结果保存在 exploration；后续 112 项正式结果包含对应路径，不累计为额外验收数量。探索版 context probe 为收集全量观测而不逐项立即抛出断言，不能作为独立正式验收。Python AST、附件完整性、文档引用和变更范围检查通过；未修改 Go/界面，因此没有重跑其测试或构建。

1705 个旧证据和两个二进制保持不变。开发构建仍为 `4332c78de3c3f22ad082df20efaca7b970f6fe33a19495e849ff2a1d041d1cda`；旧 bin 为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。未提交、推送或发布。

下一片把只读保存策略、输入时列表合并及原 ID 改配接到生产混合组件选择：初始创建必须保留真实回执列表，缺字段不能假定空；发送与异步回答的新轮次都需处理策略；改配回执丢失只读核对日志和当前组件，不重放。生产目前仍仅允许 MCP 插件，不能仅凭 MCP inventory 声称 Skill/hook 均已生效或排除。受管/安装来源、完整方法/权限、成功 hook/异步、生产 proxy/peer、Windows 和跨客户端输入独占等硬要求仍未完成，既有 WSL 待返回结果不重复请求。
