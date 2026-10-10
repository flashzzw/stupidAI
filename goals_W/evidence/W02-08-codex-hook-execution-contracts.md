# W02.08 · Codex 插件 hook 触发、逐线程覆盖与超时

2026-10-03。固定 Codex app-server 0.158.0，独立 profile/本地测试插件。新增可重跑 [探针](../../tests/codex_hook_execution_probe.py)。**完整预期失败，退出 1**：15 项中 14 项通过，可信插件的 `true` 命令在原生 hook 路径超时，不能认定插件成功执行已验收。产品代码和构建未改，W02.08 in_progress，9/40 done，Goal active。

## 已确认事实

`thread/start` 加载参数后没有触发本次 SessionStart；首个 `turn/start` 才观测到 hook/started。测试明确开启 `features.codex_hooks`，对所发现 hook 的完整 key/currentHash 通过 thread config 的 `hooks.state` 提供一次线程配置。hooks/list 仍报告 profile 级 untrusted，文件未被逐线程覆盖改写；此实验不提供用户配置的自动授信入口。

| 样本 | 原生观测 |
|---|---|
| 未提供信任 | 首轮到达本地供应商连接错误前没有 hook 事件 |
| 提供精确 key/hash 信任 | 出现 hook/started 和 hook/completed；后者 status=failed，原始错误为 `hook timed out after 3s` |
| 信任相同但 `plugins.fixture@owned.enabled=false` | 到达本地供应商连接错误前没有该 hook 事件；没有写 profile 配置 |

以上证明了此固定样本的触发/抑制与失败可观测性，不能证明整个插件的 Skill/MCP 排除、成功调用或方法 Gate。hook 事件报告 threadId、turnId、run.id、source=plugin、安装缓存 sourcePath、事件/状态/时间/耗时；没有 pluginId 或通用 exitCode 字段。run.id 中的序号和路径不是跨会话唯一身份，后续记录必须绑定原生会话和轮次；不能仅按事件名关联插件或操作。

模型路由固定为不可用的 `http://127.0.0.1:9/v1`。这些实验**发送了首轮输入并尝试本地模型连接**，不是零模型请求实验，没有接收到成功模型结果。网络重试即便配置为 0 仍出现 `willRetry:true`，因此探针显式 interrupt，并核对三个轮次均为 interrupted。没有启动 HTTP listener、MCP、真实供应商或共享后台。

## 失败缩减与对照

最初 Python hook 等待 EOF；在原生退出或超时附近取得 stdin JSON，但没有成功终态。改为增量解析一个 JSON 对象仍超时，进一步将整个命令缩减为 `true` 仍失败，故不能把原始 Python stdin 读取作为已确认根因。

随后建立返回非零的独立探针：相同临时环境直接执行 `bash -lc <command>` 与插件 hook 做对照。按 shell 包装、I/O 关闭、调度期限三个假设逐项改变：

| 命令/期限 | 直接执行 | 原生 hook |
|---|---|---|
| `true` / 3 秒 | 退出 0，11 ms（首份） | failed，3001 ms，超时 |
| `exec /usr/bin/true` / 3 秒 | 退出 0 | failed，超时 |
| `exec /usr/bin/true </dev/null >/dev/null 2>&1` / 3 秒 | 退出 0，17 ms | failed，超时 |
| `exec /usr/bin/true` / 10 秒 | 退出 0，17 ms | failed，超时 |

这些结果排除了上述简单改动足以解决故障，但尚不能区分 CLI 实现、进程生命周期与当前代理运行限制的具体作用。没有改写第三方 CLI 或绕过权限，也未把 timeout 当作预期成功。原生 hook 的真实成功/退出码失败、完整载荷和恢复仍需补验。

## 重跑和附件

```bash
python3 tests/codex_hook_execution_probe.py
python3 tests/codex_hook_execution_probe.py --command exec-true
python3 tests/codex_hook_execution_probe.py --command closed-stdio
python3 tests/codex_hook_execution_probe.py --command exec-true --timeout 10
```

最终默认命令结果与原始目录见 [result.json](W02-08-codex-hook-execution-contracts/result.json)；[协议](W02-08-codex-hook-execution-contracts/native-protocol.json)包含实际参数、错误、终态和退出码。[audit.json](W02-08-codex-hook-execution-contracts/audit.json)独立核对默认/三个对照的检查计数、三个 interrupted 轮次、配置及进程退出。[manifest](W02-08-codex-hook-execution-contracts/manifest.json)记录源码、CLI、构建和附件 hash；[差异](W02-08-codex-hook-execution-contracts/changes.diff)来自本片前置备份。

早期参数/脚本实验保留在附件 `exploration/`，不与最终检查累计。它们包括未开启/开启 codex_hooks 时仅创建线程的样本、等待 EOF、增量 JSON、短期限以及最小 true 样本。最初部分轮次在进程关闭前仍为 inProgress；不能将这些旧记录写成显式取消验收，最终探针已补齐逐轮 interrupt 和终态断言。

Python AST 检查通过。未修改生产 Go/Web，因此未重复构建/race/浏览器。1215 个既有证据文件、旧 bin 和上一片开发构建保持不变；本片所有自有协议进程均退出，未提交或对外发布。

## 后续

用真实 hook 开始/失败终态接通 Runtime→Application→界面可追溯记录，保持未结束/缺终态为未知，不将原生 sourcePath 自动提升为资产或完整插件身份。成功执行及原生超时原因继续保留缺口；随后推进完整资产/缓存绑定、逐线程 Skill/MCP 和权限。Claude 完整改配及先前 Skill 请求仍待已有终端结果，不重复请求或重跑无新条件的环境探测。
