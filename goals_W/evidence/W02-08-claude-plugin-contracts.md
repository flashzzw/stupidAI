# W02.08 · Claude CLI 插件来源、排除与重载契约

2026-10-02。W02.08 从 pending 推进为 in_progress；整体仍为 9/40 done。本片是生产接入前的原生协议验证，没有增加工作台插件选配入口。

## 结果及复现

`python3 tests/claude_plugin_contract_probe.py`：21 个场景、339 项检查通过，退出 0。固定 Claude Code 2.1.280；其中 5 个场景额外通过 SDK 0.3.280 的公开 `Query` API 验证命令、Agent、插件来源、加载回执和错误数一致。完整结果目录为 `/tmp/netcap-w0208-plugin-contract-vor7lls4`。

本地临时用户配置、市场、插件、项目和状态目录彼此隔离。真实 CLI 完成本地市场登记与安装；没有访问用户市场配置、共享 daemon、真实模型供应商或 Windows 宿主。直接 stdio 仅发送控制请求；`get_context_usage` 明确采用本地 `summary`，没有用户输入或工具执行。所有自有 CLI 均已停止。Python AST、Node syntax 检查通过。

| 实测条件 | 原生结果及生产影响 |
| --- | --- |
| 正常加载、省略独立 inline 包、完整来源键停用 | Skill、命令、Agent、hook 的清单能一起出现或消失；另一插件及项目 Skill 保持。`enabledPlugins` 需要 `name@inline`；裸名称停用没有移除目标。 |
| 初始化回执 | 被停用的包，以及 manifest 无效的包，均可能返回 `plugins_applied: true`。该字段不能独自证明加载成功或组件启用；无效 manifest 在重载结果中有 `error_count: 1`。公开 SDK 也观察到同样结果。 |
| Skill 排除与全局 hook 停用 | 所测试的两个插件 Skill override 键没有隐藏其 Skill。`disableAllHooks` 仍列出 hooks，但逐项 `disabled: true`，同时影响另一插件；不能作为独立组件排除方案。 |
| `skipMcpDiscovery: true` | `.mcp.json` 与 manifest 内联 MCP 的哨兵均未启动，原生 MCP 清单为空；Skill、命令、Agent、hook 仍存在。SDK 同样通过。本片只有抑制分支，未运行 MCP 连接正例，不能验收 MCP 执行或完整隔离。 |
| 再次 initialize 追加包 | `plugins_applied: false`，新增包没有出现。不能用重发初始化请求给存活查询追加插件。 |
| 存活查询写入停用 flag | 写 flag 后立即读取仍看到旧 Skill/hook；显式 `reload_plugins` 后才消失。保存设置不等于实际生效。 |
| 修改本地插件后读取与重载 | 只读采样仍报告旧组件；重载后版本变为 2.0.0、Skill 名称带 `-v2`、hook 从 `exit 7` 变成 `exit 8`。重载是修改执行状态，不能挂在只读来源按钮后面。 |
| 两个不同 inline 目录声明同名插件 | 原生插件表列出两个路径/版本，但命令、Agent 和 hook 被合并成单组；停用同一个来源键会一起消失。仅比名称或组件个数不能唯一绑定资产。 |
| 已安装的本地市场插件 | 省略 inline 参数不会移除已安装来源；必须核对 `name@marketplace`。关闭不存在的 inline 来源键仍保留已安装包。 |
| 已安装和 inline 同名 | 同名 inline 会覆盖已安装来源，即使路径和版本不同；停用 inline 后已安装来源重新出现。停用已安装来源也不阻止 inline 加载。必须检查排除后是否有其他来源补位。 |
| 本地市场源目录更新 | 本次真实安装仍指向源目录；显式重载会从 1.0.0 变为 2.0.0，并改变 Skill/hook。不能把原生安装等同于内容版本冻结。工作台仍须沿用资产来源戳与显式执行版本事务。 |

