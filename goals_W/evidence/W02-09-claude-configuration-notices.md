# W02.09 · Claude 配置变化通知与审批时序边界

日期：2026-10-11。起点 `b278795`；W02.09 保持 `in_progress`，整体仍为 9 done / 8 in_progress / 23 pending。此片接通原查询的配置通知、旧审批失效和持久接收历史，未完成动态权限、完整来源、冻结或全部 W 系列条件。

## 用户可见结果

“执行配置快照”摘要显示当前查询通知数，展开后显示来源、可选路径、收到时间、当前/先前查询、登记状态、记录失败及截断提示。未知来源原样保留，控制字符、双向字符和字面反斜线分别转义。只展示最近 128 条有效通知，计数覆盖本查询全部有效记录，原日记不截断。

原输入在途期间收到同查询通知后，旧工具请求不可再批准，即使规则报告仍完全一致。页面保留原请求和明确拒绝入口；不自动拒绝、中断、重发输入或重试工具。下一条明确输入建立自己的观察。来源文件漂移后的拒绝继续使用原连接、原生 ID、请求和执行快照。

接收记录只保存元数据，不读取正文。`prompt_id` 是独立的原生标识；“接收时的在途输入”由每个查询的日记顺序核对，不表示因果影响或实际加载版本。只有保存的原 `send` 操作能提供该输入的原快照；空闲 B 不会借用 A 的输入或当前快照。

## 实现边界

- SDK 在原查询登记被动 `ConfigChange`，追加 `configuration_notice` 日记后使同查询在途审批失效。原项目/本地阻止回调数组保留；新回调始终返回 `{}`，记录失败也不改变原生配置决策。
- 通知使依据转为 `unavailable`，可带 `configuration_notice_id`。随后真实规则差异可细化为 `changed`，但相同报告或还原不能重新授权旧请求。初始采样期间已经收到通知时，也不把该次采样当作可批准基线。
- 登记回执、记录失败与通知序号绑定每次查询对象；旧查询迟到的有效/失败回调不污染新查询。新查询的批准需确认登记；SDK 日记写入失败仍允许明确拒绝。
- Go 独立校验通知元数据、唯一 ID、原输入边界和查询/配置绑定；不能信任原始 `current`、接收操作或快照字段。匹配日记中的通知也会使看似未变的旧 guard 失效。损坏通知、重复输入或不可用基线不能被展示为可批准。
- `View.configuration_notices` 经 Application/SQLite 和 Handler 保存、展示。重开后保留原通知及原输入快照，关闭的查询不再声称登记有效或属于当前查询。`complete` 始终为 false。
- 登记依据是原生 `hooks_applied=true`。固定 CLI 的 hook 清单不列出 SDK 回调；不能以清单为空否定登记，也不能把初始化回执提升为全部通知覆盖。

入口：`bridges/claude/src/configuration_notices.ts`、`permission_approvals.ts`、`session.ts`；`internal/runtime/claude_configuration_notices.go`、`claude_permission_approval.go`；`internal/app/claude_configuration_notices.go`；`web/src/ConfigurationNotices.tsx`。使用说明同步于 [CONSOLE](../../CONSOLE.md)。

## 原生结果

固定 Claude CLI 2.1.280 / SDK 0.3.280，Node 24.13.0、Go 1.26.2。真实 Application/SQLite/Runtime/Handler 在自有 bwrap 用户、PID、网络和挂载空间运行；`/etc/claude-code` 绑定到自有目录，模型仅连接本地脚本供应商。Windows 注册表缺席由测试夹具表达，不算真实 Windows 策略验证。

最终原生测试构建上的六场景共 **167 项通过**：

| 场景 | 检查数 | 实际结果 |
|---|---:|---|
| 普通受管通知 | 31 | 仅改无害 env，规则相同，旧批准阻止、原拒绝成功；SQLite 重开保留通知和原快照 |
| `disableAllHooks=true` | 27 | SDK 通知实际到达；旧批准不可用，未以原生 hook 清单推断登记 |
| `allowManagedHooksOnly=true` | 27 | 保留仅受管权限规则开关；原查询通知、拒绝和 A/B 隔离通过 |
| 用户设置 | 26 | A 收到用户通知、规则相同；B 未收到该用户来源通知，零模型输入 |
| 项目设置 | 28 | 原 block 和新增空决策回调均执行；A/B 通知分属各自查询 |
| 项目本地设置 | 28 | 原 block 保留，漂移后拒绝原请求成功；B 无输入关联 |

以上六条明确输入各有一个原 Read、一个对应结果和两次本地模型请求；B 均无模型请求。受管/项目/本地变化到达空闲 B 时，原生通知没有 `prompt_id`，产品不填输入或快照。此事实只覆盖本次样本，不将缺少提示 ID 推广为所有空闲通知的契约。

最终版本另重跑原七场景（未变、收紧、还原、无效受管、无效用户、本地漂移、中止），**158 项通过**。明确拒绝继续保持原 ID/请求/快照；恢复相同规则不重新授权。无效用户设置本次未产生可保存通知，仍由原解析错误与不可用观察阻止批准，进一步说明通知覆盖不完整。

## 验证与产物

