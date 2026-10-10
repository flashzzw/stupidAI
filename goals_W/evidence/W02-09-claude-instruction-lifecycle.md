# W02.09 · Claude 指令来源的新鲜度、嵌套加载与原 ID 恢复

2026-10-10。固定原生 CLI 的 `memoryFiles` 汇总会遗漏工具触发加载的嵌套指令，也不能枚举恢复历史里的全部旧指令内容。**11 个场景、559 项原生检查通过**；另对实际请求完成 **163 项复核**。共 34 次本地模拟模型请求，只有嵌套场景执行一次自有文件的 Read；没有真实供应商调用。

本片新增可复跑的 SDK/CLI 探针，并在现有来源面板说明已证实的嵌套和历史覆盖限制。没有扩张配置冻结、排除或权限能力，Go 与生产 bridge 行为不变。整体仍 9/40 done。

## 已确认的原生边界

| 场景 | 检查数 | 实际请求、原生汇总和事件的关系 |
|---|---:|---|
| 父目录指令改写 | 51 | 热查询继续发送 V1；原 ID 重开后同时发送 V1 历史和 V2，新旧汇总路径/类别/token 估算完全相同 |
| 用户指令改写 | 51 | 同上，用户来源分别核对 |
| 导入文件改写 | 51 | 同上，导入事件保留 `include` 与 `parent_file_path` |
| 受管指令改写 | 51 | 同上，仅覆盖自有 `/etc/claude-code/CLAUDE.md` 挂载，不是企业策略认证验收 |
| 删除导入文件 | 49 | 热汇总仍列出已删除文件；冷汇总不再列出，但实际请求仍含历史 V1 |
| 新增父目录规则 | 50 | 热汇总与热输入均未加入新规则；原 ID 重开后才在汇总和请求中出现 |
| Read 触发嵌套指令 | 51 | Read 前请求没有嵌套内容，Read 后请求有；出现带 `trigger_file_path` / `prompt_id` 的 `nested_traversal` 通知，但热/冷汇总均未列出该文件，冷请求仍保留其历史内容 |
| 初始化后、首次输入前改写 | 52 | 首次输入已面对 V2 磁盘文件，却仍发送初始化时缓存的 V1；以后原 ID 重开才加入 V2 |
| 加载回调内改写 | 53 | 在父目录 `InstructionsLoaded` 回调内改成 V2，初始化返回时磁盘已经是 V2，首次请求仍为 V1；事后读取磁盘摘要不能证明已加载字节 |
| `disableAllHooks` | 53 | 原生 hook 清单明确报告 `all_disabled: true`，但 14 次指令加载通知仍可收到；不能把其他 hook 的禁用语义直接套用到这些通知 |
| 加载回调返回 `block` | 47 | 通过 SDK 回调返回阻止结果后，父目录指令仍进入初始、热和冷请求；此返回值不是已验证的指令排除接口 |

每个场景都有 3 个成功轮次并保留同一个原生 ID；冷请求包含初始、热查询和冷查询的原始用户轮次。初始七类自有路径与 User / Project / Local / Managed 标签逐项核对。实际请求中的 `Contents of …` 路径也单独检查，包括没有括号来源标签的嵌套格式；只允许本夹具和自有受管挂载路径。

这些结果描述固定版本在本轮“改写 → 汇总 → 输入 → 关闭/重开”序列中的行为，不推断新旧指令在模型中的优先级或实际遵循程度；长时间静置、自动压缩和全部文件监听时间窗未覆盖。嵌套 Read 由本地脚本供应商明确请求，不是自主方法使用或 Gate 验收。

## 材料与验证层次

各场景的原始请求、SDK 事件、加载通知、各阶段汇总、磁盘摘要、原生历史和结果保存在 [final 目录](W02-09-claude-instruction-lifecycle/final/)。[场景索引与计数](W02-09-claude-instruction-lifecycle/summary.json)对应上述 559 项；首次探索记录和 hook 策略独立核对另外保留，不计入最终原生检查数。

