# W02.08 · Claude 启动设置来源的独立读取

日期：2026-10-07。已安装插件启动前排除仍需可靠的配置来源依据。本片把“读取失败不能当作来源缺席”的检查接入生产只读 API 与界面；尚未开放已安装插件启停，也没有把完整启动准入条件缩减为读取完成。

## 用户可见结果

执行详情 → Claude 已安装插件登记 → **启动设置来源读取** 可按保存的会话/profile/cwd 显式读取当前来源。入口显示文件内容标识、读取失败/无效 JSON/读取期间变化、Windows HKLM/HKCU 结果、预解析报告的来源层，以及原生 Git/cwd 对应的本地文件。设置正文、环境值、策略内容和原始错误输出不返回。

`POST /api/sessions/{id}/plugins/startup-settings` 只接受空 JSON 对象，沿用登录令牌和同源检查；调用方不能指定另一份 profile、cwd 或文件路径。读取前后复核会话身份与 generation，原生 Git/cwd 文件版本在读取前后不一致则拒绝返回旧结果。读取不修改快照、创建原生操作、启动/恢复 Claude 查询或执行插件。

界面分别显示“本次来源读取完成/不完整”与“完整启动配置仍待核对”。结果 `complete=false`，不会用于放行安装来源；不把系统策略的 present/absent 观察当作已验证完整有效权限。刷新失败清除旧结果，断连禁用读取。

## 实现与依据

Go Runtime 使用保存配置中的 Node/bridge 路径启动一次独立元数据进程，调用固定 SDK 0.3.280 的公开 `resolveSettings()`。该进程观察底层文件读取及 reg.exe 回执，保留 SDK 原本会省略的失败；不在存活 Session/bridge 中修改全局读取方法。读取受进程期限、输出 1 MiB、单文件 1 MiB 和累计内容 8 MiB 约束。子进程原始 stderr 不转发，异常输出或自称完整准入的响应会被拒绝。

早期只观察 `readFile` 的探针没有捕获配置正文读取；实际固定 SDK 使用 open/read 文件描述符路径。最终观察器跟踪这些读取，复制实际返回的字节后计算摘要，检测重复读取内容变化及读后消失；读取失败不能被后续 close 或一次成功补读掩盖。SDK 仍可能省略无效文件，接口会单独列出它们。此观察不承诺全部文件的原子快照或完整 schema 验证。

调用方另外传入已有原生 Git/cwd 来源图与自定义 settings 路径，独立读取并核对；不伪称这些文件来自 SDK 的目录发现。实际仓库子目录测试中，SDK 不报告主仓库 local 文件，新入口仍显示该文件及原生来源版本。该入口不自行重建完整配置合并算法。

[本轮文档依据](W02-08-claude-startup-source-reads/documentation.md)核对了官方公开 resolver、来源和优先级说明，并与固定 sdk.d.ts 比较。policyHelper 不执行、最新服务端策略未获取、原生 Git/cwd 图与 SDK 不同等边界保持可见。受管策略优先于程序传入的 settings，因此不能用派生文件加一次成功 resolver 调用来保证启动排除。

## 实际验证

| 层次 | 结果 |
|---|---|
| 生产 worker + 真实固定 SDK | **7 类、63 项通过**：正常、坏 JSON、底层 EIO、注册表拒绝、超限、helper 声明、读后消失；OS 故障与系统策略路径为受控边界 |
| Runtime 输出/生命周期边界 | **11 类通过**：正常/部分结果、伪完整、SDK 版本不符、另一 cwd、缺字段、无效摘要、矛盾结果、来源漂移、超限输出及取消 |
| 原生 Application/Runtime/Handler | **13 项通过**：真实 Claude 查询创建后关闭，在原保存会话中读取三次，不重新启动查询、不新增 hook/模型输入；实际 WSL 两个注册表 hive 均为 absent |
| HTTP | 新增 **5 项**令牌、同源、参数与会话边界通过，包含在 console 回归中 |
| 浏览器 | **16 项通过**：真实 Handler 正常/坏 JSON 结果、受控 SDK helper 结果、文件摘要、主仓库文件、明确缺口、失败清理及断连/移动布局 |
| 独立审计 | **59 项通过**：重新核对文件 hash、SDK 结果、原始 journal、SQLite 与浏览器请求 |
| 回归与构建 | app/console 全包 race、相关 Claude/FileSource runtime race、tagged vet、bridge build、Web typecheck/build 与开发 Go 构建通过 |

真实 native 样本只有初始一个查询和一个自有 SessionStart 标记，没有 user input 或模型 result。takeover 是同步命令回执，不额外派发原生操作；SQLite 只有 create 操作和 create/takeover 两份命令回执。这里的“启动设置读取”发生在原查询关闭后、潜在下一次启动之前，不等同于已接入新建会话的强制 preflight。

首轮 TypeScript readonly 类型恢复写法失败、原生测试未用 import、HTTP 测试缺同源头、浏览器断连后 details 重置而隐藏按钮、审计误认为 takeover 会派发 operation 的记录均保留。分别修正代码或测试/审计预期后通过，没有放宽产品同源、原生身份或启停限制。

## 复验与归档

```bash
npm --prefix bridges/claude run build
python3 tests/claude_startup_preflight_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaudeStartupSettingsReader' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags=nativeclaude -race ./internal/runtime -run '^TestClaudeNativeStartupSettingsRead$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(Claude|FileSource)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativeclaude ./internal/... ./cmd/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器模板 `tests/claude_startup_preflight_browser.js` 注入实际 Handler 响应与当前 Web 构建；helper 显示分支使用受控 SDK 案例，不冒称 Windows 主机存在该策略。

[原始目录](W02-08-claude-startup-source-reads/original-directories.json)、[命令状态](W02-08-claude-startup-source-reads/command-results.json)、[审计](W02-08-claude-startup-source-reads/audit.json)、[浏览器](W02-08-claude-startup-source-reads/browser.json)、[桌面截图](W02-08-claude-startup-source-reads/valid-1440.png)、[移动截图](W02-08-claude-startup-source-reads/helper-390.png)、[源码差异](W02-08-claude-startup-source-reads/source.diff)、[清理](W02-08-claude-startup-source-reads/cleanup.json)和[最终哈希](W02-08-claude-startup-source-reads/manifest.json)随片保存。5945 个旧证据、旧 `bin/netcap` 及既有 Session/bridge 源码不变；自有进程和浏览器均已收尾，未提交或推送。

后续将这些读取依据与原文件/私有派生 settings 生命周期、完整安装键及实际来源绑定连接，继续实现启动前排除和原 ID 事务。完整受管/remote/helper 准入、权限作用、动态来源、四配置、真实方法/Gate、其他/异步 hook、共享后台/输入独占、Windows 日常链路与 W04–W07 等硬要求仍未完成。W02.07/08/09 保持 in_progress，9/40 done，Goal active。
