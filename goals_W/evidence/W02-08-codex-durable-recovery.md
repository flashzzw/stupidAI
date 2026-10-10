# W02.08：Codex 已确认恢复的跨重启核对

2026-10-06，Codex app-server 0.158.0 / proxy 0.157.1。修复原配置恢复已确认并写入工作台回执、但最终核对前重启后仍卡在未知的问题。新增绑定原操作的完整恢复确认，沿已有 Operation 回执持久化，不新增状态库或修改原生 CLI。

## 实现与边界

Runtime 收到恢复响应后，完整核对原 ID、权限、能力选择、原历史与配置来源稳定性，才生成 `configuration_recovery` 回执字段。字段包含版本、完整意图摘要、配置/约束指纹和核对时间；原生 ID 仍来自原回执。仅收到 `thread/resume` 的响应不够，未取得完整确认的旧回执不补造历史。

重启丢失连接内存后，仅当上述回执已持久化，且原线程/完整加载清单均确认未加载、原历史未变、没有未决工作、原来源及约束仍匹配时，才能只读确认历史恢复结果。回执绑定错误、缺失或损坏、来源/历史漂移、线程仍加载和读取竞态保持未知。原 Scope、快照及生效历史保持，完整失败候选归档，自动化仍暂停。

历史恢复成功与当前能力加载分开：跨重启确认不恢复连接内的 Skill/hook 建立依据，当前线程仍未加载、能力显示待核对、发送保持不可用。暂停期间的后台轮询不会为补齐依据自动加载；用户明确点击“恢复自动化”后才按原 ID 加载原配置、核对权限及能力。界面提供对应说明。

原生恢复响应在到达 Runtime 前丢失，或者完整确认尚未写入工作台回执就丢失，仍不能走本路径。存活共享后台、其他客户端改配、全局输入独占与硬崩溃窗口没有由本片证明完成。该路径没有重复派发恢复，也不把当前配置相似当作原操作成功。

## 实际验证

真实 Application/SQLite/Runtime、隔离 stdio 后台、原生目录 Skill 和含 Skill/MCP/同步 hook 的本地插件。项目、配置、缓存及状态目录独立，供应商指向不可用 loopback `127.0.0.1:9`。没有成功模型/工具调用；后续正文和 hook 载荷沿已有受控失败轮次核对，不代表方法/Gate 或 hook 成功。

| 检查 | 结果与证据 |
|---|---|
| 已确认回执持久化 → 工作台/自有后台重启 → 只读确认 → 明确 resume → 后续轮次 | 最终 108/108；[摘要](W02-08-codex-durable-recovery/durable/summary.json)、[完整原生结果](W02-08-codex-durable-recovery/durable/result.json.gz) |
| 既有正常恢复、应用层漏回执、原生层漏回执 | 103/97/96，共296；[正常](W02-08-codex-durable-recovery/normal/summary.json)、[应用层](W02-08-codex-durable-recovery/application-loss/summary.json)、[原生层](W02-08-codex-durable-recovery/native-loss/summary.json) |
| Runtime 回执/来源/历史/占用边界 | 新增15类，连同既有8类共23通过；[最终日志](W02-08-codex-durable-recovery/boundaries-final.log) |
| 浏览器新场景/既有恢复回归 | 20/26；[新场景](W02-08-codex-durable-recovery/browser-final.json)、[回归](W02-08-codex-durable-recovery/browser-regression.json) |
| 独立原始数据核对 | 137/137；[审计](W02-08-codex-durable-recovery/audit.json)、[脚本](W02-08-codex-durable-recovery/audit.py) |
| App/console 全包 race、Codex/Claude Runtime 非原生回归、带标签 vet、Web 类型检查/开发构建 | 通过；[App/console](W02-08-codex-durable-recovery/app-console.log)、[Runtime](W02-08-codex-durable-recovery/runtime.log)、[构建](W02-08-codex-durable-recovery/build-final.log)、[命令终态](W02-08-codex-durable-recovery/command-results.json) |

原始快速测试和真实流程均复现“已保存回执但重启仍未知”，保留[单元红灯](W02-08-codex-durable-recovery/unit-red.log)、[原生红灯](W02-08-codex-durable-recovery/native-red.log)和[原始数据](W02-08-codex-durable-recovery/red/result.json.gz)。读取末尾发生配置漂移的新增反例也先失败，补最后一次指纹核对后通过；[失败记录](W02-08-codex-durable-recovery/late-drift-red.log)保留。旧错操作反例原先要求不报错，新实现明确拒绝持久回执绑定错误，测试据此核对失败结果并保留中间日志。

独立审计从 SQLite 导出、原生协议、历史快照和 hook 载荷重算意图摘要并核对关联。恢复确认前后历史6→6，明确 resume仍为6，后续单独输入才变为7；被截获的原生响应不冒充 Runtime 收到的证据。原生丢回执例仍 uncertain，重启没有新配置请求。

浏览器回放真实持久确认、未加载核对和明确 resume 完成时的快照；HTTP/SSE 与命令回执受控，没有从浏览器发起原生执行。非空草稿在待核对时不能发送，明确 resume 的原生核对完成后才可发送；测试不发送草稿。[桌面](W02-08-codex-durable-recovery/unloaded-1440.png)和[手机](W02-08-codex-durable-recovery/unloaded-390.png)截图保留。夹具中的 URL 全局、文案、快照时间点与空草稿假设曾导致失败，已按实际界面和原生状态修正，见[过程说明](W02-08-codex-durable-recovery/browser-first.log)，未用合成成功状态替代原生样本。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags=nativecodex ./internal/runtime \
  -run '^TestCodexNativeOriginalConfigurationRecovery' -count=1 -v

python3 goals_W/evidence/W02-08-codex-durable-recovery/audit.py
```

开发 `.dev/bin/netcap` SHA-256：`aea69423c9b72d27dc7ac978c45b889c78da4cdd6833b37a13f5b42c4cb09ba4`。[source diff](W02-08-codex-durable-recovery/source.diff)、[manifest](W02-08-codex-durable-recovery/manifest.json)、[来源目录](W02-08-codex-durable-recovery/original-directories.json)与[清理核对](W02-08-codex-durable-recovery/cleanup.json)记录归档时点。4337 个旧证据文件、Claude bridge 和旧 `bin/netcap` 未改动；自有进程均退出、浏览器关闭、临时执行脚本归档移出工作区，未提交或推送。

沿用前片已归档的固定版本协议依据，本轮未重复已知不可达的官方资料请求。W02.07/W02.08 仍 in_progress，9/40 done，Goal active。下一片继续确认保存前的故障窗口与可持久核对边界，再推进尚未覆盖的实际组件作用；真实模型/工具/Gate、完整受管/安装来源、共享后台/Unix proxy、输入独占、Windows 和五项既有 WSL 请求保持未完成。
