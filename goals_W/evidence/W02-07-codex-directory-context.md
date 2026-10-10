# W02.07 · Codex 普通目录 Skill 的逐轮选择与恢复契约

2026-10-05。固定 Codex app-server **0.158.0**，新增[隔离原生探针](../../tests/codex_directory_skill_context_probe.py)。用户/项目目录 Skill 的十二轮上下文、正文载入和恢复对照 **111 项通过**。本片取得生产接入所需的原生契约，未修改 Runtime/Application/界面或构建。W02.07 仍为 in_progress，整体 9/40 done，Goal active。

## 本轮确认的行为

沿用[原有选择参数探针](W02-07-skill-selection-contracts.md)，通过线程 config 的 `skills.config` 数组合并单项绝对入口路径与 enabled 状态，保留已有无关禁用项。此次由真实 rollout 的 `world_state.host_skills` 补丁还原当前描述，并按原生 turn ID 与 `skills.selected_skill_instructions` 分类核对本轮新增正文，不从完整历史字符串推断当前加载。

| 场景 | 实际观察 |
|---|---|
| 默认用户 Skill | 当前描述包含用户入口；显式 Skill 输入载入其正文 |
| 新线程禁用用户入口 | 用户描述和新的显式正文均缺席；项目描述、其他原生来源继续保留 |
| 新线程禁用项目入口 | 项目描述和新的显式项目正文均缺席；用户描述保留 |
| 同后台另一默认线程 | 不受其他线程选择影响；普通文本输入不主动载入测试正文 |
| 已加载线程热 `thread/resume` 带新禁用覆盖 | 请求成功，但下一轮仍保留旧描述，并可继续载入旧启用入口正文 |
| 先 `thread/unsubscribe`，再按原 ID `thread/resume` 带覆盖 | 下一轮采用新选择；启用与禁用两个方向均通过，历史保留 |
| 后台冷恢复，省略 Skill 覆盖 | 原 ID 与历史保留，但重新采用 profile 默认，原用户 Skill 禁用丢失，描述/正文重新出现 |
| 冷恢复明确重传完整覆盖 | 用户禁用、项目禁用、用户启用分别保持；原 ID 与各自历史轮数符合预期 |
| `skills/list` | 仍返回目录/profile 的默认启用状态，不能代替上述线程选择依据 |

这些行为由原生生成的上下文记录证明，**不证明模型收到上下文、遵循方法或 Gate 通过**。十二轮均只尝试不可用的 `127.0.0.1:9`，观察到连接错误后主动 interrupt，终态全部为 interrupted；没有成功模型响应、工具或 hook。两个自有 stdio 后台退出 0，没有 listener、安装/卸载或共享后台切换。profile、三个测试 Skill 文件及其内容保持不变。

## 对生产接入的影响

普通目录 Skill 与插件禁用列表不能共用“冷恢复自动保留”的假设。此前[插件策略证据](W02-08-codex-plugin-policy.md)证明 `disabledPluginIds` 持久保存；本次普通 Skill 的覆盖需要显式重传，不能只凭原 Session ID 或恢复回执称选择已恢复。

固定版本的 [ThreadResumeParams](W02-08-codex-plugin-policy/schema/ThreadResumeParams.ts) 接收通用 config，已归档的 [ThreadResumeResponse](W02-08-codex-plugin-policy/schema/ThreadResumeResponse.ts) 未列逐项 Skill 选择。本次实际回执的 `instructionSources` 只有项目 AGENTS.md；采集到的 `turn_context` 和 `thread_settings_applied.thread_settings` 也未包含 `skills.config`。这些已检查字段不能提供下一轮之前的逐项选择核对，不能把参数接受或旧上下文当作当前有效选择。

后续生产接入必须保留完整选择数组、绑定准确来源、在原 ID 改配/冷恢复时按已验证顺序应用，并处理回执丢失和输入前核对依据。普通目录选择与共享资源交付、同名/路径别名、受管来源及方法任务仍需继续；本片没有用额外模型输入充当用户会话的隐式加载检查。现有生产仍只支持已归档的本地插件 Skill 选择与显式输入。

## 验证与复跑

```bash
python3 tests/codex_directory_skill_context_probe.py
```

最终原始目录：`/tmp/netcap-w0207-directory-context-laxk6zo4`。四个线程分别覆盖默认、用户禁用、默认对照、项目禁用；十二轮中包含热恢复、退订应用及后台冷恢复。最终 111 个检查均通过。前期十轮 89 项用于收集热/冷恢复的实际差异，随后增加明确行为断言与项目来源对照；中间 93 项和最初观察日志保留，不累加为额外验收。

Python AST 解析、归档 JSON/JSONL、文档引用、差异与旧产物审计通过。没有产品改动，因此未重跑 Go/浏览器测试或重新构建。既有需要 listener 的完整供应商请求探针仍待 WSL 结果，这次上下文证据不替代该层。

附件：[最终结果](W02-07-codex-directory-context/native/result.json)、[初始协议](W02-07-codex-directory-context/native/initial-protocol.json)、[恢复协议](W02-07-codex-directory-context/native/restored-protocol.json)、[冷恢复省略覆盖](W02-07-codex-directory-context/native/a-cold-without-override-context.json)、[冷恢复显式禁用](W02-07-codex-directory-context/native/a-cold-explicit-disable-context.json)、[项目禁用](W02-07-codex-directory-context/native/d-project-disabled-context.json)、[差异](W02-07-codex-directory-context/changes.diff)、[审计](W02-07-codex-directory-context/audit.json)与[清单](W02-07-codex-directory-context/manifest.json)。各阶段完整 rollout 同在 native 目录，installationId/宿主标识脱敏，未复制认证文件。

1953 个旧证据与两个二进制均保持不变。开发构建仍为 `14898fa4f4154da888cf3452dd3042871a36666bda275a90d9fe0d112b1d19c1`；旧 bin 为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。未提交或发布。下一片继续普通来源的生产绑定、恢复核对和共享 Skill 交付；完整方法/Gate、hooks、受管/安装来源、Windows/proxy 与输入独占等硬要求不变。
