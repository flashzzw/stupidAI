# W02.08 / W02.09：受管策略下的逐次工具审批

2026-10-09。真实 CLI 复现：`allowManagedPermissionRulesOnly=true` 会忽略工作台注入的 `flagSettings.ask`。受管 allow 的 Edit 已在 `read_only` 会话中无审批修改文件；显式选择的 MCP 工具也可直接执行。现通过 SDK `PreToolUse` 返回 ask，并要求初始化确认 hook 已注册，恢复这些工具的逐次审批。原生 deny 继续直接阻止调用。

这片修复既有审批承诺，继续核对原文件与同目录派生文件的实际权限和插件来源。**W02.08/W02.09 仍为 in_progress，整线 9/40 done，Goal active。** 已安装插件的格式2候选可保存，原 ID 应用与完整启动准入仍未开放。

## 实现与版本

- [tool_approvals.ts](../../bridges/claude/src/tool_approvals.ts) 只匹配只读会话已有的 `Bash / Write / Edit / NotebookEdit`，以及显式启用的 MCP 和 CLI 插件 MCP 命名空间。名称转义并锚定；未选择的服务、Read/Glob/Grep 不新增审批。
- [Session](../../bridges/claude/src/session.ts) 合并审批 hook 与原有设置守卫，只有 `settings_guard_version` 存在时注册 `ConfigChange`。有审批 hook 时必须收到 `hooks_applied=true`；缺失或 false 均在 initialized/input 记录前拒绝。错误检查在复用 query 之前执行，SDK 尚未完成关闭时的 send 重试也不能绕过。原有 flag ask、工具输入和权限模式保持。
- 固定 **Claude Code 2.1.280、SDK 0.3.280、Node 24.13.0**。SDK hook 只返回 ask，不返回 allow 或改写参数。测试及构建源码见 [差异](W02-09-claude-managed-tool-approvals/source.diff)与 [manifest](W02-09-claude-managed-tool-approvals/manifest.json)。Go/Web/依赖未改。
- 本地开发构建 `.dev/bin/netcap` SHA-256 仍为 `123b59f2a09a80fb227e37db60d0b6f2eae4452a56db6b925028186f2a995594`；旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。bridge 的两个构建文件另见 manifest。

