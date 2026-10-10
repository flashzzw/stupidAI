# W02.08：原配置恢复的真实进程中断窗口

2026-10-06，Codex app-server 0.158.0 / proxy 0.157.1。本片验证前两片的原配置恢复和持久确认，新增实际 SIGKILL 场景，不修改生产代码、界面或构建。

## 实际路径

隔离子进程承载生产 Application/SQLite/Runtime，通过既有 stdio 夹具连接固定版本后台。沿已验证的原生目录 Skill 与 Skill/MCP/同步 hook 混合插件流程形成原历史和改配回执丢失，然后明确恢复原配置。供应商指向不可用 loopback 地址，原有六轮历史来自受控失败轮次；没有成功模型、工具或 hook 调用。

测试在实际 `Persistence.Save` 的指定边界停住，保存原状态库内容、将要提交的状态、原操作及实时原生协议检查点。父进程核对后台 PID 和 birth，再用子进程句柄发送 SIGKILL；没有调用 Application.Close 或用返回错误模拟崩溃。原 stdio 后台因管道关闭正常退出，由自有 subreaper 回收。恢复时重开原 SQLite 和原 profile，用新的隔离后台读取原线程。

| 中断位置 | 重开后的实际结果 | 检查 |
|---|---|---|
| 恢复意图 prepared 已提交，尚未派发 | 原操作标 not_sent，释放恢复标记；新明确请求使用独立操作，旧命令不重放 | [23/23](W02-08-codex-recovery-crash/prepared/summary.json) |
| 原生完整确认已取得，回执尚未提交 | 保持 uncertain、原候选和暂停；拒绝新恢复请求，不推断成功 | [21/21](W02-08-codex-recovery-crash/before_ack/summary.json) |
| 回执已提交，Save 尚未返回 | 只读核对已保存确认，归档一次失败候选；当前线程仍未加载 | [21/21](W02-08-codex-recovery-crash/after_ack/summary.json) |
| 恢复最终核对已成功，最终归档尚未提交 | 从已保存确认只读完成归档，保持原 ID/快照及暂停 | [21/21](W02-08-codex-recovery-crash/before_finish/summary.json) |
| 成功及归档已提交，Save 尚未返回 | 保留已提交事实和唯一归档，不重新恢复或加载 | [21/21](W02-08-codex-recovery-crash/after_finish/summary.json) |

五例共107项，全部通过。每例核对原 Scope、执行快照、生效历史、六个原轮次及工作区产物。重开后的被动读取与同命令重试没有配置重放。未确认状态不能输入；已确认状态仍显示当前能力未核对，只有单独明确 resume 才加载并核对原 Skill、插件和权限，历史仍为六轮。崩溃后的协议记录没有新建线程、fork 或模型输入。

另有[既有持久确认回归108项](W02-08-codex-recovery-crash/regression/summary.json)及带标签 vet 通过。原生回归验证新故障入口未改变原有路径。因没有生产或界面改动，本片不重复构建、浏览器或无关全包检查；既有构建摘要保持不变。

## 证据与复核

[四个窗口日志](W02-08-codex-recovery-crash/native-final.log)记录四个窗口，[确认提交前日志](W02-08-codex-recovery-crash/before-ack-first.log)记录另外一个窗口。完整检查点、协议与恢复快照保存在各目录的 `result.json.gz`；例如[确认提交前原始结果](W02-08-codex-recovery-crash/before_ack/result.json.gz)和[最终提交后原始结果](W02-08-codex-recovery-crash/after_finish/result.json.gz)。被父进程保存的原生响应不能冒充工作台已经提交的回执。

[独立审计147项](W02-08-codex-recovery-crash/audit.json)从原始检查点、SQLite 导出、协议和保留文件核对结果，包含提交边界、原意图摘要、原权限/能力、候选归档、进程终态和零新输入。[审计脚本](W02-08-codex-recovery-crash/audit.py)可直接对归档重跑。首次审计误用 `id` 读取 Command，正确 JSON 字段为 `command_id`；仅修正审计器并保留[记录](W02-08-codex-recovery-crash/audit-first.log)，未修改生产或原生证据。

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags=nativecodex ./internal/runtime \
  -run '^TestCodexNativeOriginalRecoveryHardCrash$' -count=1 -v

python3 goals_W/evidence/W02-08-codex-recovery-crash/audit.py
```

[来源目录](W02-08-codex-recovery-crash/original-directories.json)、[source diff](W02-08-codex-recovery-crash/source.diff)、[命令终态](W02-08-codex-recovery-crash/command-results.json)、[清理](W02-08-codex-recovery-crash/cleanup.json)和 [manifest](W02-08-codex-recovery-crash/manifest.json)记录本片测试与产物。4418个旧证据文件、全部生产代码、bridge/Web构建及两个二进制均未改变。自有应用进程确被SIGKILL，原生后台与恢复后台均已退出；未提交或推送。

## 仍未完成

本片是实际 Application 库进程/SQLite/stdio 故障验收，不等于生产 `serve` 的 HTTP/Unix proxy/peer 链路、存活共享后台、多客户端输入独占、断电或在途工具恢复。完整确认保存前中断仍没有可持久验证的成功依据，保留未知，不以重试消除该缺口。

W02.07/W02.08 仍 in_progress，9/40 done，Goal active。下一片转向 CLI 插件实际组件作用与尚未接通的同步 hook 选配范围。真实模型/工具/Gate、完整受管/安装来源、共享后台/输入独占、Windows 和五项既有 WSL 请求保持未完成；复用已有固定版本资料，不重复已知不可达的外部调研。
