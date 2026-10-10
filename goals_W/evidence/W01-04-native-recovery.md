# W01.04 / W01.05 · Claude 原生恢复与只读来源准入

2026-10-08。在监听/IPC 前置已恢复的 WSL 中，直接运行固定 Claude CLI 2.1.280、SDK 0.3.280、现有生产 Application/Runtime/Handler 与本地模拟供应商。全部使用自有 profile、项目、工具和凭据；未更改用户配置或共享后台。开发二进制 SHA256：`dd1f5367639be39e80452ccc7c37f9e10159e1ad58d5be0912dfae17f9d02ede`。持久工作目录为 `.dev/w0104-native-recovery-work`。

本片修复原生只读入口误用执行凭据准入的问题，并重新验证原生后台故障边界。supervisor 恢复正例成立；worker 崩溃后原工具输出仍缺失，而且会自动重发原提示。完整工具恢复未通过，不能将下列诊断检查的成功作为 W01.05 验收完成。

| 验证层次 | 实际结果 | 范围与限制 |
|---|---|---|
| A/B 原生 supervisor | 13 项通过 | 独立配置、凭据与完整 ID；attach/detach/reattach 不替换 worker，停 A 不影响 B；A 正常退出后恢复原 ID/历史 |
| supervisor SIGKILL | 12 项通过 | attach 断开及 supervisor 重建期间原 worker/真实工具继续；只执行一次，原结果回到模型 |
| worker SIGKILL 原探针 | 10 项诊断检查通过，输出连续性未通过 | 替换 worker 保持原 ID，但原工具仍活时已报 idle；原提示被自动重发，新请求没有原工具调用或结果 |
| worker 重试与生产守卫 | 19 项诊断检查通过，`goal_acceptance_met=false`、`tool_output_continuity=false` | 模拟供应商对重发提示再次请求同一 Bash；替换 worker 要求新审批，原工具完成后审批仍在、原结果仍缺失 |
| 只读准入回归 | 3 个顶层测试、15 个子场景通过 | 原生认证 profile/custom settings 可预览、绑定、读历史；执行仍拒绝；来源漂移、坏设置和 reader/版本前置继续拒绝 |
| 真实 Handler 与浏览器 | 13 项通过 | 原生 worker 已停止后发现、UUID 预览、历史、只读登记；无 composer/原生 operation/执行命令，执行配置仍禁用；1440/390 视口 |
| 独立证据审计 | 59 项通过 | 逐份核对原生 ID、供应商请求、工具记录、HTTP、SQLite、helper 计数、浏览器与基线；恢复结论仍为 false |

入口与原始结果：

```bash
python3 tests/workbench_boundary_probe.py --case claude-supervisors
python3 tests/native_claude_lifecycle_probe.py --case supervisor-crash
python3 tests/native_claude_lifecycle_probe.py --case worker-crash
python3 tests/claude_worker_retry_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run 'TestClaude.*(Settings|Roster|Configuration|Source)' -count=1
make dev-build
```

A/B、supervisor、原 worker 目录分别为 `/tmp/netcap-boundary-twdyopk9/claude-supervisors`、`/tmp/netcap-claude-native-nblbmo_k/supervisor-crash`、`/tmp/netcap-claude-native-j6pmzmhx/worker-crash`。重试探针修复前、初版修复后、最终目录分别为 `/tmp/netcap-claude-worker-retry-24c98iiy`、`/tmp/netcap-claude-worker-retry-54d256of`、`/tmp/netcap-claude-worker-retry-hwmn6kzp`。足以复核的原始记录已归档于 [A/B](W01-04-native-recovery/ab/claude-supervisors/observations.json.gz)、[supervisor](W01-04-native-recovery/supervisor/supervisor-crash/observations.json.gz)、[worker](W01-04-native-recovery/worker/worker-crash/observations.json.gz)与[最终重试](W01-04-native-recovery/retry-final/result.json.gz)。

原 `ToolProvider` 只会发一次工具请求，并精确匹配不带末尾换行的提示；因此原探针的一次实际执行不足以证明原生不会重试。新增 `RetryingProvider` 按末条原提示识别恢复请求，两次请求使用不同工具 ID。第二次审批未批准，所以只有原工具实际启动和完成；这只是所测默认权限下的审批事实，不推广为其他权限模式或供应商行为的保证。所有主请求保留完整原生 ID，但恢复请求缺少原 tool_use/tool_result；原工具输出没有进入任何后续模型请求。

