# W02.09 · 共享 stdio MCP 与四配置联合验证

日期：2026-10-10。同一条共享 MCP 资源资产现在可以从生产入口选给 Codex A/B、Claude A/B；每份配置分别绑定自己的工作目录、凭据引用和原生状态。Codex 新增资源定义与后台环境引用版本记录，创建表单和执行详情已接通。共享与各自专属工具调用、原 ID 启停改配、重启和依赖变化已联合验证。

本片为 **9/40 done，W02.07 / W02.08 / W02.09 in_progress，Goal active**。完整来源、动态权限、Skill/插件混合矩阵及其他首版硬要求保留；本地脚本化供应商只验证协议与实际工具执行，不替代真实模型任务或 Gate。

## 生产行为

- Codex 继续支持已有 `codex_mcp` profile 映射；未填写该字段的 MCP 资产可使用与 Claude 共用的资源目录/JSON。当前 Codex 资源路径只接受 stdio `type/command/args/env`，同名原生配置层、未知字段、无效环境引用和不支持的远程定义明确拒绝。
- 创建前只读固定定义文件 SHA-256，并从已核对 socket、进程身份和 profile 的 Codex 后台取得显式环境引用。名称必须在实例 `env_allow` 内；保存名称、存在状态和摘要，命令、展开参数及凭据值只用于运行时覆盖，不进入 Scope 或执行快照。
- `${VAR}` 与变量不存在时的 `${VAR:-default}` 从该后台解析；相对资源路径与实际项目 cwd 分开处理。定义字节、依赖脚本或绑定环境变化时，输入/原 ID 恢复不能静默采用新版本。
- 禁用不读取或展开未使用的环境引用，仍提供合法的原生禁用配置。重新启用时为候选版本核对当前引用，预览到保存之间发生变化会拒绝；重试与工作台重启不重新绑定已保存候选。未改动的专属资产、原快照及旧操作保持原版本。
- 界面显示资源来源、定义摘要、引用名和记录时的存在状态，并区分禁用未读取与保留先前记录。原生清单和版本记录分开表达。Codex 资源改配仍限已选服务启停；新增、恢复继承、同名版本替换及远程传输尚未开放。

## 原生依据与实际检查

固定版本：Codex CLI **0.157.1**、app-server **0.158.0**、Claude Code **2.1.280**、SDK **0.3.280**、Node **24.13.0**、React **19.3.0**、Go **1.26.2**。基线为 `63a584c`；源码、构建及版本信息见本地 [manifest](W02-09-shared-mcp-profiles/manifest.json)。

