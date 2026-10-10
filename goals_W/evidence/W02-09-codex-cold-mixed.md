# W02.09 · 混合能力冷候选与原生插件策略

2026-10-06。延续 [hook 文件与冷候选](W02-09-codex-hook-mixed.md)，修复原线程未加载时，混合能力撤去因 `thread/settings/update: thread not found` 失败的问题。固定 app-server 0.158.0、CLI 0.157.1；复用生产 Application、SQLite、Runtime 和 Handler。W02.07/08/09 仍为 `in_progress`，9/40 done，Goal active。

## 行为与原生边界

包含 CLI 插件（MCP、Skill、hook）和独立 Skill 的执行，在保存候选后或准备完成后关闭应用及自有原生进程，重开仍保留原身份、原配置快照、候选、历史、配置证明与暂停状态。准备和读取不加载线程；明确应用才能切换。两个时点均验证先启用混合能力，再从混合原配置撤去能力并采纳文件版本。

固定版本的 [ThreadResumeParams](W02-08-codex-plugin-policy/schema/ThreadResumeParams.ts) 不提供 `disabledPluginIds`，而未加载线程不能接受 `thread/settings/update`。因此，当明确应用需要改变禁用列表时，生产采用已由隔离原生对照验证的顺序：

1. 再核对原历史、候选文件、配置证明与未加载状态。
2. 按原 ID 加载中间配置，将本次管理的原/新插件全部排除；核对身份、权限、原禁用列表和完整 MCP 清单。
3. 更新该线程的禁用策略，保留不属于本次管理范围的原生禁用来源。
4. 取得本连接退订回执，复核空闲、历史、策略及来源，再按原 ID 加载最终候选。
5. 只有最终原生回执和实际资源核对通过，才晋升快照。

中间配置不作为一次成功改配，也不产生新的工作台执行身份。它发生在明确应用事务内，未发送模型输入。任一配置派发后失败或丢回执均保留未知；旧快照和待核对候选不变，不把它分类为可直接重发的 `not_sent`。中间加载真实回执丢失的样本已验证单次派发、幂等、禁止输入和重启后不重放。

新故障测试还发现 MCP 清单解析将缺失或 `null` 的 `data` 当作完整空列表。现改为要求明确数组；`[]` 可证明空清单，缺失/`null` 不能证明插件已排除。原有分页、重复项与来源核对仍保留。

## 验证结果

| 层次 | 实际结果 |
|---|---|
| 原生协议对照 | 通过：中间排除 → 策略写入 → 退订 → 最终加载；历史不变，无模型输入 |
| 保存后冷重开、联合启用与撤去 | 29 项通过 |
| 准备后冷重开、联合启用与撤去 | 26 项通过 |
| 中间加载真实丢回执 | 37 项通过；未知、旧快照、候选、幂等、禁止输入及重启不重放 |
| 原用户/项目/新增目录/混合/worktree/冷候选/丢回执回归 | 89 项通过 |
| 已确认原配置恢复跨重启回归 | 108 项通过 |
| Runtime 故障边界 | 新增 11 类通过；已加载、历史变化、丢回执、身份/权限/策略异常、清单缺失或仍有被排除插件 |
| App/Console、相关 Runtime/MCP race | 通过 |
| tagged vet、Web 类型检查/构建、开发 Go 构建 | 通过 |
| 浏览器 | 15 + 15 + 14，共 44 项通过；真实 Handler 数据，命令/SSE 回放 |
| 独立归档核对 | 277 项通过；原生顺序、精确禁用 ID 集合、实际 marker、历史候选、丢回执和原始摘要 |

浏览器分别覆盖[保存后重开](W02-09-codex-cold-mixed/retire_cold_pending-390.png)、[准备后重开](W02-09-codex-cold-mixed/retire_cold_prepared-390.png)和[未知状态重开](W02-09-codex-cold-mixed/cold_loss_reopened-390.png)。未知状态没有再次准备/应用按钮，普通输入仍禁用；正常应用只提交保存的原快照/候选引用。没有修改 Web 生产代码。

所有测试插件、配置、状态和进程均隔离。hook 在写入自有 marker 后故意超时；模型端点为不可用的 `127.0.0.1:9`，每次输入显式中止。它们验证资源加载与调用归属，**不证明成功 hook、模型任务、方法效果或 Gate**。中间协议顺序不包含 `turn/start`，正常最终应用后的独立测试输入才验证退休资源不再调用。

## 失败记录与修正

- `native-first.log`：保存后和准备后重开均在撤去阶段复现未加载线程不能写插件策略。先用原生协议对照证明可行顺序，再接入生产事务。
- `policy-unit-first.log`：缺失 MCP 清单被接受；修复共享清单解析后，缺失和 `null` 两类均拒绝。
- `native-final.log`：负例暂时把文件还原成当前执行的旧版本，给既有自动恢复留下了加载窗口；原生 trace 确认发生原配置恢复，后续冷准备正确拒绝已加载线程。负例改为第三个文件版本，以单独验证“候选再次漂移”，不改变原配置恢复规则。最终 `native-complete.log` 通过。
- 原始失败、较早成功样本与最终样本均保留在[目录映射](W02-09-codex-cold-mixed/original-directories.json)；不以早期样本替代最终版本结果。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings|MCP)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/... ./cmd/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags nativecodex ./internal/runtime -run '^TestCodexNativeHookSourceAdoptionMixedCold(Pending|Prepared|Loss)$' -count=1 -v
```

独立原生协议入口为 `TestCodexNativeColdPluginPolicyBoundary`；原恢复回归入口为 `TestCodexNativeOriginalConfigurationRecoveryAcknowledgedRestart`。浏览器脚本为 [codex_cold_plugin_policy_browser.js](../../tests/codex_cold_plugin_policy_browser.js)，使用归档 `native-complete-*` 的 Handler 响应及当前 Web 静态资源；`__PHASE__` 分别为 `retire_cold_pending`、`retire_cold_prepared`、`cold_loss_reopened`。

[命令结果](W02-09-codex-cold-mixed/command-results.json)、[独立核对](W02-09-codex-cold-mixed/independent-audit.json)、[清理记录](W02-09-codex-cold-mixed/cleanup.json)、[源码与产物清单](W02-09-codex-cold-mixed/manifest.json)记录本轮实际版本。5108 个旧证据文件、Claude bridge、Web 源码及旧 `bin/netcap` 保持不变。没有提交、推送、改变用户配置或操作共享后台。

## 剩余范围

本片中间加载正例为混合能力撤去，丢回执覆盖第一次中间加载。更多混合候选组合、后续策略写入/退订/最终回执和硬崩溃窗口、失败中间配置的完整恢复矩阵仍需补齐。仅覆盖 UserPromptSubmit 超时 hook，加载期及其他事件、成功/通用/异步 hook 不由此片验证。全部受管/引用脚本/有效配置、真实模型方法/Gate、安装来源、共享后台/proxy、跨客户端输入独占、四配置完整联合和 Windows 硬要求保留；五项既有 WSL 请求仍待结果。下一片优先补中间策略变更后的原配置恢复及剩余原生故障窗口。
