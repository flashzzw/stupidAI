# W02.06 · Claude 资源替换与恢复继承

日期：2026-10-01（Asia/Shanghai）。在 [启停事务](W02-06-apply.md)上补齐新增资产、当前登记资源版本的显式替换、撤去覆盖与两资产混合协议。W02.06 仍为 **in_progress**，累计 done **7/40**，Goal active。

## 实现与契约

- 沿用预览、保存、准备、明确应用和持久 Operation。选择可带 `version`；已有资产省略版本时保持原固定版本，新选资产使用当前登记版本。显式版本必须匹配当前登记，候选引用、来源摘要、逐服务变化和资源版本在保存前确定，普通重连和目录更新不自动升级。
- 请求只调整列出的资产。新增和替换复用创建时的格式、兼容性、托管来源与同名检查；其余引用和服务保持。目标最多 16 项引用和 16 个服务，单次最多调整 16 项资产；重新验证只重建差异，16 项满额时部分替换仍可准备。旧候选的排列顺序在核对时兼容，不重写持久内容。
- `inherit` 撤去该资产的显式服务和来源依赖，旧快照保留完整引用。原 ID 重启后，撤去的旧服务必须不存在或属于有名称的原生继承来源，不能残留为 `dynamic`；当前目标为空时仍核对、展示继承清单。继承可能继续暴露同名服务，不将其解释为禁用。
- SDK 继续使用原 cwd/settings/认证、`settingSources: ['user','project','local']` 和非严格 MCP 配置；只关闭已确认空闲的自有 query。没有通过清空设置、删除继承来源或动态重加绕过排除要求。
- 原生成功的新绑定与脱敏清单仍原子持久。执行前检查当前能力库准入，成功后只核对持久意图与原生结果；后续停止新引用不否认已经完成的操作。迟到确认不恢复人工撤销的输入权；提交前 crash 仍未知且阻断重放，失败候选确认退出后才保留原绑定。
- UI 在无显式 MCP 的会话中也提供添加入口；每个资产一个启停控件，登记版本变化时另选资源版本。预览同时显示版本变化、消失和新增的服务；历史快照展示资源版本。恢复全部继承后仍能查看加载/使用证据和再次添加。