已实际取得官方 [Codex MCP](https://developers.openai.com/codex/extend/mcp) 与 [app-server](https://developers.openai.com/codex/app-server) 文档，并保留[读取结果](W02-09-shared-mcp-profiles/official-docs/search.json)。配置参考页重定向后的 TLS 读取失败，没有以搜索摘要补全事实；接口结论仍以固定版本实测为准。

| 层次 | 实际结果 |
|---|---|
| 定义覆盖原生探针 | [25 项通过](W02-09-shared-mcp-profiles/raw/definition-final/result.json)，三次实际 MCP 调用、六次本地模型请求。`thread/start.config` 可加入 profile 原本没有的服务；原生 `env` 不展开 `${VAR}`，`env_vars` 可读取后台环境；每次调用均需原生审批，cwd 正确、profile 不变，rollout 未保存传入的假 token。 |
| 四配置 Application / HTTP / CLI | [206 项通过](W02-09-shared-mcp-profiles/verification/joint-changes-third.log)。四份实际模型请求在 barrier 重叠，共用一条资产及来源版本，各自另选专属资产；核对精确 call ID / tool use ID 的结果回传、逐次审批、四配置共享脚本漂移和 Codex A 专属脚本漂移的影响范围。工作台重启保留原 ID/快照；八次明确禁用/启用保留历史和专属绑定。停止 Codex A 自有后台期间，其他三份仍实际调用工具。禁用后工具不暴露、不执行，未配置凭据的禁用定义可创建但不能直接启用。 |
| Go 边界与持久化 | [Application 定向 19 条 PASS](W02-09-shared-mcp-profiles/verification/app-binding-first.log)、[Runtime 定向 33 条 PASS](W02-09-shared-mcp-profiles/verification/runtime-binding-first.log)。覆盖返回元数据错配/缺失、绑定先于原生派发、SQLite 重启和查询副本、预览后环境变化、幂等重试、多服务启用、来源漂移、同名冲突及真实自有进程的环境读取；重复变量和未授权/缺失变量拒绝。 |
| 浏览器 | [45 项通过](W02-09-shared-mcp-profiles/verification/browser-first.json)，使用实际三阶段 HTTP DTO 和最终 Web 资产。四配置创建表单、启停 payload、来源/环境元数据、禁用/历史/重新启用、两类 CLI 的改配边界、390px 布局均核对。缺失、旧格式、未知格式和默认值为明确的显示夹具；四次创建请求被拦截用于核对表单，没有新增真实执行。 |
| 原始材料独立复核 | [228 项通过](W02-09-shared-mcp-profiles/verification/audit-first.json)。只读 HTTP 帧、SQLite、原始模型请求和工具日志，重算定义摘要与环境绑定，核对版本链和每次结果/审批。共 **62 次本地模型请求、27 次实际 MCP 调用、27 次单次审批**，没有重发模型轮次。 |
| 常规检查 | [app / runtime / console 全包 race](W02-09-shared-mcp-profiles/verification/race-first.log)通过（142.252s / 37.757s / 1.946s）；[两类 native 标签的 cmd/internal/tests vet](W02-09-shared-mcp-profiles/verification/vet-all-tags.log)、Web 类型检查/构建、Go 开发构建、Python/JavaScript 语法和 diff 检查通过。 |

已查看[桌面](W02-09-shared-mcp-profiles/verification/browser-first/native-1440.png)和[390px 详情](W02-09-shared-mcp-profiles/verification/browser-first/native-390.png)，来源、绑定状态和限制可见，无横向溢出。

原生联合测试的 backend 与最终 backend 源码一致；被测二进制早于新增 UI 构建，SHA-256 为 `dc35a411b5fa3093653b8e01265316e8fe64c9328a945baf78920f539bc5afa7`。Web 完成后再构建的最终 `.dev/bin/netcap` 为 `a876ccfb6c9283a9d38026cf6924388257e4b57a147d840efd909f880b0e6e9a`，浏览器使用最终 `index-BEO6SgtF.js`。两份产物及对应哈希均本地保留。

## 失败反馈与限制

生产 Application 最初以“必须有唯一已登记映射”拒绝共享定义，[原始红灯](W02-09-shared-mcp-profiles/verification/shared-selection-red.log)在接入资源路径后转绿。后续边界测试真实捕获 `scripts/` 相对路径错误、禁用仍读取缺失凭据、`args:null`、`env:null` 和嵌套默认值五项问题；[失败](W02-09-shared-mcp-profiles/verification/definition-boundaries-red.log)及[修复结果](W02-09-shared-mcp-profiles/verification/definition-boundaries-green.log)均保留。旧 UI 缺少共享定义选择器的[浏览器红灯](W02-09-shared-mcp-profiles/verification/browser-red.json)也已转绿。

夹具失败分别保留：首个定义探针在 rollout 首次落盘前读取失败，已改为同一会话继续轮询，并修正中断测试仍报告 `passed=true` 的汇总；首轮四配置创建在 View 尚未投影时过早断言，修正后先通过 68 项、再通过 91 项；启停扩展先误读 operation 顶层 `kind`，随后空工具分支错误复用非继承实例的 `super()`，导致本地供应商 HTTP 500 和未知操作。后者有独立[红灯](W02-09-shared-mcp-profiles/verification/provider-exclusion-red.log)/[绿灯](W02-09-shared-mcp-profiles/verification/provider-exclusion-green.log)，修正后最终联合 206 项通过。这些夹具失败不计为生产功能通过，旧失败结果不被覆盖。

`mcpServerStatus/list` 报告服务名、状态、serverInfo、工具等，未提供完整已加载命令/环境。因此同名服务状态不能证明定义版本切换，历史 `used_tools` 也不能自动归属新的同名资产。Codex 新增改配、同名资源版本替换和恢复继承仍需加载回执、来源与历史归属证据；远程定义、完整环境/可执行依赖、外部文件竞态、受管/remote/helper 来源新鲜度、动态权限及 Skill/插件混合四配置矩阵继续保留。

全部原生实验使用 `/var/tmp` 自有目录、bwrap user/PID/net/mount 隔离、自有策略目录、模拟注册表、假凭据与本地供应商；没有调用真实供应商或用户后台。旧 `bin/netcap` SHA-256 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，依赖、锁文件、Claude bridge 和既有原始证据保持不变。当前结果只收口共享 stdio 这一联合场景，不提升完整 W02 或 P11–P15 状态。

## 复跑与归档

```bash
npm --prefix web run build
mkdir -p .dev/bin
go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
python3 tests/codex_mcp_definition_probe.py
python3 tests/shared_mcp_profiles_native.py --netcap "$PWD/.dev/bin/netcap"
python3 tests/shared_mcp_profiles_audit.py --native <输出的Fixture目录> --output <审计JSON路径>
go test -race ./internal/app ./internal/runtime ./internal/console -count=1
go vet -tags=nativeclaude,nativecodex ./cmd/... ./internal/... ./tests/...
```

浏览器入口 `tests/shared_mcp_profiles_browser.js`：`__FIXTURE__` 使用联合脚本的 `result.json`（至少包含 `sessions` 与 `initial/shared-disabled/reenabled-used` 三帧），`__ASSETS__` 为 `web/dist` URL 到文件的映射，`__OUTPUT__` 为截图目录，通过 Playwright 运行。

成功联合原目录为 `/var/tmp/netcap-shared-mcp-profiles-dgyj3x52/netcap-console-wtpfmh_5`，定义探针原目录为 `/var/tmp/netcap-codex-mcp-definition-gx5vf1m3`。[归档索引](W02-09-shared-mcp-profiles/archive-index.json)记录各原路径、原哈希和归档哈希；Go 源码副本压缩为 `.go.gz`。Git 同步源码、测试、构建资产与本 Markdown 报告，原材料、截图和开发二进制由已有 `.gitignore` 保留在本地。
