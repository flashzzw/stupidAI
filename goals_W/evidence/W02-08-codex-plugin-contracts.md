# W02.08 · Codex 插件来源与安装缓存契约

日期：2026-10-03。固定 Codex app-server **0.158.0**。W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 范围与结果

新增 `tests/codex_plugin_contract_probe.py`，使用已有隔离环境和 stdio harness，创建自有用户/项目本地市场与测试插件，核对插件发现、源预览、安装状态、Skill/hook 发现及缓存变化。**28 项原生检查通过**。没有创建线程、发送模型输入或启动 fixture MCP/hook，不代表逐线程选择、真实调用、方法使用或 hook 权限已经验收。未改产品、未重建开发二进制，不影响等待用户执行的 Claude 插件改配探针。

每个市场各有一个名为 `fixture` 的插件，随后令它们带入同名 Skill。源包包含 legacy `.codex-plugin/plugin.json`、Skill、stdio MCP 声明及 PreToolUse hook。安装、配置启停、卸载都只作用于自有临时 profile；没有访问远程市场、真实供应商或共享后台。

## 决定生产绑定方式的事实

| 观察 | 实测结果及含义 |
|---|---|
| 同名来源 | `fixture@owned-home` 与 `fixture@owned-project` 同时安装；两个 Skill 都叫 `fixture:fixture-project`，`skills/list` 保留各自 pluginId 和缓存路径。名称不能单独决定归属 |
| 源预览与安装状态 | `plugin/read` 在未安装和已禁用时仍列 Skill/hook/MCP；其中 Skill 的 enabled 仍为 true。应单独读取 summary 的 installed/enabled，不能把组件预览当作会话加载 |
| 实际缓存来源 | `plugin/read.skills[].path` 位于市场源目录，`skills/list` 与 `hooks/list` 则指向 `CODEX_HOME/plugins/cache/<market>/<plugin>/<version>/...`；二者不是同一对象 |
| 版本预览 | 源 manifest 从 1.0.0 改成 2.0.0 后，`plugin/read.summary.localVersion` 立即为 2.0.0；重新启用但未重装时，Skill 仍来自 1.0.0 缓存。localVersion 不足以证明实际安装/执行版本 |
| 显式更新 | `plugin/install` 重装后，发现路径转为 2.0.0，Skill 名称反映新内容。旧缓存的字节在此前源修改期间保持不变 |
| 同版本内容变化 | 只改 2.0.0 源文件时，缓存内容不变；随后显式重装 2.0.0，会改写**同一缓存路径**的字节。不能只用版本号或路径承诺执行内容被冻结 |
| 精确 profile 启停 | `config/value/write` 的 `plugins.fixture@owned-project.enabled=false` 在同一 profile 的 Skill/hook 发现中只排除项目插件，另一同名市场插件保留；进程重启保持禁用。此为 profile 范围，不是逐线程覆盖 |
| hook 状态 | `hooks/list` 返回缓存 sourcePath、完整 pluginId/key、source、enabled 与 trustStatus。此样本的 enabled 为 true，但 trustStatus 为 untrusted；没有实际运行它 |
| 安装策略与解析失败 | 本地市场 NOT_AVAILABLE 令 plugin/install 返回 -32600 明确拒绝；损坏项目市场产生带路径错误，用户市场仍可发现。没有把本地策略样本提升为受管/云策略矩阵 |
| 卸载与隔离 | 卸载项目插件后，其 Skill/hook 发现消失，市场源仍可预览；其他插件保留。另一个独立 profile 对同一项目源不继承第一个 profile 的 installed 状态 |

这要求后续绑定至少分开记录：完整插件 ID、市场/源包、实际安装缓存、资产版本、内容戳、当前发现与执行使用。安装和 profile 配置写入有共享状态影响，不能直接充当一次会话的选择。缓存路径应从原生发现核对，不能由源预览的 localVersion 推导。

## 来源与可重跑入口

本轮尝试读取官方 [Build plugins](https://developers.openai.com/plugins/build/plugins) 和 [Codex App Server](https://developers.openai.com/codex/app-server)，均返回 HTTP 403；官方搜索也为 403，`https://learn.chatgpt.com/docs/app-server` 导航超时。**未取得新的官方正文**，不把这些链接当作本轮接口稳定性的证据。

已有仓库 [Codex App Server.md](../../Codex%20App%20Server.md) 将 plugin/list、read、install、uninstall 标为 under development，并写明 “Don't call this method from production clients yet.”。本片遵守实验边界，没有为此新增生产调用。既有固定版本生成类型位于 `/tmp/netcap-w0207-schema/v2/`，本轮实际调用核对了使用的字段；相关类型摘录及仓库文档 hash 归档。Skill/hook 的 source/path/id/state 字段以原生回执为准，不能从 schema 是否存在推断已完成实际加载。

```bash
python3 tests/codex_plugin_contract_probe.py
```

最终原始目录：`/tmp/netcap-w0208-codex-plugins-4v5wm1ke`。主进程、重启进程、第二 profile 共三个自有 app-server 均退出 0。协议调用只包含 initialize、plugin/list/read/installed/install/uninstall、config/value/write、skills/list 和 hooks/list；三个协议文件均无 thread/turn 请求，MCP/hook 的自有启动标记不存在。首轮 5 项及随后扩展的 16/18/24/25 项是逐步核对记录，不与最终 28 项累计计数。

证据：[最终结果](W02-08-codex-plugin-contracts/result.json)、[独立协议/退出核对](W02-08-codex-plugin-contracts/audit.json)、[主进程协议](W02-08-codex-plugin-contracts/native-protocol.json)、[重启协议](W02-08-codex-plugin-contracts/restarted-protocol.json)、[第二 profile 协议](W02-08-codex-plugin-contracts/profile-b-protocol.json)、[manifest](W02-08-codex-plugin-contracts/manifest.json)、[本片差异](W02-08-codex-plugin-contracts/changes.diff)。Python AST 解析通过；没有产品修改，因此未重复 Go、前端或构建矩阵。原生版本查询的临时 PATH helper 警告不影响退出，原始日志保留。

## 仍需完成

Codex 生产资产绑定、逐线程选择/排除、真实 MCP 和 hook 调用/权限、同 ID 带模型历史恢复、完整用户/项目/受管来源仍未完成。hook 清单可作为下一片来源解释的依据，信任与执行结果须独立验证。公开插件接口的开发中限制保留，不用源预览或安装成功替代真实会话验收。

Claude 的 `claude_plugin_change_smoke.py` 与此前 Skill 完整探针仍待已请求的终端结果。本片没有重复要求授权、没有重跑受限监听/宿主检查；下一步继续收取这些结果并推进 Codex 实际缓存绑定。