首次将该原生 profile 接入生产发现时返回 409：`Claude settings 的 apiKeyHelper 与登记的凭据引用冲突`。原生读取误用了 `requireCapability(..., "history")` 和执行用的 `profileFingerprint()`，将合法原生认证来源当成新执行的路由冲突。修复增加只读配置准入与 `native_read_error`，共用原字节指纹算法；原有可用绑定保持兼容。只读读取允许字符串 helper 与认证/路由 env 作为数据，不调用 helper；坏 JSON、无效字段类型、HOME/XDG/Claude 状态目录覆盖、reader/Node/profile 缺失、未验证 CLI 或 history 能力继续拒绝。创建、发送以及普通 managed history 仍走原执行准入。此片只覆盖登记 profile/settings_file，未扩展为完整项目、受管或动态配置检查。

[修复前回归日志](W01-04-native-recovery/native-read-red.log)中的两个正向子场景实际失败；[修复后定向日志](W01-04-native-recovery/native-read-green.log)先覆盖 10 个子场景，随后补 5 类 reader/版本前置，最终完整 app/console race 通过（98.861s / 1.722s）。相关 Runtime race、带原生测试标签的 vet、开发构建通过。Web 构建执行了类型检查，生成文件哈希保持原样。没有重复运行与本改动无关的完整 W 系列矩阵。

最终重试的真实 HTTP 记录证明：发现、历史、绑定和观察可读；helper 调用计数未因这些读取增加。六种执行命令 create/send/resume/takeover/interrupt/respond 都返回 409，创建仍报告原认证冲突；SQLite 仅有一个暂停的 `native_readonly` Session，没有原生 operation。worker 替换前后及原工具完成后，生产观察始终为 `sampled` / `unknown` / `complete=false`，只报告 roster 的替换 PID 声明，没有伪造执行器或清除占用。[独立审计源码与结果](W01-04-native-recovery/audit.json)保留上述正例与缺口。

浏览器使用固定 `/home/zzw/.local/bin/playwright-cli` 的 `netcap-w0104-read` 会话和真实生产 Handler。最终服务目录为 `/tmp/netcap-claude-native-browser-c_zzgilm`；[浏览器结果](W01-04-native-recovery/browser.json)、[1440 截图](W01-04-native-recovery/native-read-1440.png)和[390 截图](W01-04-native-recovery/native-read-390.png)已归档。MCP 的 dynamic import 不支持及后续 `Transport closed` 未计通过；CLI 首次脚本异步回调依赖不存在的 `URL` 全局而关闭会话，改用已支持的字符串路径解析后完整通过。两个早期服务租约和最终服务均正常退出，浏览器命名会话关闭；卡住的自有 `playwright-cli list` 经精确 argv/父命令核对后终止，清理记录保留。未改浏览器配置或版本。

归档使用明确文件白名单，包含初始失败、供应商请求、工具标记、stdout/stderr/ANSI、原生主 JSONL、HTTP、SQLite 表审计、测试/构建日志和截图；不复制完整 Claude profile，不归档 daemon/session 私钥、工作台访问令牌或 shell 环境快照。原生 jobs/daemon 文件仅是诊断样本，不提升为生产控制契约。含临时访问令牌的浏览器脚本与原始私密日志已移除，只保留模板和脱敏日志。

6,290 份旧证据、旧 `bin/netcap`、Web/bridge 源码与构建保持不变；本次生产改动为 4 个 Go 文件，新增 3 个测试文件并更新使用说明/进度。最终差异、清理、文件白名单及哈希见 [manifest](W01-04-native-recovery/manifest.json)。没有提交、推送、真实供应商调用或对外发布。

W01.04 从已解除的历史环境阻塞更新为 in_progress，W01.02/W01.05 继续 in_progress；整体仍为 9/40 done、Goal active。下一步核对原生 worker 的恢复/重试选项与可追溯输出恢复路径；`CLAUDE_CODE_RESUME_INTERRUPTED_TURN` 在现有 `claudeEnv` 中被过滤，其后台作用尚未验证，不能直接改变生产契约或宣称解决。独立前台、完整输入权、完整配置/能力、Windows/第二有效环境和其他首版硬要求全部保留。