最终原生运行后，补加了实际请求来源路径的解析和 `--audit` 入口；163 项复核直接重读 34 份供应商请求，包含此前的消费契约及每例两项路径检查，没有再次运行模型轮次。它与原生检查有重叠，不将两者相加宣称更多独立场景。

最初探索没有假定汇总能覆盖所有已加载文件。实际出现嵌套遗漏、新旧内容共存和 hooks 禁用下仍有通知后，才固定相应消费契约。`hooks-disabled` 另通过生产只读 `NativeHookChannel` 核对 `all_disabled`；没有仅凭测试配置的期望值推断原生策略已生效。

界面使用上一片的真实 Application/Handler 双会话 DTO 与本片重新构建的资产回放，**29/29 项通过**；新增检查确认嵌套/历史限制可见，其余失败、切换、断连、未知与空清单边界保持。见[浏览器结果](W02-09-claude-instruction-lifecycle/validation/browser-result.json.gz)、[390px 截图](W02-09-claude-instruction-lifecycle/validation/native-390.png)。截图已查看，无横向溢出；这不是本片重新运行 Application 原生链路的证据。

Python/JavaScript 语法、最终 diff、Web 类型检查/构建和 `.dev/bin/netcap` 构建通过。生产 Go、bridge 与依赖未改，没有重复上一片已通过的 Go 全包 race/vet。Web 先构建，再构建内嵌其产物的 Go 程序；旧 `bin/netcap` 和旧原始证据保留。

## 版本、隔离与复跑

Claude Code **2.1.280**、Agent SDK **0.3.280**、Node **24.13.0**。参考[官方 TypeScript 接口](https://code.claude.com/docs/en/agent-sdk/typescript)、[SDK hooks](https://code.claude.com/docs/en/agent-sdk/hooks)及固定 `sdk.d.ts` 中的 `InstructionsLoadedHookInput` / `memoryFiles`。官方资料用于核对事件输入与设置来源；上述缓存、遗漏、竞态和 `block` 结论来自本轮原生实测，不冒称官方保证。

探针自建 `/var/tmp` 私有目录、隔离 user/PID/network namespace、自有 `/etc/claude-code`、HOME/profile/项目和本地文本供应商。没有启用子代理，没有宿主配置写入或外部消息；所有自有查询关闭后退出隔离 namespace。受管文件的 `Managed` 标签只是本地挂载实测，不覆盖真实 Windows 注册表、账号策略或远端真实性。

```bash
npm --prefix bridges/claude run build
python3 tests/claude_instruction_lifecycle_probe.py --case rewrite-parent
python3 tests/claude_instruction_lifecycle_probe.py --case nested-read
python3 tests/claude_instruction_lifecycle_probe.py --case hook-race
python3 tests/claude_instruction_lifecycle_probe.py --case hook-block
# 使用上一步输出的自有目录，不启动任何查询：
python3 tests/claude_instruction_lifecycle_probe.py --audit /var/tmp/netcap-instruction-lifecycle-EXAMPLE
```

其余 `--case` 名称见测试入口的 `MODES`。新增请求解析后重新运行原生探针会把每例两项路径检查一并计入，但本报告保留实际已执行的 559 / 163 分层计数。产物与源文件由 [manifest](W02-09-claude-instruction-lifecycle/manifest.json)和[归档索引](W02-09-claude-instruction-lifecycle/archive-index.json)绑定；原始材料按[约定](README.md)留在本地，只有顶层 Markdown 进入 Git。

W02.07/W02.08/W02.09 仍 `in_progress`；**9 done / 8 in_progress / 23 pending，Goal active**。下一片可用原生加载事件补充嵌套/导入来源及查询归属，仍须区分汇总、已观察通知、历史内容和未验证加载版本。完整来源新鲜度、动态权限、外部竞态、四配置、真实方法/Gate、六入口/输入权、Windows 与全部首版硬要求继续保留。