| 层次 | 最终结果 |
|---|---|
| SDK | 103 项通过：通知元数据 12、原审批/查询生命周期 25、指令通知 33、hook/权限清单 27、本地设置阻止 6 |
| Go 定向 race | 80 项（含子场景）通过，覆盖通知接收归属、引用校验、旧查询、损坏记录、基线和原快照绑定 |
| 原生材料独立复核 | 通知矩阵 380、原审批回归 216 项通过；从 wire、journal、Handler 和供应商请求重新核对 |
| 浏览器 | 新通知 43、原审批回归 26 项通过；真实 Handler 帧回放，1440/390 宽度、明确回复、断线、历史重开与展示故障变体 |
| 完整 race | `go test -race ./internal/runtime ./internal/app ./internal/console` 通过 |
| vet | 双 `nativeclaude nativecodex` 标签的 runtime/app/console/cmd/netcap 通过 |
| 构建 | bridge TypeScript、Web 类型/生产构建、`.dev/bin/netcap` 通过 |

最终开发二进制 SHA-256：`3edc574df907e1947ee3de614ed4c6f63b611d6070876c801e535de1f0f0839c`。

最终原生测试二进制 SHA-256：`4c592269e59e827f0e1101e3347bf4ccead98b69b0e2f28cc6842ada853947eb`。

旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。依赖版本未变，旧证据未覆盖；仅开发构建及相应 `web/dist` 更新。

失败材料保留：最初 4 个 SDK 回归因缺少观察器/合并而失败；收尾的 13 个 Go 子场景复现损坏通知、重复输入和不可用基线仍保留允许标记。两处修复后重跑相关验证，最终原生与浏览器矩阵重新生成。中间成功构建与最终构建分别存档，不混计为最终通过数。

## 未关闭的时序硬条件

下列前置实验使用起点 `b278795` 的原生 Session 和公开 SDK spawn/stdio 接缝，只操作自有 Read 与自有受管文件。测试只延迟原 SDK allow 的写入，不改其字节；独立的只读权限查询回复不进入生产采样守卫。

| 时序 | 实际结果 | 结论 |
|---|---|---|
| 最后检查之后 | 延迟原 allow；改为 deny；同一 stdio 的独立读取确认 CLI 已报告新 deny；原样放行旧 allow 后仍读到正文 | `current_deny_prevents_read` 失败，不能声称批准原子性 |
| 首次采样之前 | 自有 policy 已改，原生监听尚未更新；采样与审批采用旧 ask，Read 返回正文 | 相同报告不证明当前文件版本已生效 |
| 模型请求等待期间 | 等待原查询清单报告新 deny，再释放模型 Read；原生直接拒绝，没有审批请求 | 此具体顺序通过，不覆盖前两种窗口 |

三个时序实验各保留原 ID、1 条输入、1 个 Read/对应结果和 2 次本地模型请求。前两项各有一条硬条件失败，第三项 6 项通过。另有普通/禁用/仅受管三组直接 SDK `ConfigChange` 观察实验，每组 5 项通过，用于确认接口与登记行为。

本片通知守卫能拒绝已收到通知后仍待提交的批准，但没有验证原生的条件批准/策略版本原子复核原语；已经交给 SDK 的允许不能靠事后通知撤回。前两项失败不能因通知矩阵通过而关闭。实际文件版本、全部变化覆盖、未触发审批的工具、完整权限、冻结、Windows 策略、输入独占与方法/Gate 均保留相应硬要求。

## 复跑与本地归档

```bash
npm --prefix bridges/claude run build
node tests/claude_configuration_notices_test.mjs
node --experimental-test-module-mocks tests/claude_pending_permissions_test.mjs
go test -race ./internal/runtime ./internal/app -run 'TestClaude(ConfigurationNotice|ApprovalNotice|ApprovalProjection|ToolDenial|DenialUses)' -count=1 -v
go test -tags=nativeclaude -c -o .dev/native-configuration-notices.test ./internal/runtime
python3 tests/claude_pending_permissions_native.py --binary .dev/native-configuration-notices.test --case policy-notice
```

其余新模式为 `policy-disabled-notice`、`policy-managed-notice`、`user-notice`、`project-notice`、`local-notice`；原七模式继续可用。每次原生入口打印独立证据目录。将 `{case, root}` 行组成 JSON 数组后，可分别用 `tests/claude_configuration_notices_audit.py` 或 `tests/claude_pending_permissions_audit.py` 的 `--matrix` 与 `--output` 只读复核。

本地原始材料：[归档](W02-09-claude-configuration-notices/)、[清单](W02-09-claude-configuration-notices/manifest.json)。其中保存最终/中间矩阵、原始失败、三种时序、三组 SDK 观察、wire/journal/SQLite、浏览器截图及源码/构建副本；Go 源码副本和 ELF 使用 gzip，避免被 `go test ./...` 当作项目包。原始材料按 `.gitignore` 留在本机，Git 只同步本 Markdown 报告和可复用测试入口，详见[证据约定](README.md)。

下一片转回前置独立的 W01/宿主控制与输入交接工作；只有取得新的原生条件批准能力或环境变化时再重验上述原子窗口。完整目标与硬要求保持，Goal 仍为 active。
