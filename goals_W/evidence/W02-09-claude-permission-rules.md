# W02.09 · Claude 原查询权限规则与热策略观察

日期：2026-10-10。已接通原查询的权限规则、来源、原生忽略标记、额外目录标签、解析错误和两次报告比较。**W02.09 仍为 in_progress，W 系列保持 9/40 done，Goal active。** 这片补充实际运行中的观察证据，不将规则清单提升为完整权限证明。

## 交付行为

普通 Claude 会话在“执行配置快照 → Claude 权限规则与来源”点击读取，可查看当前原生查询报告的 allow/deny/ask、来源、规则原文、保存位置和 `notInEffect`。保留重复声明与未知来源；同一来源、行为和规则同时出现不同忽略标记时，逐行标出冲突。支持来源筛选和分批展开；控制字符、双向控制符与字面反斜线分别转义，规则作为文本显示。

`POST /api/sessions/{id}/permissions/catalog` 只接受空对象，沿用工作台鉴权、原会话/快照/状态目录和读取前后归属检查。复用 SDK 公共 spawn 接缝中的 stdio 通道，仅发送固定 `list_permission_rules`，其他 SDK 帧、Unicode、大消息、并发关闭与迟到回执保持原有处理。没有开启查询时返回不可用；不启动 bridge 或查询，不更改权限，也不创建输入/审批操作。

响应的 `query_id` 是 bridge 为每次查询生成的观察身份，`configuration_id` 绑定保存配置；原生 ID 单独保留。`sample_hash` 覆盖规则、来源、忽略标记、额外目录、策略开关和错误位置，保留重复项，排除菜单排序与读取时间。页面只比较同一查询/配置的两次报告；读取失败、断线、切换会话和查询重开重置比较。解析错误只返回文件与字段，不传出原始诊断正文。报告不持久化为输入许可，`complete` 始终为 false。

## 原生事实与限制

