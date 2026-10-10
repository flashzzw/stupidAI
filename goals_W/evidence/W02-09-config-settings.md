# W02.09 配置设置差异与原 ID 恢复边界

日期：2026-10-07。W02.09 保持 `in_progress`，W 系列保持 9/40 done。本片交付保存版本与当前磁盘的设置差异；一般配置版本的明确应用、原生生效确认和失败恢复仍未接通。

## 用户可见变化

新普通 Codex 来源快照保存 `settings.version=1`：仅保留固定公共 effort 枚举值，其余值及来源只保留 SHA256。effort 未设置和未知值分别记录，不推断模型默认值、不暴露未知正文。来源版本依然绑定快照、Scope 和操作；历史不改写。

现有只读配置清单返回 Application 按已保存 Scope 计算的 `settings_difference`，绑定原快照 ID。页面区分 effort 合并值、包含被覆盖层的 effort 来源、其他合并设置、其他设置来源及层结构。其他字段 origin 的整层版本变化不会被误判为该字段的值变化；来源完整版本与文件字节/权限仍沿原有检查核对。配置层顺序、未知元数据、自定义键和嵌套配置变化不会从比较摘要中消失。

旧快照缺少比较依据时显示未记录，继续核对原有完整来源摘要。旧 hook 候选保持其已保存的记录格式；新增读数不能隐式补写候选或阻止原有 hook 采纳。此界面不更新快照、不应用配置、不授予输入权。

## 原生边界

固定 app-server `0.158.0`，proxy `0.157.1`，均为自有隔离 stdio 进程。每例只用一次指向不可用 localhost provider 的输入建立历史并明确取消，不是成功模型或工具调用。最终 11 例、84 项断言通过，每例核对原 ID、完整历史及取消完成通知；成功恢复另核对回执和 `thread/read` 的 effort 一致。

| 已保存 low，随后修改磁盘 | 恢复参数 | 实际结果 |
|---|---|---|
| 热线程，磁盘 high | 无覆盖 / 显式 high | 都保留 low |
| 当前连接退订，磁盘 high | 无覆盖 | 保留 low |
| 当前连接退订，磁盘 high | 显式 high | 变为 high |
| 自有后台重启，磁盘 high | 无覆盖 / 显式 high | 都变为 high |
| 热线程，磁盘清除 effort | 显式 null | 保留 low |
| 退订 / 自有后台重启，磁盘清除 effort | 显式 null | RPC -32600，`reasoning_effort must not be empty` |
| 当前连接退订，磁盘清除 effort | 无覆盖 | 保留 low |
| 自有后台重启，磁盘清除 effort | 无覆盖 | null，未推断模型默认值 |

这是固定版本的实验结果，不构成停止共享后台或统一热改配置的产品入口。当前比较依据也不构成原线程已建立配置的证明。

## 验证及失败记录

- 原生生产 Application/Handler 清单 **50 项**：保存/Scope 绑定、按原快照比较、排版权限独立、effort 变化、被覆盖 effort、自定义来源、其他合并设置、未设置、项目失去信任，以及所有清单调用仅使用 32 次配置/要求读取。读取不改变快照或新增原生操作。
- Runtime 新增 **19 场景**：比较/隐私/隐藏层/顺序/未知元数据/默认/未知值/旧记录兼容，以及无效格式拒绝；既有目录/配置/Hook/MCP 相关 race 检查通过。
- Application 旧记录比较、绑定竞争回归，以及旧 hook 候选保存/持久重开通过。App/Console race 通过；最后的兼容修复另跑对应 Application race 入口。
- 既有真实原生冷漂移/还原 **12 项**、hook 采纳及丢回执恢复 **43 项**通过。
- 浏览器回放真实 Handler 响应 **39 项**，1440/390 宽度；合成旧/缺失/未知格式显示 **14 项**。页面无错误，仅发读取请求；截图见 [桌面](W02-09-config-settings/changed-1440.png) 与 [移动宽度](W02-09-config-settings/untrusted-390.png)。浏览器回放不替代真实原生操作验收。
- Web 类型检查/构建、开发 Go 构建与 tagged vet 通过，旧 `bin/netcap` 未替换。

保留三类失败：`HasTurnEvent` 原本只解析顶层 turnId，取消完成通知使用嵌套 turn.id，新增断言先红，修复后所有边界场景约 7 秒完成；显式 null 的首次实验失败保留，最终作为明确拒绝边界验证；自定义键初始被误认为会进入合并配置投影，实测只出现在来源层，改为分别验证来源变化与受支持的 personality 合并值变化。另保留新增比较字段导致旧 hook 候选误拒的红灯，修复后原候选仍可保存并持久重开。

## 可重跑入口

```bash
export GOCACHE=/tmp/netcap-w0206-auth-go-cache
export NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
export NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeConfigurationResumeBoundary$' -count=1 -v
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeConfigurationCatalog$' -count=1 -v
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNative(ConfigurationBindingColdDrift|HookSourceAdoption)$' -count=1 -v
go test -race ./internal/runtime -run '^Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings|MCP)' -count=1
go test -race ./internal/app ./internal/console
go test -race ./internal/app -run '^Test(ConfigurationSettings|ConfigurationCatalog|CodexHookSource)' -count=1 -v
go vet -tags=nativecodex ./internal/... ./cmd/...
make dev-build
```

浏览器模板为 `tests/configuration_catalog_browser.js` 与 `tests/configuration_settings_legacy_browser.js`，以原生生产 `result.json` 的 responses 和本轮 `web/dist` 静态路径填充 `__FIXTURE__`、`__ASSETS__`，通过 Playwright 执行。旧记录模板明确只用于显示契约；生产兼容行为由 Go 入口验证。

## 归档与限制

[审计](W02-09-config-settings/audit.json)、[逐命令结果](W02-09-config-settings/command-results.json)、[来源差异](W02-09-config-settings/source.diff)、[清理](W02-09-config-settings/cleanup.json)、[原始目录索引](W02-09-config-settings/original-directories.json) 和 [SHA256 清单](W02-09-config-settings/manifest.json) 可复核本片。33 份原生结果包含完整调用轨迹，压缩归档；84 个自有原生进程全部正常退出，浏览器关闭。5651 个既有证据、旧二进制与 Claude bridge 源码保持原哈希；未提交或推送。

开发二进制 SHA256：`47b4d01be1c7a778a3306a3bbee1c83a8b6d37c6b8bad54ae579c103da96b0c6`。

下一片基于新记录的比较依据接入非空 effort 候选、原连接证明、原 ID 显式应用和回读；旧记录不能从当前磁盘补造原值，其他配置/路由/凭据/权限及隐含能力变化不得顺带升级。null 清除、冷恢复与所有 unknown/丢回执边界需要独立契约。完整受管/外部依赖/动态权限、四配置联合、W02.07/08、真实模型方法/Gate、成功/通用/异步 hook、共享后台与输入独占、Windows 和五项既有 WSL 请求继续保留。用户再次提供的 Claude `bp2rp6xu` 属于既有归档，未重复运行。
