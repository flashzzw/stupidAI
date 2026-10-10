# W02.07 · Claude 原会话 Skill 改配

2026-10-02；整体 9/40 done，W02.07 in_progress。本片扩展已有执行版本事务，支持 Skill 新增、启停、显式版本替换与恢复继承；保留完整方法 Gate 和来源联合验收要求。

## 用户可见结果与实现

“原会话能力改配”统一展示 Claude 的 MCP 与 Skill 选择，分别提供添加入口和版本选择。保存候选不会覆盖生效快照；准备后保持暂停，明确应用才关闭空闲自有查询，按原生 ID 重开并核对清单。资源库更新不会自动升级现有执行；旧快照进入历史，其他资产继续保持原版本。

Application 的候选构造、保存验证、Intent、晋升与历史记录均携带 Skill。差异使用 `kind: skill` 和资产名称，MCP 旧字段保持兼容。Runtime 区分原配置与目标 Skill，读取 SDK 原子持久记录中的 `skill_target`、`skill_previous`、`skills`，核对目标、历史摘要与原生来源；回执未知只核对，不重新应用。旧 MCP 事务没有这些字段时沿用其“Skill 不可改”的旧契约，不伪造历史选择。

Session 在准备、打开、输入和权限回复前核对 Skill。改配前后比较未涉及的命令/描述，释放外部包后核对其名称、别名及描述归属均消失；自然 Skill 撤去临时 override，恢复新查询的原始设置。候选失败后等待其自有查询退出，再保留原配置与 not_sent 记录。冷改配会先重开原查询取得清单基线；这些动作没有模型输入。

## 实际证据

复用已确认的 Application/Session 接口，按 TDD 先捕获两个失败：应用层把 Skill 禁用误认为没有差异，真实 Session 忽略新 Skill 目标。对应 `stage-red.log`、`native-red.log` 均退出 1；实现后同一入口转绿。没有更改自然 Skill 的目录键规则或外部包交付契约。

固定 Claude Code 2.1.280 / SDK 0.3.280，隔离 profile、项目、状态目录和开发构建。原生验证使用真实 CLI、自有 stdio 与无服务的本地供应商地址，无模型输入/结果。

| 层次 | 执行与结论 |
| --- | --- |
| 原生 Session/SDK | `python3 tests/claude_skill_change_native.py`：32 项通过。自然 Skill 禁用；外部新增、启停、v1→v2；两类恢复继承；同操作重复确认不换查询，不同目标拒绝；冷改配、原 ID 重开、候选冲突失败和 sibling 保持。最终目录见同前缀 `native-cold-final.log`。历史摘要为空模型历史，不算真实方法历史验收。 |
| Application/SQLite/Runtime/CLI | `NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags=nativeclaude ./internal/app -run '^TestClaudeNativeApplicationSkillChanges$' -count=1 -v`：37 项通过，原目录 `/tmp/netcap-w0207-claude-app-1264797877`。五次改配保存/准备/应用、精确候选晋升、原 ID/暂停/版本历史、资源库升级不静默生效、B 保持 v1/禁用。 |
| 状态与失败恢复 | Application 两项新测试通过，覆盖保存不生效、丢应用回执后一次核对成功、重启及混合 MCP/Skill 绑定保持。Runtime 六场景验证已释放包的名称、别名、描述重新出现或清单缺失、事务未提交时不能授予输入。 |
| Node 与既有回归 | Skill 改配 6 项、原选择 6 项、资源包 18 项通过；MCP 配置 7 项、认证恢复 10 项通过。原生资源包回归 18 项通过。 |
| 浏览器 | `tests/claude_skill_change_browser.js`：升级 16 项、继承 15 项、新增 14 项通过，1440/390 截图。预览/保存 hash、原快照/候选 ID、准备暂停、应用版本/原 ID、历史和重新添加均核对。使用上述真实 Application 各阶段导出，匹配浏览器请求后重放；HTTP/EventSource 为受控传输，浏览器没有驱动新的原生操作。 |
| 工程检查 | app/console 全包 race、Runtime Claude/Execution/NativeObservation/Skill 筛选 race、带 nativeclaude 标签的相关 vet、bridge tsc、`make dev-build` 通过。Go 均使用 `GOCACHE=/tmp/netcap-w0206-auth-go-cache`。 |

首次浏览器驱动失败来自工具环境缺少全局 URL，以及回放没有发送 change 事件；补齐驱动后重跑，不记作产品缺陷。最终脚本和请求记录保留。最终构建、源码、差异及证据哈希见 `W02-07-skill-change-manifest.json`；旧 `bin/netcap` 与此前 909 个证据文件保持不变。自有执行句柄均已退出，浏览器已关闭。

## 正文读取探针与剩余条件

新增 `tests/claude_skill_method_probe.py/.mjs`，完整模式准备通过本地脚本供应商请求原生 Skill，再按 CLI 返回的 Skill 基目录请求相对资料；从实际请求中检查正文与资料标记，恢复带真实 turn 的原 ID，并验证禁用不抹除历史。它始终将 `method_gate_verified` 标为 false，不能把脚本供应商行为当作真实模型的方法采用。

本轮只执行 `python3 tests/claude_skill_method_probe.py --planner-only`：5 项规划逻辑检查通过，Python AST/Node 语法通过，`protocol_verified: false`。完整模式未执行：当前代理没有本地监听能力。已请求用户在独立 WSL 终端运行 `python3 tests/claude_skill_method_probe.py` 并提供 Evidence directory；没有重做已知受限的环境探测。完整 Codex 选择探针仍只有 metadata_only 结果。

下一片先核对完整正文/资料读取与真实历史改配结果，再推进实际方法任务和 Gate。受管/其他 CLI 插件来源排除、正在运行 MCP 的混合改配以及完整四配置联合验收仍待补；本片的状态夹具不能替代这些原生证据。W02.07 与 Goal 保持进行中。