hook 清单的 `pluginName` 是完整来源键（如 `w0208-selected@inline`）；Skill 描述的 `pluginName` 是插件名称。不能把两者直接当作同一身份字段比较。`commands/*.md` 在本次上下文清单中也作为插件描述出现；显示层不能仅根据描述存在就认定它来自 `skills/`。

## 失败记录与证据范围

首次带断言的运行 `/tmp/netcap-w0208-plugin-contract-gjsz2yhv` 退出 1：原先假设“installed 与 inline 同时列出”和“本地安装缓存冻结旧版本”，均被原生结果推翻。随后 `/tmp/netcap-w0208-plugin-contract-zkbh_25c` 退出 1，又证实不同路径的同名 inline 也会遮蔽 installed。修正的是实验预期与生产接入判断，没有修改 CLI 或把失败要求标为完成。

- [结果及逐项检查](W02-08-claude-plugin-contracts-result.json)、[执行日志](W02-08-claude-plugin-contracts-native.log)。
- [完整原始材料](W02-08-claude-plugin-contracts-raw.json.gz)：gzip JSON，包含逐场景请求、原始 stdio 收发、采样结果、SDK 请求/结果、安装回执与 stderr，附原文件 SHA256。
- [错误假设的原始材料](W02-08-claude-plugin-contracts-assumptions.json.gz)、[首次失败日志](W02-08-claude-plugin-contracts-assumptions.log)、[交叉来源失败日志](W02-08-claude-plugin-contracts-source-cases.log)。这些不是产品 bug 的红绿测试，不将预期修正称为产品修复。
- [独立复核](W02-08-claude-plugin-contracts-verification.json)、[固定版本来源](W02-08-claude-plugin-contracts-sources.json)、[差异](W02-08-claude-plugin-contracts-diff.patch)、[归档时点 manifest](W02-08-claude-plugin-contracts-manifest.json)。

`get_hooks_listing` 在固定 SDK 的 wire schema 中声明，但不在公开 `Query` 接口中，因此本片用自有 CLI 的直接 stdio 读取。它没有加入生产 bridge，也没有通过访问 SDK 私有对象伪装为受支持 API。公开 SDK 的 5 场景复核不声称覆盖 hook 清单接口。

本轮尝试 `find-docs` 的 Context7 CLI 查询，20 秒超时退出 124，没有取回在线文档。接口依据是已安装 SDK 的类型声明、固定 CLI 帮助和既有[官方资料归档/实验](../../docs/execution-capability-experiments.md)；不声称重新核对了最新在线资料。

## 剩余工作

生产插件资产引用、来源唯一绑定、加载回执与执行版本事务，以及对应界面尚未接入。不能仅复制 W02.07 的纯 Skill 包交付：CLI 插件还有命令、Agent、hook、MCP 等隐含组件。本次 hook 只是注册清单，`exit 7/8` 从未执行；实际失败传播、权限交互、重复 MCP、原 ID 带历史恢复、受管来源与 Codex 插件矩阵仍是 W02.08 的未完成条件。

下一片沿既有 CapabilityReference/Execution Snapshot/Runtime/Session 接入已验证的来源与版本绑定，保存不生效、应用需核对全部受影响组件；对同名来源和不可拆分组件明确拒绝不能保证的选配。`reload_plugins` 仅可作为显式应用动作，不能用于无副作用读取。生产不能依赖本地安装冻结资源，也不能让来源补位绕过排除。

W02.07 两项完整探针仍无新结果，原请求继续有效；不重复运行已知受限的监听/Windows 前置检查。既有 WSL 结果 `/tmp/netcap-wsl-host-ool4jx3i` 已在 W04.01 归档，本轮不重复计数。源码变化限于两个实验入口和证据/进度；没有产品构建变化，因此未重跑 Go、浏览器或构建。此前 968 个证据、`.dev/bin/netcap` 和旧 `bin/netcap` 保持不变，Goal 保持 active。
