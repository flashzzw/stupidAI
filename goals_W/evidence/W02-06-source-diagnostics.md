# W02.06 · 来源枚举、策略边界与具体诊断

日期：2026-10-01。生产增加 MCP 实际来源展示和按服务给出的核对失败原因。**状态测试与构建通过；原生来源/策略枚举 44 项通过，但 13 项连接检查失败，整体原生探测未通过。** W02.06 in_progress，done 7/40，Goal active。

## 实现与回归

原来的 `verifyMCP` 对不同失败统一返回泛化英文错误，无法指出受影响的服务或区分缺席与继承冲突。[回归前失败](W02-06-source-diagnostics-red.txt)用调用方接口确认缺席的 identity 没有明确诊断。现在使用最后一次原生清单区分不可读取、服务缺席、同名重复、来源不一致、未排除的继承项和连接失败；OAuth 专用错误与已有原 ID 重试分类保持。

Go 能力证据增加可选 `source`，只保留已知来源名称；重复或未识别的值为 unknown。已有启用/禁用准入、工具历史、操作结果和配置事务规则不变，不因可解释原因而放行输入。当前清单没有服务时不会根据本地配置猜测“已被某策略拒绝”。

UI 在执行详情单列原生来源，并将“可用工具”改为“原生工具清单”，明确枚举不等于调用获准。新字段兼容旧记录，没有修改 State schema 或回填历史来源。界面本片只通过 typecheck/构建，未运行浏览器验收。

| 验证 | 实际结果 | 证据 |
|---|---|---|
| Bridge 诊断 | **7 项**：缺席、继承残留、错误来源、连接失败、重复、未知来源和不可读清单；不泄露原始 config/error | [测试](W02-06-source-diagnostics-bridge-tests.txt) |
| Go 投影 | **6 个场景**：来源和具体原因正确，所有不一致都继续阻断；未知来源不透传 | [定向 race](W02-06-source-diagnostics-go-race.txt) |
| 受影响回归 | 原 OAuth/动态 helper 检查、**10 项**认证恢复生命周期、**7 项**配置来源核对通过；Application/Runtime 的 Claude/MCPLogin/Execution 定向 race 通过 | [bridge](W02-06-source-diagnostics-bridge-tests.txt)、[来源](W02-06-source-diagnostics-configuration-tests.txt)、[Go](W02-06-source-diagnostics-go-race.txt) |
| 工程 | bridge TS、web typecheck/Vite、Go 开发构建、全包 vet、探测脚本语法检查通过 | [构建](W02-06-source-diagnostics-build.txt)、[vet](W02-06-source-diagnostics-go-vet.txt) |

## 原生探测与失败边界

固定 Claude **2.1.280**、SDK **0.3.280**、Node **24.13.0**、Go **1.26.2**。使用真实 SDK/CLI、生产 `mcpDefinitions/mcpInventory/verifyMCP`、自有 stdio 服务、Git 项目与独立配置目录，没有提交模型输入或启动监听端点。原生清单来源可读；服务进程启动后却在收到 initialize 前遇到 EOF，最终连接状态 failed。

下面是**枚举层**的事实，不是完整加载/调用验收：

| 样本 | 原生枚举结果 |
|---|---|
| 同名 user / project / local / dynamic | 依次由 project、local、dynamic 覆盖；其他不同名服务保持 |
| 显式 disabled，存在同名继承 | 同名 local 项仍存在，生产核对明确拒绝把它视为禁用 |
| strict + dynamic / strict + 空目标 | 只枚举 dynamic / 不枚举配置服务；因此严格模式不能直接用来替代本次单项选择 |
| user deny / project deny 加 flag allow | 目标从枚举中消失；flag allow 没有恢复被拒项 |
| user allowlist / user + project allowlists | 只枚举允许项；两层允许列表合并 |
| SDK host `managedSettings` 的 MCP deny | 目标从枚举中消失；只证明当前宿主提供的限制层，不替代企业文件、注册表或远程策略验收 |
| user/host 的工具 deny 与 flag ask | 配置样本仍枚举服务器，但连接失败，没有实际工具权限决策证据 |
| 本地 CLI 插件与动态同 basename | 插件项以 `plugin:source-fixture:identity` 和 plugin 来源单独枚举；动态 identity 仍为 dynamic |

官方 [MCP 管理](https://code.claude.com/docs/en/managed-mcp)与[权限文档](https://code.claude.com/docs/en/permissions)经 Context7 核对，说明允许/拒绝列表作用于动态配置，严格模式不绕过这些限制；[摘录](W02-06-source-diagnostics-docs.txt)保留查询内容。实验没有把 host policy 伪称为所有企业来源，也没有依据仅有的 permission 配置样本宣称 deny/ask 实际执行通过。

[最终完整结果](W02-06-source-diagnostics-native.json)保留：`source_expectations_met=true`、`connections_verified=false`、`expectations_met=false`，退出码为 1；**44/44** 枚举/诊断检查通过，**0/13** 连接检查通过。连接断言没有被删除或改成接受 failed。没有模型输入和工具调用，无法证明工具结果、审批优先级或实际排除后的调用行为。

直接向相同 Python 服务输入 initialize 和 tools/list 时，两种响应均正常，**4 项对照检查通过**，见[直接服务对照](W02-06-source-diagnostics-direct-server.json)。生产同款 systemPrompt 的单变量检查也仍失败。当前没有定位到原生管道提前关闭的原因；本机没有可用 strace，未修改固定 CLI、改用代理管道或绕过工具权限。此前 stdio 的历史通过证据保持原样，不能用来宣称本次连接通过。

原始日志、源码/构建/附件 SHA256 与进程检查见 [manifest](W02-06-source-diagnostics-manifest.json)。只使用 `.dev/bin/netcap`、自有 `/tmp/netcap-claude-sources-*` 与合成配置；未访问真实模型供应商、修改用户配置、停止共享后台、提交或发布。

## 重跑与下一步

```bash
make claude-auth-contracts
node tests/claude_mcp_configuration_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/runtime -run 'TestClaude|TestMCPLogin|TestExecution' -count=1 -timeout=120s
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 tests/claude_mcp_sources_probe.py
```

继续定位原生 stdio 提前 EOF，并补齐实际工具清单、权限冲突执行和浏览器；不能把来源枚举当作 W02.06 完成。远程动态认证、OAuth 不重启恢复、SSE 完整准备、helper/OAuth cwd 组合及其余既有硬要求继续保留。若原生连接条件仍不能推进，可交错处理前置独立的 W03.03 实现对照。
