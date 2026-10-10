# W02.08 · Codex 本地 MCP 插件原 ID 改配

2026-10-04。仅含 MCP 的本地 Codex CLI 插件现可在原会话新增、启停、明确替换资源版本或恢复继承。沿用已有候选事务、独立缓存副本和来源核对，保存候选不改变当前执行；明确应用后仍保留原 ID 和执行快照历史。W02.08 in_progress，整体 9/40 done，Goal active；本片不是完整插件或 W 系列验收。

## 原生依据与实现

固定后台 0.158.0 的隔离对照确认：已订阅线程直接 `thread/resume` 带新 config 会保留旧插件；取消当前连接订阅后，显式新 config 才换到新定义。`thread/read` 仍可能报告 idle，`thread/loaded/list` 仍含原 ID，因此退订不能称为全局卸载。退订后省略 config 保留原覆盖，显式空 config 恢复继承。另一线程和 profile config 保持不变。

探索原始记录见 [change](W02-08-codex-plugin-change/exploration/change/result.json)、[loaded](W02-08-codex-plugin-change/exploration/loaded/result.json)、[omitted](W02-08-codex-plugin-change/exploration/omitted/result.json) 及各目录协议记录。复用探索框架的 `expectations_met` 主要覆盖其原有检查和进程退出，不作为这些新增事实的逐项断言；下列生产原生测试另行断言。

- 预览/保存沿用共享资源候选逻辑：未指定资产保持固定版本，显式升级才读取新登记来源，恢复继承释放引用。Codex 普通 MCP 仍只支持原有选择启停，不能借插件请求新增普通 MCP 或指定其资源版本。候选阶段拒绝包间及包与普通 MCP 的同名组件。
- 准备操作先等待原观察结束，再只读核对原线程、完整空闲状态和完整历史摘要，并保持暂停。之前已在途的观察可能完成其权限采样；准备操作自身不退订、不恢复、不准备插件缓存。
- 明确应用持久记录原 Scope 与目标候选，再准备目标的独立缓存。多次核对原空闲和历史，退订一次，再核对并按原 ID 恢复完整目标 config。路由、状态归属和权限继续沿原执行绑定。
- 核对目标组件之外，还必须从完整线程清单确认已撤下的私有插件 ID 消失。即使目标已无任何显式能力，也不能以空选择自动判为成功。回执丢失后只读核对，不重发退订/恢复、不修复缓存；未知期间保留原版本及暂停。
- 原 ID、完整历史、路由、固定缓存、当前组件与撤下来源均一致才提升候选。旧版本进入历史；人工接管和暂停不会被迟到结果覆盖。最后一个选择撤去后，历史 MCP 成功来源仍可展示；当前清单不可用不会清掉历史或新增无显式选择的输入门槛。
- 界面提供整包选择、版本差异和明确应用入口。已确认空闲的插件改配无需等待全局卸载；普通 Codex MCP 单独改配继续原限制。产品不会停止共享后台或调用全局 plugin/install、配置写入、MCP reload。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| Runtime | 11 个改配场景通过：upgrade、inherit、丢回执、旧来源仍在、busy、历史变化、退订错误/未知状态、退订后 busy、缓存漂移、回执身份不匹配。公共 Execute/Reconcile 验证单次写入与只读核对。 |
| Application/SQLite | 3 个新增测试通过：保存不改当前绑定/不建缓存；原绑定和目标先于发送持久化，丢回执/重开/人工接管后不重放；候选拒绝组件同名。原生测试另覆盖新增、升级、启停和继承。 |
| 历史来源 | 公开 Observe 回归由红转绿；最后一个选择撤去后保留原插件历史，当前清单成功与失败均验证，不猜当前资产。 |
| 真实 Codex | [最终改配结果](W02-08-codex-plugin-change/final/result.json) **63 项通过**；A 完成 disable → enable → upgrade → inherit → add，另一线程 B 保持 v1，第三线程恢复到无显式能力。原 ID、精确候选、准备只读、一次退订、版本历史、其他 MCP、实际 serverInfo 版本、SQLite 和 Runtime 冷恢复均核对。 |
| 原生回归 | [新建选择结果](W02-08-codex-plugin-change/selection-regression/result.json) **28 项通过**；两组原生测试均启用 race，六个自有原生后台全部退出 0。 |
| 浏览器 | 六个真实 Application 状态回放流程合计 **100 项通过**，Codex 新建回归 **18 项**，Claude 既有 WSL 工具/hook 记录回放 **15 项**。使用本轮构建、受控 HTTP/EventSource；浏览器不触发额外原生操作。桌面 1440 和移动 390 均检查，无横向溢出。 |
| 回归与构建 | app/console 全包 race、相关 Codex/Claude MCP/插件/改配 Runtime race、带 nativecodex 标签全包 vet、`make dev-build`（含 TypeScript 检查）通过。未重复已知受限的 OpenCode 监听测试。 |

