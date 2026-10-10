# W02.09 配置与混合能力组合

日期：2026-10-07。W02.07/08/09 保持 `in_progress`，W 系列仍为 9/40 done。本轮在上一片明确采纳基础上验证混合选择和持久边界，并修复真实冷加载问题；不缩减其他一般配置、完整权限和四配置联合要求。

## 问题与修复

真实原生复现：原配置 effort=high，已启用带 MCP/Skill/hook 的插件和独立 Skill；保存 effort=medium 且禁用两个资源，准备后关闭自有应用/原生进程再重开。冷插件策略路径先加载一个排除受控插件的中间配置，但错误地同时采用 medium；后续对原版本 high 的空闲/来源证明检查拒绝继续，事务成为 unknown。

原始轨迹明确记录中间 `thread/resume` 请求 medium、回执也为 medium，原快照仍为 high，排除了仅显示错误或回读陈旧的解释。修复仅在正常配置采纳的中间加载保留原已记录 effort，并检查回执；最终恢复仍显式采用候选值。原配置恢复继续采用恢复目标的原值，不把失败候选值带回恢复中间态。原有文件、身份、历史、策略、证明与 unknown 门槛均保留。

生产修改位于 `internal/runtime/codex_cold_plugin_policy.go`；其他改动为验收入口。新增原生验收复用现有配置采纳夹具与已验收插件/独立 Skill 测试资产。

## 验证

固定 app-server 0.158.0、proxy 0.157.1；全部原生进程、profile、项目、资产与 SQLite 均在自有临时目录，通过 stdio 连接。新增输入指向不可用 localhost provider 并明确取消；测试 hook 写入带原 session_id/prompt 的标记后刻意超时。没有成功模型、工具、Skill 方法或成功 hook 生命周期证据。

| 原生场景 | 断言数 | 核对内容 |
|---|---:|---|
| 配置 + 混合插件 + 独立 Skill 新增 | 13 | 同一候选、实际 high、组件清单、原 ID/历史及重启 |
| 保存后重启 | 14 | 持久候选及原证明保留，明确应用后 high 与组件匹配 |
| 准备后重启及候选再次漂移 | 16 | 原快照/候选/暂停保持；漂移拒绝且没有恢复派发，还原候选后可应用 |
| 初次混合应用丢回执 | 14 | unknown 不晋升、不重放，原文件还原后恢复原 low 和空显式选择 |
| 冷重开降低 effort 并禁用混合资源 | 22 | high 中间加载、medium 最终生效、原 ID/历史、已撤去资源；实际 hook 从执行变为不执行 |
| 撤去过程中间加载丢回执 | 27 | unknown 保留原 high 与候选；明确恢复 high、原能力与原历史，实际 hook 再次执行 |
| 撤去过程最终加载丢回执 | 27 | 实际 medium 回执丢失不被当作成功；恢复 high 和原能力，失败候选只归档一次 |

上述 **7 类、133 项**通过。冷插件恢复既有 final 丢回执场景另有 **46 项**回归通过；相关 Runtime race、tagged vet、开发构建通过。中间加载首次失败和修复后通过的完整原生轨迹均保留。

浏览器回放实际 Handler 响应：联合配置/资源候选准备与应用 **17 项**、原混合能力恢复 **16 项**通过。MCP/Skill/hook 插件和独立 Skill 按原产品类型展示，effort 与资源变更属于同一候选；恢复保留原快照、失败候选和暂停。两次初始浏览器断言分别误用资产显示名、把交付为内部插件的独立 Skill 当作第二个 CLI 插件，修正测试预期后通过；没有为满足断言改变产品展示。浏览器命令/SSE是受控回放，不替代真实原生执行。

## 入口与归档

```bash
export GOCACHE=/tmp/netcap-w0206-auth-go-cache
export NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
export NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeConfigurationAdoptionMixed$' -count=1 -v
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeColdPolicyRecovery/final$' -count=1 -v
go test -race ./internal/runtime -run '^Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings|MCP)' -count=1
go vet -tags=nativecodex ./internal/... ./cmd/...
go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
```

浏览器模板：`tests/codex_configuration_mixed_browser.js` 和 `tests/codex_configuration_adoption_recovery_browser.js`，由本轮真实 result.json 的 samples/responses 填充；恢复模板重映射到 `retire_staged / retire_uncertain / retire_recovered`，不改写原生结果。

[准备截图](W02-09-config-mixed/prepared-1440.png)、[联合生效](W02-09-config-mixed/applied-390.png)、[原能力恢复](W02-09-config-mixed/recovered-390.png)、[审计](W02-09-config-mixed/audit.json)、[命令结果](W02-09-config-mixed/command-results.json)、[源码差异](W02-09-config-mixed/source.diff)、[原始目录](W02-09-config-mixed/original-directories.json)、[清理记录](W02-09-config-mixed/cleanup.json) 和 [哈希清单](W02-09-config-mixed/manifest.json) 随片保存。16 份原生结果含完整压缩轨迹及必要 hook 标记；67 个自有原生进程正常退出，浏览器关闭。

5781 个既有证据、旧 `bin/netcap`、生产 Web 源码/构建及 Claude bridge 源码保持原哈希；仅重建开发 Go 二进制。开发 SHA256：`26a3b6257e4bcc3998d5c78c514025eb761ccf34e0bdf6910494513141321b58`。未提交、推送或修改用户配置。

后续仍需完整混合配置/四配置矩阵、其余策略与恢复丢回执点、提交窗口硬崩溃、全部一般配置及清除 effort、受管/外部依赖/动态权限、W02.07/08、真实模型方法/Gate、成功/通用/异步 hook、存活共享后台与输入独占、Windows及五项既有 WSL 请求；本轮局部通过不替代这些要求。