固定 **Claude Code 2.1.280、SDK 0.3.280、Node 24.13.0、Go 1.26.2**。Context7 的官方资料查询及已安装 SDK 的 `SDKControlPermissionRulesState` / `SDKPermissionRuleEntry` 类型留在本地归档。在线 [权限说明](https://code.claude.com/docs/en/agent-sdk/permissions) 是参考；版本结论来自本轮实测，未假定 SDK Query 存在公开的权限清单方法。

直接协议样本发现：启动后、首条输入前修改受管设置，清单可同时保留旧规则和标为不生效的重复项。实际 Read 对照进一步确认文件监听存在过渡期：热修改后的最早一次调用仍被原用户规则拒绝，后续同一 ID 的调用才按新受管策略放行。没有重复标记也不能证明策略已同步；恢复策略的某次报告已返回 managedOnly=false，却仍列着旧的 policy ask。保存摘要或连续两次相同报告均不能冻结下一动作权限。

额外工作目录也存在来源标签限制：目录实际声明在用户 settings.json，原生返回 `source=localSettings`；两份文件的实际字节确认没有迁移。工作台保留原生标签并明确它不能用于定位真实文件，不按标签补造来源路径。

## 本轮验证

| 层次 | 实际结果 |
|---|---|
| Application / HTTP / SDK / CLI | **78 项通过**：双配置、五类设置来源、热策略、审批中读取、解析错误、关闭后读取拒绝及原 ID 冷续聊 |
| 原始材料独立审计 | **469 项通过**：逐项核对 **58 次** HTTP 权限读取与原生请求/回复，独立重算摘要、核对日记、源文件及 SQLite |
| 浏览器 | **34 项通过**：真实 Handler 报告回放、1440/390 视口；空、未知、冲突与迟到响应另用明确的显示契约夹具 |
| SDK / 传输 | **64 项通过**：新规则解析、帧透明传递、关闭与错误、原有审批 hook 和指令通知回归 |
| Go | runtime/app/console 三包完整 race；双 native 标签相关四包 vet 通过 |
| 构建 | bridge TypeScript、Web TypeScript/Vite、开发 Go 构建通过 |

最终原生目录为 `/var/tmp/netcap-permission-catalog-kzvuurom`。两份自有 profile 使用同一工作目录但独立状态与来源；A 完成 **5 条明确输入、10 次本地模型请求、5 个 Read 请求、2 次单次审批**，B 没有模型输入。原用户 deny、managed-only allow、managed ask 下允许一次/拒绝下一次，以及恢复原规则后的 deny，均由真实工具结果和交给本地供应商的请求核对。第二次允许的 Read 使用更新后的自有文件，核对返回新字节；没有把“文件未变，参考上次结果”当成新读取正文。

读取没有新增操作，SQLite 仅有两次创建、五次发送和两次审批回复。原会话 ID、执行快照、配置与历史保持；原生查询关闭时清单不可用，明确续聊按原 ID 重开后使用新的观察 query_id。浏览器没有重放原生输入或审批，所有请求均有映射且没有执行命令。

所有原生实验位于 `/var/tmp` 自有目录，使用 bwrap user/PID/network/mount 隔离、自有 `/etc/claude-code` 映射、受控缺席的 Windows 注册表入口与本地脚本化供应商。没有调用真实供应商、操作用户后台或修改宿主策略。Windows 注册表与动态企业远端服务不在本轮实测范围。

## 失败记录与产物

失败材料均保留：首次将额外目录标签预期为 userSettings；第二次用包含 `/` 的原生 pending ID 作为 command_id；第三次按 JSON 语义恢复文件造成字节变化，并误把 Read 缓存返回视为新正文；第四次将解除暂停误当成已开启查询。最后按原文件字节恢复、为新读取更新自有数据，并等待真实 sdk_offline 后明确续聊，完整流程通过。没有放宽产品的来源或审批守卫。

首次审计将 SourceStamp 误当裸文件哈希，四项失败；按既有“相对路径、文件模式及正文哈希”契约独立计算后全部通过。浏览器首轮即通过。

本地归档：[manifest](W02-09-claude-permission-rules/manifest.json)、[逐项索引](W02-09-claude-permission-rules/archive-index.json)、[归档核对](W02-09-claude-permission-rules/archive-verification.json)。包含直接协议样本、全部原生失败/成功材料、原始审计、浏览器截图、编译/测试日志及源码和构建副本；Go 源码与二进制采用 gzip，避免被 Go 包发现误扫。原始材料由 `.gitignore` 排除，只有顶层 Markdown 报告随 Git 同步。

最终 `.dev/bin/netcap` SHA-256：`230ef63fed1dd09490bf65e261b7b8732725127659f8dcd8c42b1ffa42008627`。最终原生测试构建：`8e8b350270a80002fd00f0d99f64897147e3bedc8c9dfebade44da9e24be4751`。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。生产包由真实 Application/Handler 原生测试执行；开发 CLI 完成构建，浏览器使用最终 Web 产物。依赖未变，旧原始证据未修改。

复跑入口：

```bash
npm --prefix bridges/claude run build
npm --prefix web run build
go test -race ./internal/runtime ./internal/app ./internal/console
go test -c -tags=nativeclaude -o .dev/permission-native.test ./internal/runtime
python3 tests/claude_permission_catalog_native.py --binary "$PWD/.dev/permission-native.test"
python3 tests/claude_permission_catalog_audit.py --native <输出的原生目录> --output <本地审计JSON>
```

完整权限模式、沙箱/工具/账号策略联合、remote/helper 来源真实性与新鲜度、读取后竞态、四配置剩余组合、真实方法/Gate、独立前台与输入独占、Windows 第二环境等硬条件继续保留。下一片继续热策略变化与原输入审批、来源版本的联合核对，不将这份按需读取报告当作持久授权依据。