原生使用测试 stdio transport；代理仍无法监听本地 TCP/Unix 服务。本轮只有 MCP initialize/list，**零模型、工具和 hook 执行**；注入的用户消息明确标记为合成历史，只用于物化线程及核对历史保持。历史 MCP 投影回归也是受控协议样本，不能提升为真实使用证据。浏览器使用 [状态导出](W02-08-codex-plugin-change/final/change-browser-responses.json)，仅交互层受控回放。

原始失败完整保留：首次 native 测试因多余 import 编译失败；两次准备只读断言将其他会话或先前在途的权限采样纳入计数，改为在公开 Runtime Execute 区间冻结协议计数后通过。Runtime 历史变化夹具最初仅改变分页结果、未改变完整历史端点，已修正；组件同名的第一对照被已有插件名称冲突检查提前拒绝，改为不同插件名后复现并修复。真正的产品红灯为 Application 尚不支持插件候选、Runtime 不支持新路径、旧 UI 无入口、历史来源丢失及不同名插件的组件冲突。各阶段见 [logs](W02-08-codex-plugin-change/logs)。

复跑原生入口：

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags nativecodex -race ./internal/runtime -run '^TestCodexNative(PluginChanges|ScopedPluginSelection)$' -count=1 -v
```

浏览器入口：[改配模板](../../tests/codex_plugin_change_browser.js)，注入状态导出、当前 web/dist 资源路径映射和六个 case 名。其他命令：`go test -race ./internal/app ./internal/console`、`go test -race ./internal/runtime -run 'Test(Codex|ClaudePlugin|ClaudeExecution|ClaudeMCP)' -count=1`、`go vet -tags nativecodex ./...`、`make dev-build`；均使用上列 GOCACHE。

最终原生目录 `/tmp/netcap-w0208-codex-plugin-change-2561923817`；新建回归 `/tmp/netcap-w0208-codex-scoped-plugin-1733830856`。开发二进制 SHA256：`12a9f81616b22459658ac5399397a3856184dec0535eea3a95d16d3f57e3a9fe`。旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，1535 个旧证据不变；未提交、推送或发布。归档中的宿主名称和 installationId 脱敏；[差异](W02-08-codex-plugin-change/changes.diff)、[审计](W02-08-codex-plugin-change/audit.json)、[清单](W02-08-codex-plugin-change/manifest.json)、[桌面](W02-08-codex-plugin-change/upgrade-1440.png)和[移动](W02-08-codex-plugin-change/upgrade-390.png)可复核。

下一片继续完整组件和实际使用/权限；Codex Skill/hooks、受管/安装来源、异步 hook、超时根因、生产 Unix proxy/peer、Windows 和跨客户端的原子输入独占仍未完成。既有 `claude_plugin_change_smoke.py`、`claude_skill_method_probe.py`、`codex_skill_selection_probe.py` 的终端结果仍待返回，不重复请求。每次空闲检查只能缩小并发变化窗口，不能证明另一客户端无法在检查后发送输入；本片没有降低这个硬要求。
