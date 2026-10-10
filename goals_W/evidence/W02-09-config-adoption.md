# W02.09 明确采纳配置来源版本

日期：2026-10-07。本片接通已记录非空 reasoning effort 与相同设置的文件版本采纳，复用原 ID 配置事务和失败恢复。W02.07/08/09 仍为 `in_progress`，W 系列保持 9/40 done；其他一般配置、完整权限与四配置联合验收尚未完成。

## 实现与范围

- 原会话勾选“采纳当前配置来源版本”，请求 `adopt_codex_configuration_sources=true`。预览列出旧/新 effort、文件版本与权限；保存只登记候选，不改变当前 Scope、快照或指纹。
- 采用已保存的安全设置摘要，只允许原值和目标均为非空公共枚举，或设置未变的文件版本变化。其余合并设置、被覆盖层中的其他键、层结构、来源身份及路径保持一致；系统/受管层、清除 effort、未知值和缺少旧值依据拒绝。项目 hook 只允许与已核对配置层对应的层版本更新，其文件、发现关系和信任不变。
- 原连接证明复用既有来源事务：用候选核对当前磁盘，将唯一允许变化的公共 effort 值还原为已记录原值后核对原配置完整指纹。证明绑定原 Scope、候选来源摘要和保存时受管要求，不能用文件存在或时间推断线程已加载。冷继续必须持有已保存证明，原线程及完整加载清单确认未加载，并核对历史不变。
- 准备只读并保持暂停。明确应用取得本连接退订依据，按原 ID 显式覆盖已保存 effort，核对回执和线程回读后才晋升。主配置/路由指纹按经验证的候选文件晋升；其他路由、凭据、实例及状态归属保持原检查，普通输入没有新增豁免。
- 应用后观察和输入核对已记录 effort；恢复也使用保存的显式值。回执丢失保留原快照、候选和 unknown，重复命令不重放。原文件还原、原线程确认未加载后，可明确恢复原设置/原 ID/原历史，单独保留失败候选并继续暂停。
- 共享能力引用仍独立核对；主配置变化不能自动升级引用同一文件的能力版本。能力选配继续沿既有事务；本轮新配置与混合能力的完整组合验收仍保留。

## 实际验证

固定 app-server 0.158.0、proxy 0.157.1，通过隔离 stdio 和临时 profile/工作目录/SQLite。新增每例只用一次指向不可用 localhost provider 的输入建立历史并明确取消，不包含成功模型/工具调用。

| 验证 | 结果与范围 |
|---|---|
| 用户配置 | 19 项通过：low→high、精确差异、原版本保存、实际回读、原历史、重启；拒绝其他设置、自定义来源、信任、hook 文件变化及过期预览 |
| 项目配置 | 12 项通过：配置层关联更新，实际 high、原 ID/历史与重启保留 |
| 仅文件版本 | 12 项通过：明确采纳排版版本，实际 effort 保持 low |
| 保存后冷重开 | 13 项通过：候选不变，准备不替换原版本，原 ID 明确应用后为 high |
| 实际丢回执及恢复 | 14 项通过：不晋升、不重放；还原原文件后恢复 low、原 ID、完整历史及暂停 |
| 最终原生确认 | 最后恢复回读检查补齐后，用户场景 19 项再次通过 |
| Runtime | 新增 21 类采纳差异边界、证明目标绑定、加载设置不匹配、无 credential stamp 时仍核对设置；共 24 场景。相关 Runtime race 通过 |
| Application | 5 类候选持久化、差异/证明篡改、来源漂移及共享能力引用检查通过；App/Console race 与最终相关 race 通过 |
| 原生回归 | 原冷漂移 12、hook 采纳/丢回执恢复 43、混合能力准备后冷重开 26 项通过 |
| 浏览器 | 应用 24、失败恢复 14 项通过；回放真实 Handler 响应，命令/SSE受控。原配置、候选、明确应用、失败归档及暂停可见 |
| 构建 | Web 类型检查/构建、开发 Go 构建、tagged vet 通过；旧 bin 不变 |

保存、准备、实际回读和重启的断言独立于页面标签。浏览器回放不替代原生验收；恢复中的“原文件尚未还原”显示由合成未满足响应覆盖，真正原文件要求由原生恢复场景验证。

## 失败与修复

初次用户配置实验被原路由文件指纹挡在预览前；明确采纳现在先核对原记录与原路由证明，再验证候选的允许差异，普通输入保持原检查。随后发现 `SourceStamp` 包含文件元数据，其 SHA 不等于主配置原始字节指纹；改为使用已保存原生用户层的文件 SHA，并在晋升时按相同规则更新。两次原生失败及修复后的成功轨迹均保留。

恢复浏览器的合成 changed 来源最初缺少 detail，触发渲染异常；按真实 Handler 的 changed 来源格式补齐后通过。移动宽度截图发现新勾选框未沿用已有横向排列规则，已复用原 hook 勾选样式并重新核对。

## 重跑入口

```bash
export GOCACHE=/tmp/netcap-w0206-auth-go-cache
export NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
export NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeConfigurationAdoption$' -count=1 -v
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNative(HookSourceAdoption|HookSourceAdoptionMixedColdPrepared|ConfigurationBindingColdDrift)$' -count=1 -v
go test -race ./internal/runtime -run '^Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings|MCP)' -count=1
go test -race ./internal/app ./internal/console
go test -race ./internal/runtime ./internal/app -run '^Test(CodexConfiguration|CodexRecordedEffort|CodexRoute|CodexHookSource|ConfigurationSettings)' -count=1 -v
go vet -tags=nativecodex ./internal/... ./cmd/...
make dev-build
```

浏览器模板：`tests/codex_configuration_adoption_browser.js`、`tests/codex_configuration_adoption_recovery_browser.js`；用对应 `result.json` 和当前 `web/dist` 路径替换 `__FIXTURE__`、`__ASSETS__` 后交给 Playwright。截图：[预览](W02-09-config-adoption/preview-1440.png)、[准备](W02-09-config-adoption/prepared-390.png)、[恢复归档](W02-09-config-adoption/recovered-390.png)。

[审计](W02-09-config-adoption/audit.json)、[命令结果](W02-09-config-adoption/command-results.json)、[源码差异](W02-09-config-adoption/source.diff)、[原始目录](W02-09-config-adoption/original-directories.json)、[清理](W02-09-config-adoption/cleanup.json) 与 [版本清单](W02-09-config-adoption/manifest.json) 随片归档。20 份原生结果保留完整压缩轨迹；67 个自有原生进程正常退出，浏览器关闭，5716 个既有证据及旧 bin/Claude bridge 不变。未提交、推送或修改用户配置。

下一片继续配置与混合能力组合、准备后冷重开、候选再漂移及失败恢复的持久边界。一般配置、清除 effort、完整受管/外部依赖/动态权限、四配置联合、W02.07/08、真实模型方法/Gate、成功/通用/异步 hook、存活共享后台与输入独占、Windows及五项既有WSL请求仍未完成。