公共请求增加可选版本字段，已有请求继续有效；State schema 与 bridge metadata 格式不变。Codex 原生选择范围保持已有契约。使用说明见 [CONSOLE](../../CONSOLE.md#claude-原会话-mcp-启停改配)。

## 实测依据

固定 Claude Code **2.1.280**、Agent SDK **0.3.280**、Node **24.13.0**、Go **1.26.2**。通过 `find-docs`/Context7 核对 [SDK MCP](https://code.claude.com/docs/en/agent-sdk/mcp)和 [TypeScript options](https://code.claude.com/docs/en/agent-sdk/typescript)：project 设置来源允许加载 `.mcp.json`；`strictMcpConfig` 会排除其他来源，故本片保留原非严格设置。具体原 ID 行为以以下真实 CLI 实测为准。

全部验证使用 `.dev/bin/netcap`、自有 `/tmp/netcap-*` 状态、设置和项目、真实 CLI/MCP，以及 loopback 模拟供应商与合成凭据。未访问真实供应商、停止共享后台、迁移用户数据、提交或发布。各原始目录与源码/构建/附件 SHA256 见 [manifest](W02-06-resources-manifest.json)。

| 层次 | 实际结果 | 证据 |
|---|---|---|
| 原生 + 生产 HTTP | **97/97**：A/B 各自从无显式选择开始，加入两资产四服务；stdio/HTTP/SSE 都实际调用并返回正确账号/cwd 或 workspace 结果。库更新后仍用旧资源；明确升级保留其他资产、旧历史，退休服务消失。恢复同名 project 来源、调用和重启仍原 ID | [原生结果](W02-06-resources-native.json) |
| 升级丢回执 | **7/7**：真实成功后隐藏持久证据，转人工并重启，再停用库的新引用；恢复只读证据后晋升原候选，未再次应用 | [drop](W02-06-resources-upgrade-drop.json) |
| 升级提交前 crash | **8/8**：真实 applying metadata 后结束自有进程组，重启保留原/候选版本、unknown 和输入阻断，不重放 | [crash](W02-06-resources-upgrade-crash.json) |
| 升级连接失败 | **6/6**：远程服务拒绝认证，原绑定和暂停保留；恢复认证后明确重试同一候选，仍需单次审批 | [rollback](W02-06-resources-upgrade-rollback.json) |
| 撤去全部覆盖后丢回执 | **7/7**：空目标的持久结果可只读确认，保留原 ID、旧快照与人工控制，不重放 | [inherit-drop](W02-06-resources-inherit-drop.json) |
| 浏览器 | **23/23**：新增入口、双服务资产单个模式控件、保持旧版、明确升级/逐服务预览、恢复继承、历史、单次审批、1440/768 与无 pageerror | [结果](W02-06-resources-browser.json)、[升级预览](W02-06-resources-browser-upgrade-preview-1440.png)、[升级后](W02-06-resources-browser-upgrade-1440.png)、[继承 1440](W02-06-resources-browser-inherit-1440.png)、[继承 768](W02-06-resources-browser-inherit-768.png) |
| 状态/源核对 | 新增/升级/释放、同名拒绝、陈旧版本、候选来源漂移、16 项下部分替换通过；动态残留/未知来源/不可读清单等 **7 项**独立源核对通过 | [Go 定向](W02-06-resources-targeted-tests.txt)、[源核对](W02-06-resources-source-proof.txt) |
| 工程 | 全量 Go race、vet、bridge TS、web typecheck/Vite、Go 开发构建通过 | [race](W02-06-resources-go-race.txt)、[vet](W02-06-resources-go-vet.txt)、[构建](W02-06-resources-build.txt) |
| 受影响回归 | Codex 原 ID 改配 **18/18**、Claude 双服务启停 **23/23**、能力库生产 **11 项**通过 | [Codex](W02-06-resources-codex-regression.json)、[Claude](W02-06-resources-toggle-regression.json)、[能力库](W02-06-resources-library-regression.txt) |

浏览器分别等待持久操作/版本和页面事件刷新，再截图；没有使用会提前接受异步谓词的 `waitForFunction`。最终 fixture 和命名浏览器已关闭，归属临时目录的残留检查见 [收尾](W02-06-resources-process-cleanup.json)。

## 实际修复

补充“原生成功后、回执核对前停用能力库”场景，旧实现等待 60 秒仍无法晋升。[原始失败](W02-06-resources-catalog-after-commit-red.txt)及 [持久状态摘录](W02-06-resources-catalog-after-commit-red.json)显示：原生 verified，工作台 uncertain，候选未晋升，错误为能力未登记/停用/不适用。原因是晋升重新执行了当前目录准入检查。现将发送前准入与完成事实核对分开，同一真实测试通过；仍不自动更改输入权或忽略后续来源漂移。

## 重跑与剩余

```bash
make claude-build dev-build
go test ./internal/app ./internal/runtime -run 'TestClaude.*(Resource|ExecutionChange|Configuration)|TestExecution(Change|Apply)' -count=1
node tests/claude_mcp_configuration_test.mjs
python3 tests/claude_execution_resources_smoke.py
python3 tests/claude_execution_apply_fault_smoke.py --case drop --resources
python3 tests/claude_execution_apply_fault_smoke.py --case crash --resources
python3 tests/claude_execution_apply_fault_smoke.py --case rollback --resources
python3 tests/claude_execution_apply_fault_smoke.py --case drop --release
go test -race ./...
go vet ./...
```

浏览器以 `tests/claude_execution_resources_smoke.py --hold --info-file <自有文件>` 启动；用状态目录令牌登录固定 Playwright CLI 的自有命名浏览器，运行 `tests/claude_execution_resources_browser.js`。

- 目前可保持原固定资源或明确选择当前登记版本；任意历史资源版本回退尚未提供。外部原来源已经漂移时仍需独立核对，不把原地修改包装成安全升级。
- OAuth、动态 header helper、受管/插件/账号来源及完整权限冲突仍未完成。两资产混合三协议和 project 同名继承通过，不代替完整来源矩阵。
- 执行快照仍为 `recorded_sources_only`；提交前无成功证据的未知状态继续保留。Codex 新增/资源替换/继承、Skill、CLI 插件、独立前台、Claude worker 输出、Windows 与真实协作等既有硬条件不因本片通过而缩减。
