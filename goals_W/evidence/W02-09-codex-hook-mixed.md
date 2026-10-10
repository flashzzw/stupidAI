# W02.09 · hook 文件、混合资源与冷候选

2026-10-06。延续 [原 ID 明确采纳](W02-09-codex-hook-adoption.md)，完成混合能力、worktree 实际来源及保存后冷重开的有限切片。W02.07/08/09 仍为 `in_progress`，9/40 done，Goal active。原生固定为 app-server 0.158.0、CLI 0.157.1；使用生产 Application、SQLite、Runtime 与 Handler。

## 用户可见行为

保存 hook 文件候选时，同时持久化经原连接核对的原配置摘要、原绑定摘要、记录时间及候选保存时的原生受管要求摘要。预览不输出或持久化证明；保存不改变旧快照、原生身份或当前能力。证明只用于已有候选的准备/应用，不授予普通输入权限。

关闭应用及自有原生进程后，仍可继续准备该候选，但必须确认原线程与完整加载清单均未加载、原历史完整、候选文件未再次漂移、未采纳配置及记录的受管要求未变。准备保持暂停且不加载线程；明确应用才按原 ID 加载候选。证明与原候选一起保留，重开、读取和准备不重写时间或原版本。

只有“原连接建立依据缺失”允许进入持久证明路径。策略读取失败、配置不符或来源变化均直接拒绝，不能把失败当作没有策略。普通文件 hook 原来的连接指纹并不包含完整受管要求，因此本片另存**候选保存时**的要求摘要；不冒称它代表原会话创建时的全部策略。准备结束、应用前再次复核。

旧候选缺少证明时，只能依靠仍可核对的原连接；不从当前新文件倒推旧配置。应用操作必须引用保存的证明，删除或替换操作中的原身份、配置、策略或时间字段均拒绝。界面说明保存依据与当前加载状态的区别，展示记录时间，并保持明确的准备/应用操作。

CLI 插件与独立 Skill 可伴随文件采纳联合启用/撤去；文件、能力候选和历史属于同一快照事务。独立 Skill 在此后端通过私有插件包交付，资产类型仍为 `skill`。worktree 用例同时保留工作树根、工作目录的配置层来源和主仓库中实际被原生发现的 hook 文件，不以当前目录猜测来源。

原生信任配置保持不变，不自动信任新 hash。本片继承上一片创建前已经明确授信的精确测试定义。文件、插件与 Skill 的实际调用、清单及快照相互核对；保存/准备不伪装实际生效。

## 验证

| 层次 | 实际结果 |
|---|---|
| 混合插件（MCP、Skill、hook）与独立 Skill | 15 项通过：联合启用、原 ID、实际 hook 版本、冷恢复、联合撤去 |
| worktree 主仓库 hook 文件 | 12 项通过；三个来源关系保留，主仓库文件改版及冷恢复有实际调用 |
| 保存候选后冷重开 | 16 项通过；原快照/候选/证明不变、漂移拒绝、准备不加载、明确应用与随后冷恢复 |
| 原用户/项目/新项目目录/丢回执恢复回归 | 共 43 项通过 |
| 已确认原配置恢复跨重启回归 | 108 项通过；改进测试事件等待后 124 秒完成 |
| Runtime 证明边界 | 15 类通过；完整加载清单、另一个已加载记录、配置/候选漂移、证明损坏、冷/热策略错误及变化 |
| App 持久候选及操作绑定 | 通过；另有 5 类操作证明篡改负例、无证明旧候选兼容检查 |
| App/Console race、相关 Runtime race | 通过；Runtime 选择已有与此片相关的集合，不包含已知监听 EPERM 用例 |
| tagged vet、Web 类型检查与构建、开发 Go 构建 | 通过 |
| 浏览器 | 24 项通过；真实冷重开 Handler 数据、模拟命令/SSE 回放，含 390px 窄屏 |
| 独立归档核对 | 294 项通过；保存/准备/应用、原始摘要、实际 marker、策略摘要、恢复通知和进程结果 |

恢复回归及归档复核的完整结果见 [命令记录](W02-09-codex-hook-mixed/command-results.json)及[独立核对](W02-09-codex-hook-mixed/independent-audit.json)。

所有 hook 都写入自有 marker 后故意超时，所有模型输入指向不可用的 `127.0.0.1:9`，逐轮显式中止。它们证明加载与调用归属，**不证明成功 hook、模型任务、方法效果或 Gate**。浏览器没有向原生发送输入，也不替代 WSL/Windows 宿主验证。

## 失败与修正

- 先复现冷候选因连接建立依据丢失而被拒；新增持久证明后，准备仍缺少传给未加载查询的历史摘要。补齐摘要，保留原查询的严格准入，最终真实原生通过。
- 混合测试初版误将私有包交付的独立 Skill 当作原生目录 Skill；改为按资产 ID、组件及原资产类型核对。worktree 初版已通过。
- 证明夹具初版未实现只读 `configRequirements/read`。补齐明确无要求响应，并加入冷/热策略读取失败和策略变化负例；生产不吞掉此类错误。
- 浏览器初版对用户文件要求不存在的项目来源字段。按来源类型核对，用户文件保留路径与版本，最终 24 项通过。独立审计初版未考虑初始快照省略 revision，已按既有版本契约核对，最终 294 项通过。
- 恢复回归两次在已通过 100 项后触及夹具三分钟期限，最后的自有原生进程被测试超时结束；失败记录保留。等待事件时反复序列化约 120 MB 的累计 trace，会消耗测试期限。改用目标线程/轮次的原始通知查询，完整 trace 仍在检查点和退出时归档，未延长期限或放宽原生断言。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/... ./cmd/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags nativecodex ./internal/runtime -run '^TestCodexNativeHookSourceAdoption(Mixed|Worktree|ColdPending)?$' -count=1 -v
```

恢复回归入口为 `TestCodexNativeOriginalConfigurationRecoveryAcknowledgedRestart`，使用同一组环境变量与标签。[浏览器脚本](../../tests/codex_hook_source_proof_browser.js)的 `__FIXTURE__` 使用归档冷候选 `browser-responses.json.gz`，`__ASSETS__` 映射当前 `web/dist` 资源绝对路径；通过 Playwright 文件脚本执行。

原目录、失败/成功日志、压缩原生结果、截图、原始文件摘要与源码/构建摘要见[目录映射](W02-09-codex-hook-mixed/original-directories.json)、[清理记录](W02-09-codex-hook-mixed/cleanup.json)、[产物清单](W02-09-codex-hook-mixed/manifest.json)。4913 个旧证据、Claude bridge 与旧 `bin/netcap` 保持不变。未提交、推送或操作共享后台。

## 剩余范围

本片冷候选正例为用户文件；混合资源正例在启用后冷恢复，再从可核对连接联合撤去，不能据此宣称所有混合候选冷应用组合均完成。共享后台和跨客户端输入独占、保存/准备/应用各崩溃窗口、引用脚本与全部受管/有效配置、成功/通用/异步 hook、真实模型/方法/Gate、安装来源和 Windows 尚需继续。五项既有 WSL 请求继续等待，不重复索取。下一片优先验证冷候选的混合原配置及准备后重启边界。
