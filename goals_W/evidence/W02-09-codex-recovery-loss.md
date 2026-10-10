# W02.09 · 冷恢复自身再次丢回执

2026-10-06。延续 [冷插件策略原配置恢复](W02-09-codex-cold-recovery.md)，验证恢复操作自身再次丢失原生确认后的持久状态。仅扩展测试与证据；生产 Go、Web、bridge 和两个二进制不变。固定 app-server 0.158.0、CLI 0.157.1；W02.07/08/09 仍为 `in_progress`，9/40 done，Goal active。

## 双重故障边界

每例先在冷混合改配的 `thread/settings/update` 已完成后丢弃真实回执。原生禁用列表已持久化，工作台保留未知的改配操作和原快照。重开、还原原 hook 文件并通过只读核对后，用户式明确恢复启动独立操作；该恢复再次在下列位置丢弃真实回执：

| 恢复丢回执位置 | 原生实际结果 | 工作台结果 |
|---|---|---|
| 中间加载 | 按原 ID 临时排除本次插件，禁用列表仍包含它们 | 恢复 unknown，没有确认记录 |
| 清除本次插件禁用项 | 仅保留其他原生禁用来源，中间配置仍排除本次插件 | 同上 |
| 退订 | 原生策略已恢复且本连接已退订 | 同上 |
| 最终原配置加载 | 原配置已真实加载，回执在交给 Runtime 前丢失 | 仍 unknown；不从当前文件或缺失确认推造成功 |

故障夹具先取得真实原生结果，再返回 EOF。初次改配和恢复分别保留独立 Operation ID，两者均为 `uncertain`。旧 Scope、执行快照、配置历史及待核对候选保留；恢复没有成功归档，也没有 `ConfigurationRecoveryAcknowledgement`。相同命令幂等；重启后仍保持暂停、配置 hold 和 `recovering` 待核对状态。

原文件已经还原，也不能凭文件匹配或清空的禁用列表重放操作。重启后的 send、resume、prepare、apply、discard、另一个 recover 命令均明确拒绝；原失败操作的恢复检查入口也不重新开放。原生记录确认重启和这些拒绝操作只读，没有新的配置加载、策略写入、退订或模型输入。

## 验证结果

| 层次 | 实际结果 |
|---|---|
| 恢复自身四个真实丢回执位置 | 每例 59 项，共 236 项通过 |
| 正常冷策略原配置恢复回归 | 46 项通过；确保没有将正常恢复一并禁用 |
| tagged internal/cmd vet | 通过 |
| 浏览器 | 31 项通过；实际最终恢复回执丢失前/后重启的 Handler 状态，只读回放 |
| 独立归档核对 | 167 项通过；两次真实回执、原生步骤、策略持久化、操作 ID/状态、缺失确认、原快照/候选与原始摘要 |

[窄屏界面](W02-09-codex-recovery-loss/unknown-390.png)明确显示恢复结果待核对，保留候选和两个操作 ID。没有成功归档、确认成功提示、重复恢复或再次应用入口；输入、恢复自动化和撤销均禁用。页面加载和刷新只发 GET，没有自动检查或命令。

没有修改生产路径；现有确认与持久状态契约直接通过。测试使用自有配置、状态、插件和 stdio 后台，EOF 后该后台退出；不代表存活共享后台或硬崩溃。故障前的三轮输入指向不可用 `127.0.0.1:9` 并显式中止，hook 写入 marker 后故意超时；第一次改配故障之后没有模型输入。**不把这些结果视为成功 hook、真实模型任务、方法或 Gate 验收。**

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags nativecodex ./internal/runtime -run '^TestCodexNativeColdRecoveryReplyLoss$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/... ./cmd/...
```

相同环境变量下的正常恢复回归为 `^TestCodexNativeColdPolicyRecovery$/^policy$`。浏览器入口为 [codex_recovery_reply_loss_browser.js](../../tests/codex_recovery_reply_loss_browser.js)，使用归档最终阶段样本的 `browser-responses.json.gz` 与当前 Web 资源。

[目录映射](W02-09-codex-recovery-loss/original-directories.json)、[命令结果](W02-09-codex-recovery-loss/command-results.json)、[独立核对](W02-09-codex-recovery-loss/independent-audit.json)、[清理记录](W02-09-codex-recovery-loss/cleanup.json)、[源码与产物清单](W02-09-codex-recovery-loss/manifest.json)记录本轮实际结果。5432 个旧证据文件及生产代码、Web、bridge、两个二进制保持不变；没有重建或重复无修改的 App/Console 单元集，没有提交或推送。

## 剩余条件

本片证明未知状态不会被抹掉或重复执行，**没有把第二次丢回执的恢复标为成功**。缺失原生确认、恢复与应用提交窗口的硬崩溃、存活共享后台、断电和跨客户端输入独占仍是独立条件。后续回到 W02.09 的有效来源汇总、漂移范围和四配置联合，复用已验收的恢复契约；完整受管/引用脚本、成功/通用/异步 hook、真实模型方法/Gate、安装来源与 Windows 不因本片通过而收口。五项既有 WSL 请求仍待结果。
