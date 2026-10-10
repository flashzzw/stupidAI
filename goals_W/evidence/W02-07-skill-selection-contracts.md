# W02.07 · Skill 原生选择键与生效证据核对

2026-10-02。本片推进生产逐会话选配所需接口核对，未修改产品实现或构建。不能用目录发现、控制参数接受或模型自述代替有效选择/方法 Gate。

## Codex：继承保留、恢复与完整请求探针

新增 `tests/codex_skill_selection_probe.py`，固定 server 0.158.0，独立 HOME/profile/项目，通过 stdio 控制。`--metadata-only` 不监听、不发送模型输入；完整模式使用已有本地文本模拟供应商，检查默认→禁用→默认的实际描述差异、原 ID 重启恢复、正文按需注入和禁用时显式 Skill 输入的实际边界。它不请求真实供应商，也不证明方法遵循。

[本次元数据结果](W02-07-skill-selection-contracts-codex.json)目录 `/tmp/netcap-w0207-selection-voq53kz5`，9 项通过：合并单项覆盖保留原本禁用的其他 Skill、三个线程的保存目录、目录清单仍沿用原配置、原 ID 恢复、合成历史条目保持、配置文件不变。`effective_selection_verified=false`；参数接受不能填补实际模型请求的证据。`skills.config` 是数组，不能直接用一个新数组丢弃其他原生选择。

首轮 `/tmp/netcap-w0207-selection-odxn8bz5` 的“resume 有 turns”断言失败。原文件保留合成注入项，却没有实际模型轮次；修正探针为读取原生回执指向的隔离 rollout 并核对合成条目，未改产品或把无轮次声明成真实历史完成。四个 Python 测试（含无效类型/重复路径子场景）验证覆盖合并与输入拒绝；语法检查通过。

完整模式本轮未执行：当前代理无法启动本地 HTTP listener。已通过异步问题请求用户在已有可用 WSL 终端运行：

```bash
cd /home/zzw/codex-netcap
python3 tests/codex_skill_selection_probe.py
```

收到 `Evidence directory` 后直接读取共享目录，不要求用户粘贴完整请求。完整模式仅按需注入 fixture Skill；模拟供应商只回复文本，不执行方法工具。因此即使完整模式通过，方法 Gate 仍需后续实际任务验证。

## Claude：目录键与声明名称不能混用

新增 `tests/claude_skill_sources_probe.py` / `.mjs`，固定 Claude 2.1.280、SDK 0.3.280。使用无输入 SDK query，读 `supportedCommands()`、`getContextUsage({detail:'summary'})` 与 `reloadSkills()`；summary 明确使用本地估计，避免默认 full 的 token-count API。没有模型结果，也没有使用 MCP 或真实供应商。

最终[11 场景结果](W02-07-skill-selection-contracts-claude.json)位于 `/tmp/netcap-w0207-claude-skills-qqbsnv4a`。61 项观测断言通过，含以下**不能等同于排除成功的边界**：

| 输入与来源 | 实际结果 |
|---|---|
| 目录 `shared`、frontmatter name `w0207-shared`，用 `w0207-shared: off`（flag/project/managed 三来源） | 目标仍在 commands 与 Skill 上下文条目中，刷新不改变；声明名称排除失败 |
| 同一资源使用 `shared: off` | commands 与上下文移除目标，项目 Skill 和 12 项内置 Skill 保留 |
| 目录与声明名称均为 `w0207-shared`，同名 override | 目标移除，其他项目资源保留 |
| 不同目录的用户/项目资源声明同一个名称 | 两个命令条目及来源同时出现，不能仅凭名称判断唯一资产 |
| 用户/项目资源目录均为 `shared`，声明名称不同，用 `shared: off` | 两项都被隐藏；该键不能精确排除单个来源 |
| SDK `skills:['w0207-project']`，真实目录为 `project` | commands 仍返回两项；Skill 上下文为空，不是正确选择 |
| SDK `skills:['project']` | commands 仍返回两项；Skill 上下文只含项目项，其他用户及内置方法不再包含 |
| 初始化后 `applyFlagSettings({skillOverrides:{shared:'off'}})` + reload | 命令与上下文移除 shared、保留 project；仅验证无在途任务、无模型输入的查询 |

`shared_command_hidden_at_start` 独立记录是否实际从命令清单消失。名称排除失败案例在最终报告里仍为 false；61 项通过表示已复现列明契约，不代表 11 种选择方式均可用于生产。SDK 上下文条目的 tokens 只用于来源观察，不能当作真实加载正文或使用的证明。

第一版探针把 SDK 的 `getContextUsage` 响应误当作另一种上下文类型：真实结构为 `skills.skillFrontmatter`、`memoryFiles`。读取固定 SDK 类型与实际回执后修正，原失败目录 `/tmp/netcap-w0207-claude-skills-dlsa5p7n` 保留。初始 5 场景和扩展 8 场景中的声明名称排除失败也保留在[原始失败矩阵](W02-07-skill-selection-contracts-claude-initial.log)及[扩展矩阵](W02-07-skill-selection-contracts-claude-expanded.log)。最终脚本以观测到的目录键契约命名断言，同时保留实际隐藏结果，没有删掉失败条件。

## 后续实施与状态

生产 Skill 绑定需要同时记录资源身份、声明名称与原生目录键；同键跨来源冲突须明确拒绝无法实现的单项排除。不能直接传 SDK 白名单来替代所选能力启停，也不能只用 commands 判断上下文暴露。下一步据这些事实实现绑定与实际清单核对，继续 Codex 完整请求、Claude 生产发现/选择、按需正文读取、同 ID 版本恢复和方法 Gate。

W02.07 in_progress、整体 9/40，Goal active。没有关闭剩余硬要求。四份新探针/测试和进度 diff、原生回执投影及校验见[manifest](W02-07-skill-selection-contracts-manifest.json)。旧证据、开发构建和旧 `bin/netcap` 均未改；没有提交、共享后台切换、安装依赖或外部消息。
