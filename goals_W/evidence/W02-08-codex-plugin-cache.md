# W02.08 · Codex 磁盘缓存与后台已加载定义

2026-10-04。固定后台 0.158.0 的隔离对照确认：插件磁盘缓存与同一后台后续线程采用的 MCP 定义可能不同。即使完整插件 ID、源版本和磁盘摘要都已取得，也不能据此证明当前后台使用了该版本。此片是原生契约调查，没有新增生产选择或修改构建；W02.08 in_progress，9/40 done，Goal active。

## 对照结果

插件只有一个 stdio MCP；服务器代码完整嵌入缓存 `.mcp.json` 的 `python -c` 参数，没有引用外部源脚本。初始化响应中的 original / cache-changed 标记因此直接区分所加载的运输定义。下表的“后台”均为自有测试进程，不涉及用户现有 daemon。

| 操作 | 实际观察 |
|---|---|
| 安装 original，启动线程 | MCP 初始化返回 original |
| 仅把缓存 `.mcp.json` 改为 cache-changed | 源目录与 profile 均不变；旧线程仍返回 original |
| 同一后台创建新线程 | 仍返回 original；出现新 MCP 子进程 PID，磁盘文件仍为 cache-changed |
| 关闭后台，以同一 profile 重启并恢复原 ID | 返回 cache-changed，保留原 ID 与显式注入的合成历史 |
| 把磁盘缓存精确恢复为 original | 已恢复线程仍返回 cache-changed，同一后台随后新建的线程也返回 cache-changed |
| 再重启，以 `plugins.fixture@owned.enabled=false` 恢复原 ID | 插件 MCP 从该线程清单中消失，无关配置服务保持连接；同一后台另一普通线程加载 original，禁用线程仍无插件 |

`plugin/installed` 在缓存改变后仍报告 localVersion=1.0.0、installed=true、enabled=true。这里验证了后台之间的行为差异，未直接检查其内部缓存实现；不把“后台保留旧定义”推广成所有组件和所有刷新操作的通用规则。

初始假设“改磁盘后新线程就加载新定义”失败，保留 [first/result.json](W02-08-codex-plugin-cache/first/result.json) 与当时脚本。针对该失败检查了三个解释：磁盘被原生恢复、新线程复用 MCP 进程、后台继续使用旧定义。磁盘摘要保持改后值且新 PID 收到旧定义，排除前两项；后台重启前后的反向对照进一步确认第三项。没有通过改写缓存检查或删除失败记录来掩盖此结果。

## 验证、范围与重跑

[最终 22 项](W02-08-codex-plugin-cache/final/result.json)通过；[完整协议](W02-08-codex-plugin-cache/final/initial-protocol.json)、[首次冷恢复](W02-08-codex-plugin-cache/final/restored-protocol.json)、[禁用冷恢复](W02-08-codex-plugin-cache/final/excluded-protocol.json)和 [MCP 初始化轨迹](W02-08-codex-plugin-cache/final/mcp.jsonl)可复核。最终目录 `/tmp/netcap-w0208-codex-cache-kh6iqlyu`；中间修正后的 21 项位于 `/tmp/netcap-w0208-codex-cache-1yy_z2y_`，计数不与最终累加。

```bash
python3 tests/codex_plugin_cache_probe.py
```

三个自有后台均退出 0，原生版本检查通过。零模型输入、零 tools/call、零 hook 执行；`thread/inject_items` 只写入明确标记的合成用户历史以物化线程，不能称为带成功模型/工具历史恢复。这里的 cold resume 是真实关闭/重启后台后的原 ID 初始化，但没有通过生产 Runtime/Application 的缓存选配路径。操作只在隔离 profile、项目及插件缓存下执行，最终源/profile/缓存与安装后的基线一致。JSON 中宿主名和安装标识脱敏。

本片仅增加原生探针和文档，没有产品改动，因此未重复 race、Web 类型检查或构建。上一片开发二进制保持 `69ad33ebc6ad431dabbd0a676091ce2ee1e38168f34a64b1f5701471af44358a`，旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。1411 个既有证据不变，差异和归档哈希见 [changes.diff](W02-08-codex-plugin-cache/changes.diff)、[audit.json](W02-08-codex-plugin-cache/audit.json)、[manifest.json](W02-08-codex-plugin-cache/manifest.json)。未提交或对外发布。

## 对后续实现的约束

原生 `McpServerStatus` 提供 pluginId、连接状态、服务自报元数据与清单，没有运输定义或缓存内容摘要；当前配置 schema 的 `PluginMcpServerConfig` 也明确排除 transport 覆盖。文档缓存路径约定不能替代后台实际加载内容证据。依据为此前已保存的固定版本 schema 和官方插件文档，本片不重复受限网络查询。

生产绑定需区分源文件、磁盘安装副本和后台已加载定义，并在新建/原 ID 恢复前核对。下一步调查可验证的加载依据，以及保持既有会话身份与其他配置的受控刷新或隔离契约；在该依据建立前不自动安装、重启共享后台或把磁盘快照标成运行版本。完整插件选择/排除、实际工具/方法/权限、Codex hook 成功/异步、Windows/proxy 等硬要求继续保留，先前已请求的 WSL 探针结果仍待收取。
