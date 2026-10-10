# W02.08 · Codex hook 命令退出与原生完成回执

2026-10-05。固定 app-server 0.158.0，继续调查已有 SessionStart 超时。**成功执行预期仍失败：15 项中 14 项通过，退出 1。** 新证据确认测试命令已经执行并以退出码 0 结束，原生 hook 仍到 3 秒才报告超时；故障收敛到命令退出后的收尾路径，尚未确定是 CLI 实现还是当前代理隔离环境的具体作用。W02.08 in_progress，9/40 done，Goal active。

## 已验证的变化

新增 [生命周期诊断](../../tests/codex_hook_lifecycle_diagnosis.py)，复用既有执行探针，仅注入观测客户端。命令向临时文件追加自身 PID；客户端只读取自有 app-server 及子进程的 `/proc` 状态、退出状态和线程子进程列表，不读环境、真实会话或文件描述符。诊断保留原生成功预期，不因命令退出 0 将 timeout 改写为成功。

| 证据 | 最终结果 |
|---|---|
| 相同环境直接执行命令 | 退出 0，写出第一个 PID |
| 已信任插件的原生命令 | 写出第二个 PID；该 PID 的父进程为自有 app-server，进程名为 bash，状态为 Z，原始 wait status=0 |
| 原生完成事件 | status=failed，durationMs=3001，`hook timed out after 3s` |
| 未信任、禁用对照 | 无 hook 事件；均到达不可用本地供应商的连接错误 |
| 协议服务端请求 | 没有待回答的服务端请求，不能归因于测试漏答审批 |
| 信号观测 | 观察到的父进程和 hook 主线程没有阻塞信号；未测所有线程或具体通知机制 |
| 生命周期结束 | 三个轮次均明确 interrupted，自有 app-server 退出 0；观测无读取错误 |

原始目录 `/tmp/netcap-w0208-hook-lifecycle-szfs6k3g`。[结果](W02-08-codex-hook-lifecycle/diagnosis/result.json)、[PID/退出诊断](W02-08-codex-hook-lifecycle/diagnosis/diagnosis.json)、[状态采样](W02-08-codex-hook-lifecycle/diagnosis/owned-processes.json)、[协议](W02-08-codex-hook-lifecycle/diagnosis/native-protocol.json)和 hook trace 均已归档。退出码只属于本次诊断观察；产品原生事件依旧没有该字段，生产展示不编造它，也不把子进程退出提升为完整 hook 成功。

## 排查过程与范围

先读取旧失败夹具自有 `logs_2.sqlite`，两份日志库均没有可用日志行；旧协议也没有服务端请求。依据 [OpenAI Docs 的日志说明](https://developers.openai.com/codex/config-file/environment-variables)，在新建隔离客户端中对 `codex_hooks::engine::command_runner` 开启 trace。第一份观测保留原 `true` 命令，发现子进程在超时前保持 Z 状态；随后用 PID 标记将命令与退出状态直接关联。原生 trace 仅报告 timeout，没有更细的根因。

排名依次检查了启动前阻塞、输出通道收尾、内部完成通知三个假设。PID 标记和退出状态排除了本样本“命令未执行”的解释；剩余两类尚不能区分。未改第三方 CLI、发送补偿信号、重探 listener/Windows 权限或延长超时。原生启动的插件目录同步仍可能尝试内置远程元数据端点并失败；没有真实供应商认证或成功模型结果。

`exploration/` 保留四个先前样本及脚本，其中两版读取文件描述符的诊断因退出进程的访问限制遗漏了进程样本，不能作否定证据。最终脚本去掉这类读取，仅使用已能核对的状态路径；不再尝试该受限读取。第一版 trace 包含较宽的执行日志，归档只摘录 hook runner 信号；完整原始日志仍在对应临时目录。

## 超时记录回归

[生产原生回归夹具](../../internal/runtime/codex_hook_execution_native_test.go)将 `true` 改为 `exec sleep 2`，保留 1 秒期限，使测试的失败来自明确超时条件。这是测试夹具修正，没有修复或掩盖上述成功命令超时，旧证据保持原样。

`TestCodexNativeHookExecutionRecords` 带 race 通过，**12 项**检查覆盖原调用/轮次、原操作、超时元数据、HTTP 投影和 SQLite 重开保留；四个自有后台均退出 0。[结果](W02-08-codex-hook-lifecycle/timeout-records/result.json)及[运行日志](W02-08-codex-hook-lifecycle/logs/native-records.log)已归档，原始目录 `/tmp/netcap-w0208-codex-hook-records-1495225475`。这仍是超时失败链路回归，不是成功路径验收。浏览器、生产 Go/Web 和开发构建没有变化，未重复浏览器或全包检查。

## 重跑与后续

```bash
python3 tests/codex_hook_lifecycle_diagnosis.py

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeHookExecutionRecords$' -count=1 -v
```

两个 Python 文件的 AST 检查通过。当前诊断的成功预期失败已如实保存；已请求同脚本的 WSL 终端对照，尚未收到结果，不用基础宿主检查替代 hook 完成验收。原有 Claude 插件改配、Claude Skill 方法、Codex Skill 完整选择三个终端结果也仍待返回，没有重复请求。

[审计](W02-08-codex-hook-lifecycle/audit.json)、[差异](W02-08-codex-hook-lifecycle/changes.diff)与[归档时点哈希](W02-08-codex-hook-lifecycle/manifest.json)核对源码、文档、附件和两个二进制。生产代码与构建不变，未提交。下一步据终端对照区分运行环境影响，再推进 hook 成功/失败归属和组件选配；异步、普通 Skill 恢复、方法/Gate、受管来源、Windows/proxy 和输入独占等硬要求继续保留。
