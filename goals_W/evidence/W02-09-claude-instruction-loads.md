# W02.09 · 原生指令加载记录与原输入归属

日期：2026-10-10。执行详情现在展示持久保存的 Claude 指令加载通知，包括汇总遗漏的导入与嵌套来源、当前或先前查询，以及有原生证据时的原输入操作和原操作快照。启动、Read、工作台重启与原 ID 恢复已贯通真实 Application / SQLite / Handler / SDK / CLI。

本片延续[来源汇总](W02-09-claude-instruction-sources.md)和[加载生命周期边界](W02-09-claude-instruction-lifecycle.md)。仍为 **9/40 done，W02.07 / W02.08 / W02.09 in_progress，Goal active**。加载通知不含正文摘要，本片不绑定已加载文件版本、不声明完整来源或授予权限。

## 行为与关联依据

- Bridge 白名单保存路径、原生来源/原因、父文件、触发文件、globs、原生提示/子代理标识及时间；不复制正文、工具参数或任意回调字段。每次打开分配独立查询标识，区分查询绑定 cwd 与通知实际 cwd。
- `InstructionsLoaded.prompt_id` 与 SDK 输入 UUID 是两个独立标识。生产代码通过同一查询的 `PreToolUse.tool_use_id`、原生 assistant 工具块及其明确的 `user_message_uuid` 连接到已保存输入。迟到通知不用当前 active 操作兜底；跨查询、配置、原生身份、replay、子代理、缺证据或多个候选均不指定操作。
- Application 再核对持久操作的 Session、原生 ID、cwd、状态目录、实例和 send 类型，引用该操作已经保存的 `ExecutionSnapshotID`，不会改用当前快照。
- 已写入日记的通知和关联证据随原 ID 恢复保留。关闭或断线只展示保存记录；恢复后的新查询与先前查询分开。View 最多展示最近 128 条有效通知，原生日记保留；未知类别原样展示，缺失、有效空记录、损坏与截断分别表达。
- 加载与关联观察器返回空决策；新增 PreToolUse 观察器追加到既有 ask 回调。原生审批约束的初始化 ACK 与权限处理保持原契约。

## 实际验证

固定版本：Claude Code **2.1.280**、SDK **0.3.280**、Node **24.13.0**、React **19.3.0**、Go **1.26.2 linux/amd64**。版本、源码及构建哈希见本地 [manifest](W02-09-claude-instruction-loads/manifest.json)。

| 层次 | 结果与范围 |
|---|---|
| 原生 Application / Handler | [31 项通过](W02-09-claude-instruction-loads/verification/native-loads-final.log)。A/B 各七项启动来源；A 的 Read 新增嵌套通知，重启后八条保留，原 ID 恢复后十五条区分当前七条/先前八条。只产生两次 create、三次显式 send、四次本地模拟模型请求。原操作与快照、来源汇总遗漏、B 会话隔离均核对。 |
| 独立原生标识探针 | [53 项通过](W02-09-claude-instruction-loads/raw/prompt-probe/result.json)。确认 prompt 与输入 UUID 不同、PreToolUse payload/回调工具 ID 相同，以及指令加载回调的第二参数并非该工具 ID。 |
| 受管工具审批 | [四策略共 574 项通过](W02-09-claude-instruction-loads/verification/native-approval-final.log)：managed-only 145、all-sources 139、managed-hooks-only 145、all-hooks-disabled 145。原/派生设置下共 88 个工具调用、30 次审批、32 次原生拒绝；176 次本地模拟请求。新增观察器没有覆盖既有 ask。 |
| SDK 与 Go 合约 | [加载/关联/审批 37 项](W02-09-claude-instruction-loads/verification/sdk-instruction-contracts.log)、[来源/设置守卫/失败恢复/hook 回归 37 项](W02-09-claude-instruction-loads/verification/sdk-session-regression-final.log)通过；[Runtime / Application 定向检查](W02-09-claude-instruction-loads/verification/go-instruction-contracts.log)通过，含歧义与跨查询拒绝、旧记录、截断和原快照归属。 |
| 浏览器 | [35 项通过](W02-09-claude-instruction-loads/verification/browser-final.json)。使用实际四阶段 Handler DTO 和最终生产资产，验证来源、导入/触发文件、两个独立 ID、原快照、重启、切换会话与断线；旧 bridge、空记录、失败、截断、未知类别和子代理为明确的合成显示边界。没有发送执行命令或新增清单请求。 |
| 原材料交叉核对 | [139 项通过](W02-09-claude-instruction-loads/verification/audit-final.json)。从原生日记、工具消息、只读 SQLite、模型请求和审批回执重新核对关联链与策略结果，没有重发模型轮次。 |
| 常规检查 | [runtime / app / console 全包 race](W02-09-claude-instruction-loads/verification/go-race-final.log)通过（36.422s / 151.931s / 2.128s）；带 nativeclaude/nativecodex 标签的 cmd、internal、tests 目录 vet、Bridge/Web/native 测试编译、开发构建和语法/格式/diff 检查通过。最终 UI 提示修复后 Web 与开发二进制重建，console 检查返回缓存通过，不另算一次新执行。 |

