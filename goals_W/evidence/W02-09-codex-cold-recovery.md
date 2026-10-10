# W02.09 · 冷插件策略的四处丢回执与原配置恢复

2026-10-06。验证 [冷混合候选](W02-09-codex-cold-mixed.md)在中间加载、策略写入、退订、最终加载四个位置丢失真实原生回执后的恢复。复用现有生产恢复路径，本轮没有修改生产代码、Web 或两个二进制。固定 app-server 0.158.0、CLI 0.157.1；W02.07/08/09 仍为 `in_progress`，9/40 done，Goal active。

## 场景与结果

每例先建立同一原生线程的 CLI 插件（MCP、Skill、hook）、独立 Skill 和用户 hook 文件配置，再保存撤去两个资产并采纳文件版本的候选。冷重开、准备后，故障夹具先取得真实原生 RPC 结果，再向生产 Runtime 返回 EOF；它没有以“尚未发送”替代真正执行过的操作。Runtime 关闭自有 stdio 连接后，该测试后台退出。

| 丢回执位置 | 原生实际完成的动作 | 原配置恢复顺序 |
|---|---|---|
| 中间加载 | 本次插件已临时排除；禁用列表尚未改变 | 直接按原 ID 加载原配置 |
| 策略写入 | 禁用列表已写入本次两个插件 ID，并保留其他来源 | 临时排除 → 仅清除本次插件禁用 → 退订 → 加载原配置 |
| 退订 | 策略已写入且本连接已退订 | 同上 |
| 最终加载 | 候选已经真实加载，但 Runtime 没有取得其确认 | 同上；不把缺失确认的候选晋升为当前快照 |

所有位置均保留旧 Scope/快照、失败候选及 unknown，阻止普通输入，重复命令沿用原操作，重开不重放失败改配。恢复前的只读核对要求原线程未加载、历史与原基线一致、原配置来源匹配；旧文件尚未还原时不能恢复。测试明确还原自有原文件后，调用原有恢复入口。

恢复成功保留原身份、完整原快照和配置历史，归档且仅归档一次失败候选，自动化仍暂停。明确恢复自动化后的独立测试轮次重新调用原插件 hook 和原用户文件定义，精确 marker 为 `pack`、`v1`、`v2`。原生记录独立证实：策略写入后的禁用列表跨关闭连接保留，恢复只清除本次插件 ID，`retained@owned` 始终保留。

## 验证依据

| 层次 | 实际结果 |
|---|---|
| 四个真实原生丢回执位置及恢复 | 每例 46 项，共 184 项通过，含原配置实际调用 |
| 原有普通文件采纳丢回执回归 | 13 项通过 |
| 原有混合冷中间加载丢回执回归 | 37 项通过 |
| tagged internal/cmd vet | 通过 |
| 浏览器 | 21 项通过；真实最终加载丢回执的 Handler 快照/核对结果，HTTP 命令和 SSE 回放 |
| 独立归档核对 | 173 项通过；原生调用顺序、真实丢失回执、策略持久结果、恢复配置、历史、marker 与原始摘要 |

[准备恢复](W02-09-codex-cold-recovery/ready-390.png)与[恢复后归档](W02-09-codex-cold-recovery/recovered-390.png)展示显式核对/恢复、原快照保留、失败原因及候选归档、暂停状态。浏览器只发送一个 `recover_execution` 回放命令，不发送模型输入；原插件与独立 Skill 分别在正确栏目展示。初版浏览器断言误将私有包交付的 Skill 也要求显示在 CLI 插件栏目，已按真实资产类型修正，失败记录保留。

测试仅使用自有临时配置、状态、插件、进程与不可用的 `127.0.0.1:9` 供应商。hook 写入 marker 后故意超时，全部模型输入显式中止。因此它证明恢复和调用归属，**不证明成功 hook、真实模型任务、方法效果或 Gate**。既有生产路径直接通过；没有为取得通过结果改变来源、权限、占用或恢复准入。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags nativecodex ./internal/runtime -run '^TestCodexNativeColdPolicyRecovery$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/... ./cmd/...
```

相同环境变量下的兼容回归入口为 `^TestCodexNativeHookSourceAdoption$/^native-loss$` 与 `^TestCodexNativeHookSourceAdoptionMixedColdLoss$`。[浏览器脚本](../../tests/codex_cold_recovery_browser.js)使用 `native-first-405874354/browser-responses.json.gz` 与当前 Web 资源，执行环境为 Playwright 文件脚本。

完整记录见[目录映射](W02-09-codex-cold-recovery/original-directories.json)、[命令结果](W02-09-codex-cold-recovery/command-results.json)、[独立核对](W02-09-codex-cold-recovery/independent-audit.json)、[清理记录](W02-09-codex-cold-recovery/cleanup.json)、[源码与产物清单](W02-09-codex-cold-recovery/manifest.json)。本轮扩展测试用故障夹具与验收入口；未重跑无修改的生产构建和 App/Console 单元集。5342 个旧证据、生产 Go/Web/Claude bridge 及两个二进制保持不变，未提交或推送。

## 保留的硬要求

这些 EOF 发生在真实 RPC 完成之后，且关闭的是自有 stdio 后台；它们不等同于存活的共享后台、操作中断电、应用提交中 SIGKILL 或恢复自身再次丢回执。更多混合候选组合、四配置完整联合、加载期/成功/通用/异步 hook、完整受管/引用脚本/有效配置、真实模型方法/Gate、安装来源、共享后台/proxy、输入独占和 Windows 继续保留。五项既有 WSL 请求仍待结果。下一片继续恢复自身故障与持久状态边界，并按 W02.09 的来源/配置联合条件收口，不因这些局部样本通过而结束整包。
