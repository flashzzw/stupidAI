# W02.08 · Codex 同步 hook 执行与失败记录

2026-10-03。生产执行详情现在显示 Codex 同步 hook 调用及失败原因，并跨观察/应用重启保存历史。本片完成的是**失败记录、关联与占用保护**，不是原生 hook 成功执行或完整插件选配验收。W02.08 in_progress，9/40 done，Goal active。

## 用户可见行为与持久规则

- Runtime 消费既有连接的 `hook/started` / `hook/completed`；原生会话、轮次、run.id 共同构成记录身份。同一个原始 run.id 出现在另一轮次时不会合并。
- 记录保留原始调用 ID、轮次、source、sourcePath、原生状态、时间和耗时。`completed/failed/blocked/stopped` 分别展示成功/失败/失败/取消，并保留原生状态；不填造 exitCode。事件没有完整 pluginId，不从文件路径或同名事件猜资产。
- 只把严格匹配的原生超时诊断转换为 `failure_kind: timeout`，命令、任意输出、statusMessage 和错误正文不进入产品记录。来源路径仍可能指向插件安装缓存，并不代表缓存内容已与资产固定绑定。
- Application 只按同会话、同原生 ID、同轮次的唯一已保存输入操作关联记录；迟到事件仍关联原操作。新观察或新连接没有重放旧通知时，已有终态历史保持。
- 已知调用仍在执行、没有有效终态或元数据冲突时保留占用；新的空闲回执不能据此完成原输入、放行新输入或恢复控制。断连将原运行记录转为 unknown。原有矛盾不会被迟到事件覆盖，重复事件不重复计数。
- Codex 的完整完成摘要本身带开始/结束时间和耗时，经过字段校验后可以记录终态，不要求客户端此前恰好收到 started；缺少或无效完成字段则保留 unknown。Claude 原有需完整生命周期的规则保持。

共享界面组件显示来源路径、原生状态、安全超时原因和原操作，移动视口换行显示长路径；断连时保留历史结果，原运行记录显示待核对。异步 hook 不在该原生通知的覆盖范围内，界面明确标注。

## 已执行验证

| 层次 | 结果 |
|---|---|
| Runtime 公开 Observe | 15 场景通过：开始占用、四种终态、重复/迟到开始、跨轮次/其他会话、来源冲突、缺结束/无效开始/未知状态、断连和安全超时分类；Claude 8 场景回归保留 |
| Application 公开 API/持久化 | 7 场景通过：历史不被空观察清除、缺终态阻止发送/恢复、原操作保持 uncertain、迟到原操作关联、已有失败终态与运行状态两种重启、断连 API/占用变 unknown |
| 固定 Codex 原生 | [最终 12 项](W02-08-codex-hook-executions/native-result.json)通过：真实插件超时、来源/调用/轮次/原操作/时间/耗时、退出码缺省、主动取消轮次、SQLite 重启保存、配置不被产品修改、Handler 响应 |
| Codex 浏览器 | [23 项](W02-08-codex-hook-executions/browser.json)通过：最终构建回放真实 Application/Handler 数据；检查超时/原状态/来源/关联、移动布局、会话切换、合成 running/unknown 与断连、无执行请求。HTTP/SSE 为受控回放 |
| Claude 回归 | 原生 Application [17 项](W02-08-codex-hook-executions/claude-native-result.json)、共享界面 [18 项](W02-08-codex-hook-executions/claude-browser.json)通过，含真实 0/2 结果及来源未报告说明；未结束状态为合成展示样本 |
| 竞争/静态/构建 | 最终 app/console 全包 race、Codex Runtime 与两类 hook 定向 race、相关三个包带 nativecodex/nativeclaude 标签 vet、`make dev-build`（含前端类型检查）通过 |

最终 Codex 原始目录 `/tmp/netcap-w0208-codex-hook-records-1554252726`，Claude `/tmp/netcap-w0208-plugin-app-1615771888`。Codex 测试使用已有 native stdio fixture；测试准备阶段在隔离 profile 安装本地插件并明确配置信任，再由**生产 Application**创建两个会话，其中一个通过生产 send 触发一次 SessionStart 超时，随后通过生产 interrupt 取消。供应商仅为不可用的 `http://127.0.0.1:9/v1`，无成功模型回复、MCP 调用或方法 Gate。另一个会话未发送输入，没有 hook 调用。四个自有原生进程均退出 0；浏览器已关闭。

此前 [最小执行诊断](W02-08-codex-hook-execution-contracts.md)中的成功预期仍失败。本片将真实 failed/timeout 作为记录链路的输入，并核对产品忠实呈现，不把原生失败改称成功。`plugin/install`、信任配置写入仅用于自有测试准备，未新增生产安装/授信接口。

## 红灯与测试修正

行为红灯和相应修复均保留于附件 logs：原生 started 被丢弃、terminal 未投影、重复/迟到/冲突/断连处理、空观察清历史、缺终态放行发送、原操作错误完成、迟到操作没有关联、断连仍声明 live，以及超时缺少安全分类。

另有三类测试/构建修正，不算产品行为故障：应用本来就忽略仅时间戳改变的观察，历史测试改用新的诊断标识等待实际观察；浏览器执行环境没有全局 URL，改用已有路径解析方式，随后才取得旧界面无入口的真实红灯；断连文案预期改为实际“连接断开 · 数据已过期”。共享组件改名时漏改 main.tsx 的类型导入，首次构建失败已修正；最终类型检查和构建通过。早期原生结果 `/tmp/netcap-w0208-codex-hook-records-2403294924`、`/tmp/netcap-w0208-codex-hook-records-523682087` 同样保留，不与最终计数累计。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(Codex|ClaudeHookExecution|ClaudeHookCatalog)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags 'nativecodex nativeclaude' ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags nativecodex ./internal/runtime -run '^TestCodexNativeHookExecutionRecords$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -tags nativeclaude ./internal/app -run '^TestClaudeNativeApplicationHookExecutions$' -count=1 -v
```

浏览器模板：[Codex](../../tests/codex_hook_execution_browser.js)、[Claude](../../tests/claude_hook_execution_browser.js)。附件保留实际响应、最终 [桌面截图](W02-08-codex-hook-executions/failure-1440.png)/[移动截图](W02-08-codex-hook-executions/failure-390.png)、日志、[源码差异](W02-08-codex-hook-executions/changes.diff)、[独立核对](W02-08-codex-hook-executions/audit.json)与[归档时 manifest](W02-08-codex-hook-executions/manifest.json)。差异按前置备份生成，包括 ClaudeHookExecutions 删除和共享 HookExecutions 新增；不依赖本仓库 untracked 文件的 git diff。

本片 `.dev/bin/netcap` SHA256：`4dbe88331739e983c749465bd28639a468f4724a490f7cb2671d72b7fe8316aa`。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。1283 个既有证据文件未改写，用户已有改动保留；未提交或对外发布。

## 剩余要求

Codex 原生 hook 成功执行及超时原因、异步 hook 可观测性、完整资产/缓存内容绑定、逐线程插件 Skill/MCP 选择与排除、完整权限和带成功模型历史恢复仍未完成。生产 Unix proxy/peer 与 Windows 不由本片 stdio 证明。Claude 完整改配和先前 Skill 探针仍待已请求的终端结果；不重复请求授权或无新条件的超时实验，下一片继续实际缓存/资产绑定与完整排除。