官方 [permissions](https://code.claude.com/docs/en/agent-sdk/permissions) 与 [hooks](https://code.claude.com/docs/en/agent-sdk/hooks) 说明了自动允许会跳过 `canUseTool`、可在 PreToolUse 控制审批；本轮 Context7 查询保存在[官方资料摘录](W02-09-claude-managed-tool-approvals/official-hook-permissions.md)。在线资料未固定版本，权限结论以本轮 2.1.280/0.3.280 实测为准，不能套用后续版本新增规则。

## 真实红灯与修复后结果

[Edit 基线](W02-09-claude-managed-tool-approvals/native-red.log)的原/派生两阶段均将 `OWNED_CUSTOM_DENY_BEFORE` 改成 `OWNED_CUSTOM_AFTER`，审批次数为 0，要求 1 次的断言失败。普通来源合并对照通过。记录同时包含真实工具结果、实际文件、原生日记及供应商请求，不只依据 fixture 的汇总。

[MCP 基线](W02-09-claude-managed-tool-approvals/managed-mcp-red.log)复用了当前开发二进制及依赖，仅将 Session 构建换成本片开始前版本（SHA-256 `2c464424f8b2e66b3e3be917467b673d44ed8a09a3c12ce7c7ea2d0410af4c9f`）。原生规则读取确认精确 allow `mcp__identity__whoami` 来自 policySettings，managedOnly=true。A/B 各实际调用一次并将正确凭据/目录的结果交给各自供应商；SQLite 只有创建和发送操作，没有 respond，随后夹具等待审批超时。外层 result 为空不能作为通过依据；决定性材料是[实际调用](W02-09-claude-managed-tool-approvals/raw/mcp-red/A-calls.jsonl)、运行日记、供应商请求与 [SQLite 投影](W02-09-claude-managed-tool-approvals/raw/mcp-red/store-projection.json)。未另做旧 bridge 的插件绕过对照。

修复后的 Access 矩阵每组都以原/派生文件、同一原生 ID 各执行 11 类 Read/Edit/Write；阶段间重置自有样本数据：

| 策略组 | 检查结果 | 实际行为 |
|---|---:|---|
| managed-only | 145/145 | 非受管 deny 不生效；受管 deny 仍阻止，受管 allow 的写入进入逐次审批 |
| all-sources | 139/139 | user/project/custom deny 保留；其余写入仍逐次审批 |
| allowManagedHooksOnly | 145/145 | 普通启动 hooks 被限制，SDK 审批仍生效 |
| disableAllHooks | 145/145 | 普通启动 hooks 不执行，SDK 审批仍生效 |

共 **574 项通过**。从原始 wire 重算 **88 次工具调用、32 次原生拒绝、30 次工作台审批、22 次 SDK 写入审批 hook**。每个 hook 响应都是 ask；原生拒绝没有 `can_use_tool` 请求，允许的写入在对应审批回复之后才返回工具结果。8 次受管 Read ask 沿原有原生规则进入审批。原/派生的设置与来源层只差目标插件键，已采用参数及权限状态相同，实际权限行为一致；原 ID 和全部历史保留，私有派生文件结束后删除。

受管目录的明确绝对规则 `Read(//etc/claude-code/anchored/**)` 阻止了真实读取，拒绝内容未出现在交给供应商的 tool_result 中。Write 也遵循对应 Edit 路径限制，拒绝路径没有新文件。

真实安装的声明目录与缓存目录分别保存；只在缓存 startup.py 放入 canary。原文件阶段的 `CLAUDE_PLUGIN_ROOT` 和原生 `system/init.plugins[].path` 都指向声明目录，完整来源键为 `w0208-selected@w0208-local`；派生文件排除目标，缓存 canary 未执行。这些 Access 观察没有调用 reloadPlugins。**`system/init` 随每次模型轮次出现，第一份在输入之后，不能作为启动前只读准入。**

## 验证与复跑

| 层次 | 实际结果 |
|---|---|
| SDK 合约 | 66/66，含 matcher、仅 ask、缺失/false ACK、设置守卫、插件/hook/认证与配置恢复 |
| 真实 Session + 本地供应商 | 四策略、原/派生文件，574/574 |
| 真实 Application/HTTP + 受管精确 allow | MCP 39/39；CLI 插件 33/33，逐次允许/拒绝、A/B 隔离及原 ID 重启恢复 |
| 受影响入口回归 | 普通创建 25/25；格式2候选保存、幂等、重启、撤销 45/45，实际应用仍拒绝 |
| Go | runtime/app/console 三包 race 通过（console 命中缓存）；`nativeclaude` tagged vet 通过 |
| 构建 | bridge TypeScript、开发二进制、当前源码的 native runtime 测试二进制通过 |
| 独立审计 | [518 项通过](W02-09-claude-managed-tool-approvals/audit.json)，原始 wire、工具结果、文件、SQLite 投影和哈希分别核对 |
| 本轮未执行 | 浏览器（未改 UI）、真实 Windows/企业策略、真实外部供应商与独立前台输入权 |

原生夹具使用自有 profile、假凭据、本地供应商，以及 bwrap 独立 user/PID/network/mount namespace。`/etc/claude-code` 映射到自有目录；reg.exe 返回受控缺席，不算真实 Windows 验收。宿主策略、用户设置、共享 daemon 和旧二进制未修改。普通 Go 检查的 TMPDIR 在仓库外，原生夹具放在本片自有目录。

元数据 bare 读进程在收到全部回复后由夹具 SIGTERM 结束，wire 保留退出码 143；实际会话另以 closed 日记核对收尾。

在项目根目录执行，先确保固定 CLI/SDK 可用：

```bash
npm --prefix bridges/claude run build
node --experimental-test-module-mocks --test \
  tests/claude_tool_approvals_test.mjs tests/claude_settings_guard_test.mjs \
  tests/claude_plugin_selection_test.mjs tests/claude_hook_execution_test.mjs \
  tests/claude_hook_catalog_test.mjs tests/claude_mcp_auth_recovery_test.mjs \
  tests/claude_configuration_recovery_test.mjs
TMPDIR="$PWD/.dev/w0209-managed-settings-access-work/owned" \
  python3 tests/claude_managed_settings_access_probe.py
TMPDIR="$PWD/.dev/w0209-managed-settings-access-work/owned" \
  python3 tests/claude_managed_settings_access_probe.py --surface mcp
TMPDIR="$PWD/.dev/w0209-managed-settings-access-work/owned" \
  python3 tests/claude_managed_settings_access_probe.py --surface plugin
TMPDIR="$PWD/.dev/w0209-managed-settings-access-work/owned" \
  python3 tests/claude_policy_helper_admission_probe.py \
  --binary "$PWD/.dev/w0209-managed-settings-access-work/runtime.test" --surface candidate
TMPDIR="$PWD/.dev/w0209-managed-settings-access-work/owned" \
  python3 tests/claude_policy_helper_admission_probe.py \
  --binary "$PWD/.dev/w0209-managed-settings-access-work/runtime.test" --surface create
```

Go 测试二进制由当前源码的 `go test -c -tags nativeclaude -o .dev/w0209-managed-settings-access-work/runtime.test ./internal/runtime` 构建。MCP 旧 Session 对照另加 `--bridge "$PWD/.dev/w0209-managed-settings-access-work/baseline-bridge/dist"`；不要用当前 Session 覆写基线来伪造红灯。

## 保留的失败、材料和限制

- [最初夹具](W02-09-claude-managed-tool-approvals/native-first.log)误把 `Read(/anchored/**)` 视为锚定策略目录；实际进入工作台审批并被夹具拒绝，不能算原生 deny。之后使用明确绝对规则；单斜线受管路径语义仍未确定。普通 user allow 下的 Edit 本来就应受 read_only 审批，预期也已纠正。
- 最初泄漏检查扫描整个供应商请求，误将脚本提供的 `Edit.old_string` 当成泄漏；最终只检查实际 tool_result。早期内联 hook 实验、当时的临时 ConfigChange 合并与最终实现分开保存，不算最终产物的证据。
- 收尾补测发现 SDK 延迟关闭时，`open` 的 query 复用会跳过已有 fault，使拒绝初始化后的 send 被接受。缺失/false ACK 两例[先失败](W02-09-claude-managed-tool-approvals/ack-retry-red.log)，错误检查前移后完整 SDK 66 例通过，并重跑最终 bridge 的原生矩阵及受影响入口。这是生产 Session 与受控 SDK 关闭边界的证据，不宣称真实工具已在该窗口执行。
- 三轮审计假设失败均保留：把每轮都会出现的 `system/init` 当成一次初始化、混用 MCP/插件结果字段、把创建时一次 HEAD 探测当成模型输入。另一次原始副本核对发现 archive.py 在复制后修改，刷新归档后通过；没有修改原始测试结果来通过审计。
- [材料索引](W02-09-claude-managed-tool-approvals/archive-index.json)记录原路径、原哈希、归档哈希及脱敏；[附件哈希](W02-09-claude-managed-tool-approvals/artifact-hashes.json)覆盖本片产物。原始 SQLite、账号文件与 access-token 留在自有目录，仅保存必要投影和哈希；console.log 的访问令牌行已脱敏。10,897 个旧 W 证据和 31 个旧 goals 证据保持。
- [收尾检查](W02-09-claude-managed-tool-approvals/closeout.json)确认自有进程和私有设置文件无残留，最终 bridge 哈希、原二进制、任务状态与差异均已核对；未提交或推送。

本轮真实文件工具为 Read/Edit/Write；Bash/NotebookEdit 只验证了 matcher 与 SDK 合约，不能视为全部工具或策略组合验收。动态受管策略、remote/helper 真实性与新鲜度、读取后竞态、完整安装来源启动准入、原 ID 应用事务、四配置联合、独立输入权和真实 Windows 等硬要求继续保留。下一片继续将启动准入和实际加载来源证据连接到已保存候选，再推进原 ID 事务。