已查看[桌面启动来源](W02-09-claude-instruction-loads/verification/browser/startup-1440.png)及[390px 嵌套通知与原操作](W02-09-claude-instruction-loads/verification/browser/nested-390.png)截图，无横向溢出。[恢复后的历史通知](W02-09-claude-instruction-loads/verification/browser/restored-390.png)保留原操作。

原生测试执行的 backend 与最终 backend 源码一致；其测试二进制早于最后一处 UI 提示修复。浏览器使用修复后的 `index-CLcSE7bv.js`，SHA-256 `c0a5774f3f08c07c67026c714eff3804d9d8f4860515fb15a973e87235efb800`；最终 `.dev/bin/netcap` 为 `85da646a56ea2fdbc3c7509de562f0fa2b3dd86c3be97297acd2bf0148cd02d1`。

## 先失败后修复与证据范围

首轮新实现的原生入口在 `nested_original_input_and_snapshot` 失败，19 项记录中前 18 项通过。[原始结果](W02-09-claude-instruction-loads/raw/native-red/loads-result.json)与 SQLite 显示操作/快照存在，但原生 prompt ID 不等于输入 UUID。最小协议回归[先红](W02-09-claude-instruction-loads/verification/prompt-join-red.log)[后绿](W02-09-claude-instruction-loads/verification/prompt-join-green.log)，随后用上述实际工具证据修复生产关联。该最小 fixture 按真实协议模式整理，不是完整原始 wire。

浏览器首轮夹具误把工作区摘要当作包含完整 View 的详情，已修正可选字段处理并保留[夹具错误](W02-09-claude-instruction-loads/verification/browser-fixture-error.txt)。之后发现“不可用且记录失败”分支隐藏诊断提示，[28 项中的这一项失败](W02-09-claude-instruction-loads/verification/browser-red.json)；将缺口提示移到可用性分支之外后，最终 35 项通过。这是独立的 UI 修复，不作为原生加载失败计数。

所有原生实验均位于 `/var/tmp` 的自有目录，并以 bwrap 隔离 user/PID/network/mount namespace，使用自有 `/etc/claude-code`、受控缺席的注册表回复、假凭据与本地模拟供应商。没有调用真实供应商、用户后台或真实 Windows 策略。旧 `bin/netcap` SHA-256 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`；依赖、锁文件、既有原始证据与 `.gitignore` 未修改。

记录仅覆盖成功收到并写入的通知，不补齐丢失回调；记录失败标记是当前 Bridge 进程的诊断状态，不能充当跨重启的完整性证明。版本绑定、完整受管/remote/helper 来源与新鲜度、动态权限、外部竞态、四配置原生联合、W02.07/08 其余条件，以及全部首版硬要求继续保留。下一片继续完整来源与权限的联合场景，不以这组元数据替代全部有效配置。

## 复跑入口与本地材料

```bash
npm --prefix bridges/claude run build
npm --prefix web run build
mkdir -p .dev/w0209-instruction-loads
go test -c -tags nativeclaude -o .dev/w0209-instruction-loads/runtime.test ./internal/runtime
python3 tests/claude_instruction_sources_native.py --loads --binary .dev/w0209-instruction-loads/runtime.test
python3 tests/claude_instruction_lifecycle_probe.py --case nested-read --prompt-correlation
TMPDIR=/var/tmp python3 tests/claude_managed_settings_access_probe.py
node --experimental-test-module-mocks --test tests/claude_instruction_loads_test.mjs tests/claude_tool_approvals_test.mjs tests/claude_settings_guard_test.mjs tests/claude_configuration_failure_test.mjs tests/claude_configuration_recovery_test.mjs tests/claude_hook_execution_test.mjs tests/claude_instruction_sources_test.mjs
go test -race ./internal/runtime ./internal/app ./internal/console
go vet -tags nativeclaude,nativecodex ./cmd/... ./internal/... ./tests/...
```

浏览器入口为 `tests/claude_instruction_loads_browser.js`：将 `__FIXTURE__` 替换为原生入口生成的 `loads-browser-fixture.json`，`__ASSETS__` 替换为当前 `web/dist` URL 到文件的映射，`__OUTPUT__` 替换为截图目录，经 Playwright 运行。原材料审计入口为 `tests/claude_instruction_loads_audit.py --native <Application 输出目录> --prompt <标识探针目录> --approvals <审批矩阵目录> --output <审计 JSON>`，不启动 CLI。

本次目录依次为 `/var/tmp/netcap-instruction-sources-p3ut7o97`、`/var/tmp/netcap-instruction-lifecycle-32ziaar1`、`/var/tmp/netcap-managed-settings-access-xmml7u83`。必要原材料、SQLite 只读投影、截图、源码副本和构建元数据已归档于同名本地目录；[归档索引](W02-09-claude-instruction-loads/archive-index.json)记录原路径、原哈希与归档哈希，Go 源码副本压缩为 `.go.gz`。Git 只同步本 Markdown 报告，原始材料继续由 `.gitignore` 排除。
