# W02.08 · Codex 混合插件的 Skill 准备上下文

2026-10-04。固定 Codex app-server 0.158.0，隔离 profile、项目和两个私有缓存。新增 [原生探针](../../tests/codex_plugin_context_probe.py)，对同时包含 Skill 与 MCP 的本地插件完成 **11 个阶段、149 项检查**。本片补充线程实际准备上下文、正文按需载入和排除边界；没有修改生产代码或构建，不将它提升为成功模型投递、方法使用或 Gate 验收。W02.08 in_progress，整体 9/40 done，Goal active。

## 取得的事实

此前 `skills/list` 和 `hooks/list` 只提供目录/profile 清单，没有线程参数。固定 schema 的 [SkillsListParams](W02-08-codex-plugin-context/schema/SkillsListParams.ts) 与 [HooksListParams](W02-08-codex-plugin-context/schema/HooksListParams.ts) 保留在附件。本轮沿用已有官方资料与固定版本生成类型，未重复此前无法访问的网络查询；以下结论来自本轮真实 CLI 原始回执和文件。

首个 `turn/start` 在本地供应商连接失败前，已将实际准备的指令写入自有 rollout：`world_state.state.host_skills.body` 保存 Skill 根路径和描述清单，后续轮次通过 `full:false` 的补丁更新；`response_item` 中的原生 `content_item_kinds` 标明 `host_skills.instructions` 或 `skills.selected_skill_instructions`。因此可以观察某轮准备了哪些描述、是否注入 Skill 正文，仍不能证明模型收到或使用了它们。

| 阶段 | 实际观察 |
|---|---|
| A/B 选择版本 one | 当前准备上下文含 one 描述和对应私有缓存根；MCP 来自 one 的完整 pluginId/serverInfo；项目 Skill 和无关 MCP 保留。正文未提前注入 |
| 禁用及未选择 | 当前上下文不含插件描述，MCP 清单无该插件。禁用时提供显式 Skill 输入也未生成新的正文载入记录 |
| 已订阅线程直接 resume 到 two | 保持 one 描述和 one MCP；仅回执接受新参数不能证明新版本生效 |
| A 退订后按原 ID resume 到 two | 当前上下文改为 two，MCP 同步改为 two；B 下一轮仍为 one。原 ID 与先前历史保持 |
| A 恢复继承 | 当前上下文移除插件描述，MCP 排除；历史仍含以前的 one/two 描述 |
| A 重新添加 two 并显式选 Skill | 原生生成一条 `skills.selected_skill_instructions` 正文记录，含 two 的正文与完整缓存 Skill 路径；仅发生正文载入，没有方法执行 |
| A 已载入正文后再禁用 | 新一轮无插件描述、无插件 MCP、无新的 Skill 正文注入；之前已载入的 two 正文仍在历史里 |
| 自有后台重启，B 原 ID 恢复 one | one 的描述和 MCP 保持，显式正文为 one，前三轮历史均在；没有更换原生 ID |

目录 `skills/list` 在同时存在已启用线程 B 时仍没有这些仅由线程 config 启用的插件条目，进一步确认它不能作为逐线程清单。使用的是 `plugins.<完整来源 ID>.enabled` 配置覆盖；不使用 `disabledPluginIds` 来声称能力排除。

## 验证方法与证据强度

两个缓存的 manifest 均声明 1.0.0，Skill/MCP 内容和私有来源键不同。没有调用 plugin/install/uninstall、全局 reload 或配置写入。profile config 与所有预置缓存文件的字节保持不变；插件选择通过明确线程覆盖进行。原生 MCP 仅 initialize/list，没有 tools/call。

所有 **11 个轮次**均观察到指定线程/轮次的本地连接错误，然后显式 interrupt，并核对最终为 interrupted。路由固定为不可用的 `http://127.0.0.1:9/v1`，不启动 listener、不连接真实供应商；这里有模型连接尝试，不能称为零模型请求。两个自有 app-server 均退出 0，无 hook 或工具执行事件。

探针从已核对原生 ID/cwd 的自有 rollout 读取原始记录，合并 `world_state` 更新来判断**当前**描述；正文载入仅计算当前 `turn_id` 且标注 `skills.selected_skill_instructions` 的记录。不会搜索整个历史后误判旧 Skill 仍启用，也不会把旧正文消失作为禁用的预期。每个阶段同时核对线程 MCP 清单的完整性、来源 ID、服务版本与无关继承。

最初探索仅搜索全部 response_item，所以升级和恢复继承后仍同时找到旧描述；这不是原生排除失败，而是历史与当前上下文的测量范围不同。相关原始观测及脚本保留在 [exploration](W02-08-codex-plugin-context/exploration)。正式探针使用原生补丁语义，独立检查旧历史保持与当前描述排除。探索中的 `result.json` 不含完整验收断言，不能与正式 149 项累计。

`thread/read` 的可见 userMessage 投影没有显示显式 Skill 正文记录，完整依据在 rollout。因此后续产品接入需要限定文件读取范围、核对身份/轮次和完整性，并区别历史证据与当前加载；不能仅凭聊天文本或名称猜测插件资产归属。这里记录的是固定版本观察到的内部格式，不声明它是跨版本稳定 API。

## 重跑与归档

```bash
python3 tests/codex_plugin_context_probe.py
```

最终原始目录：`/tmp/netcap-w0208-plugin-context-ezmyzswo`。Python AST 检查通过；本片未改 Go、界面或构建，因此没有重复其测试矩阵。源码仅增加上述可重跑探针及说明。

附件：[正式结果](W02-08-codex-plugin-context/final/result.json)、[初始后台协议](W02-08-codex-plugin-context/final/initial-protocol.json)、[恢复后台协议](W02-08-codex-plugin-context/final/restored-protocol.json)、[升级后的当前上下文](W02-08-codex-plugin-context/final/upgrade-context.json)、[正文载入后禁用](W02-08-codex-plugin-context/final/disable-after-body-context.json)、[MCP 调用记录](W02-08-codex-plugin-context/final/mcp.jsonl)、[差异](W02-08-codex-plugin-context/changes.diff)、[审计](W02-08-codex-plugin-context/audit.json)与[清单](W02-08-codex-plugin-context/manifest.json)。原始 rollout 保留在 final 下；归档宿主名称和 installationId 已脱敏。

1617 个既有证据及两个二进制保持不变。开发构建仍为 `12a9f81616b22459658ac5399397a3856184dec0535eea3a95d16d3f57e3a9fe`；旧 bin 为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。未提交、推送、发布或切换共享后台。

下一片将按轮次的描述/正文载入证据接入执行详情，再继续混合组件选择和完整方法/权限验收。生产 Codex 选配当前仍仅支持 MCP 插件；本片不自动放开 Skill/hooks 包。方法使用、实际模型投递、完整排除与文件访问权限、hook 成功/异步、受管/安装来源、生产 proxy/peer、Windows 与跨客户端输入独占仍待完成。此前已请求的 WSL 结果未新增，不重复请求。
