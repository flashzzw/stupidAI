# W02.08 · Claude 启动前来源预解析的缺失证据

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 结论

固定 SDK 的 `resolveSettings()` 可以在不启动 Claude 查询或插件的情况下返回用户/项目/受管设置来源，但**返回成功不等于全部来源读取成功，也不等于可以据此放行已安装插件**。

实际 SDK + 受控注册表回复确认：HKLM/HKCU 读取被拒绝或进程不可用时，SDK 仍返回用户/项目设置，根结果没有 `complete` 或 `errors` 字段。损坏的项目 JSON 被省略，其他来源仍返回。`policyHelper` 可以出现在受管来源中，但这个接口不执行它，故其输出不能代表 helper 最终追加的限制。

本代理的真实 host 样本中，两个注册表查询均报告不可判定、错误码 1；SDK 仍正常返回且没有受管来源。这些数据不足以区分实际没有策略和未读取到策略。没有进一步重试宿主，也没有据此放宽生产插件准入。已向用户发出一次可用 WSL 终端的同脚本只读对照请求，尚待结果。

## 执行与证据

新增 [Python 入口](../../tests/claude_settings_resolution_probe.py)和[SDK 观察器](../../tests/claude_settings_resolution_probe.mjs)。每个案例使用独立 HOME/profile/cwd，关闭非必要流量；只声明测试插件键，没有安装、启动或调用插件。导出设置**键名**、来源类别/路径、测试键值、读取状态和错误码，不导出实际受管设置正文。脚本仅诊断，不是已接通的生产 preflight。

| 案例 | 实际结果 |
|---|---|
| 注册表键缺席控制 | 模拟两个 hive 的明确 key-not-found 回复；返回用户/项目两个来源。 |
| Access denied 控制 | 模拟两个拒绝回复；SDK 仍返回相同测试设置和来源，缺少错误/完整性字段；观察器保留 unknown。 |
| EACCES 控制 | 模拟两个进程不可用回复；SDK 同样返回普通来源；观察器保留 unknown。 |
| policyHelper 控制 | 通过 SDK 的 `serverManagedSettings` 注入自有 helper 声明；报告 remote 受管来源，但 helper 未执行、标记文件不存在。此处不是实际企业策略。 |
| 损坏项目 JSON | 真实临时文件无法解析；SDK 省略项目来源且不在根结果报告错误。诊断脚本独立读取声明文件，保留无效来源和待核对状态。 |
| 真实 host | SDK 实际尝试只读 `reg.exe`，HKLM/HKCU 均为 unavailable、错误码 1；正常返回用户/项目来源，不能判为受管来源完整。 |

最终受控五案例 [10 项检查](W02-08-claude-settings-provenance/controlled-final/result.json)通过。这里只验证缺失证据的行为，没有模拟结果冒充真实 Windows 策略读取。[独立审计 12 项](W02-08-claude-settings-provenance/audit.json)确认上述正反例，明确 `host_provenance_reads_available=false`、`production_preflight_verified=false`。

[真实 host 原始结果](W02-08-claude-settings-provenance/host/result.json)来自早期诊断汇总：执行器退出 0、其 `passed=true` 当时只表示诊断进程结束，不能用它声称来源可读。新增汇总现要求两个 hive 都有明确 present/absent 结果，未知读取会令 `--host` 退出 1；没有仅为更新汇总再执行宿主查询。旧结果原样保留，审计按真实读取状态判定。

初步 `resolve-probe.mjs` 只记录到两次 reg.exe 调用，未捕获回执，另存[初步结果](W02-08-claude-settings-provenance/resolve-result.json)；随后 host 样本补齐了读取结果。最终 host 原始目录 `/tmp/netcap-w0208-settings-resolution-ljl6csa1`；受控样本路径见[目录清单](W02-08-claude-settings-provenance/original-directories.json)。初版受控诊断没有独立捕获损坏 JSON 的待核对状态，补齐后的最终 10 项通过；[初版结果](W02-08-claude-settings-provenance/controlled-first/result.json)保留。

全部诊断执行器已结束。受控案例没有调用实际 Windows 程序；真实 host 只读取注册表，没有写 Windows 配置。没有 Claude 查询、模型请求、MCP、hook、listener、共享后台或用户 profile 修改。

## 契约、复验与隔离

SDK 固定 **0.3.280**。[类型声明](W02-08-claude-settings-provenance/sdk-contracts.txt)将结果描述为 “raw settings cascade, not a security decision”，并明确 `policyHelper` 不执行、WSL/Windows 可能调用 reg.exe、权限 defaultMode 还需独立信任过滤。本片实际核对了对应缺口，没有声称重新取得在线官方文档；沿用前片 Context7 不可达的记录。

```bash
# 五个受控案例，不调用 Windows；最终 10 项通过。
python3 tests/claude_settings_resolution_probe.py
# 可用 WSL 终端的只读对照；当前尚待用户返回该版本的结果。
python3 tests/claude_settings_resolution_probe.py --host
node --check tests/claude_settings_resolution_probe.mjs
python3 goals_W/evidence/W02-08-claude-settings-provenance/audit.py
```

缺席分类目前只识别明确的英文 registry-key-not-found 文本；其他语言或未知错误保留 unavailable，不会仅按退出码 1 当作缺席。简单 JSON 读取检查只覆盖本次声明的用户、项目和项目本地文件，不等于完整 schema 校验、原子文件快照或所有受管来源扫描。即使 `--host` 的读取检查通过，也不会将 `production_preflight_verified` 提升为 true。

[差异](W02-08-claude-settings-provenance/source.diff)、[manifest](W02-08-claude-settings-provenance/manifest.json)及[完整性核对](W02-08-claude-settings-provenance/integrity.json)记录本片时点。Python AST、Node syntax 和受控案例通过；生产代码、Web、依赖及两个二进制未变，没有重复 Go/浏览器/构建。3305 个旧 W 证据未改；开发二进制仍为 `7dadabec610c43296d7c0f0cf0bfcf800871bbb5403a718c64afe8b4ead4471c`，旧 bin 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。没有提交或发布。

## 后续

安装来源的生产接入仍需失败可观察的来源预检：区分不存在/不可读/无效内容，保留未执行 helper 和其他未覆盖来源；再关联完整安装键、适用 scope、市场声明、实际路径和资源版本。不能只凭 resolveSettings 的成功返回或空受管清单放行。上一片派生 settings 的已测启动行为仍成立，但不代替这些来源与权限条件。

既有四个 WSL 工具/方法探针与本次受管来源读取对照均待结果。完整方法/Gate、来源/策略与权限、原生丢确认恢复、输入独占、Windows/proxy/peer 和其他 hook 条件继续保留。本轮有新的失败行为和诊断证据；不将单个前置失败提升为整个 Goal 已阻塞或已完成。
