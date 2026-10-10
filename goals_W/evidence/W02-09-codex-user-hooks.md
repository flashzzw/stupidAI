# W02.09 · Codex 用户 hook 文件版本与输入约束

2026-10-06。完成有效来源的独立修复切片；W02.09 进入 **in_progress**，W02.07/W02.08 继续 **in_progress**，整体 **9/40 done**，Goal active。完整有效配置与四配置联合验收仍依赖前项收口。

## 用户可见变化

新普通 Codex 执行将保存 profile 的 `hooks.json` 文件版本写入执行快照及原生 Scope：内容摘要、权限、请求路径、解析路径和存在状态，格式为 `codex_user_hooks.version=1`。不存在也会记录，但不创建文件。字段缺失的旧快照保持原状，不补造历史依据。

文件变化后，“核对当前来源”显示差异，Runtime 观察保留原历史但撤下输入就绪状态；输入、审批回复、改配和恢复在派发前拒绝。中止入口不因这项来源检查被禁止。能力库版本或插件改配不能顺带采纳新的用户 hook 文件；候选及原操作保持原来源版本。恢复原文件后可以重新核对，本片实际验证了明确冷恢复原 ID/原历史。

执行配置快照新增“Codex 用户 hook 文件版本”展开区，显示原路径、内容标识、权限或“记录时不存在”，旧记录显示未记录。仅展示文件版本，不宣称原生已加载或已执行；来源核对按钮仍是只读操作。

原先 `config/read` 与已选插件包检查没有绑定这份继承文件。最初原生复现中，用户 hook JSON 的文件摘要和原生 `definition_hash` 均变化，Runtime 仍返回 `execution_clear=true`；新的接受条件失败。修复在 Application 捕获/核对、持久 Scope、Runtime 派发/观察及恢复配置指纹中使用同一份原文件版本。

文件读取复用原有 Claude 来源读取行为，提取为 `FileSource`，保留 `ClaudeSettingsSource` 类型别名和原诊断文字。普通文件上限 2 MiB，读取前后检查身份、大小、权限、修改时间与解析路径；FIFO、目录、过大文件和损坏链接不会作为完整来源。新指纹字段为可省略字段，旧 nil 绑定不因新增空字段改变序列化。

## 实际验证

| 验证 | 结果及范围 |
|---|---|
| 原生复现与修复 | 初始 6 项中“漂移撤下就绪”失败；修复后的同入口通过。没有把空清单或独立文件 hash 当作加载证明。 |
| 最终原生与 SQLite | **19 项通过**：一轮本地连接失败输入明确取消后，修改用户 hook；显示漂移、拒绝输入，重开控制台/后台仍保留原版本并拒绝 resume，加载清单为空；恢复原文件并明确 resume 后，同一原生 ID 进入加载清单，完整单轮历史逐字节保持，当前能力重新核对。 |
| 既有真实恢复回归 | `TestCodexNativeOriginalConfigurationRecoveryAcknowledgedRestart` **108 项通过**，覆盖新 Scope 字段进入原 ID 改配/恢复及已确认回执跨重启核对。 |
| 文件与派发边界 | Runtime 12 类文件/绑定变化、6 种派发入口拒绝；Application 持久化、缺失文件、旧记录、Scope 损坏与候选不升级等 5 类通过。 |
| 相关检查 | App/Console 全包 race 通过；最终新增 App 定向 race 通过；Codex 与相关 Claude Runtime race 通过；两种 native 标签 vet 通过；`make dev-build`（含 Web typecheck）通过。 |
| 浏览器 | **16 项通过**，回放真实 Application/Handler 响应，检查版本/诊断/旧摘要保留、非空草稿不能发送、恢复后的来源核对和 390 宽布局；旧记录/缺失文件是另外明确构造的渲染样本。只有 GET，没有 CLI 或命令写入。 |
| 独立审计 | **33 项通过**，从原文件字节、协议、历史、加载清单和 Handler 响应重新核对；不以新测试的 pass 字段代替关键事实。 |

最终原生目录 `/tmp/netcap-w0209-codex-hook-sources-2059348149`；恢复回归 `/tmp/netcap-w0208-combined-resources-1315886961`。固定原生 server 0.158.0、proxy 0.157.1，全部使用自有 profile/状态/插件。唯一初始输入访问的是不可用 `http://127.0.0.1:9/v1` 并明确 interrupt，没有成功模型回复、MCP 工具或 hook 执行；检查发现漂移之后没有新输入。

## 保留的中间失败与证据边界

早期零输入样本只证明创建与观察。一次冷恢复测试只等待持久旧 idle 状态，虽返回通过，未证明原生已经重新加载；增强为等待新观察与完整加载清单后，暴露 `no rollout found for thread id`。固定 CLI 对该零输入线程没有可恢复日志。最终增加一轮真实、明确取消的初始输入后才完成冷恢复验收；原先的不足样本保留在 `stale-cold-observation`，不能纳入最终成功计数。

扩展测试曾把协议 map 当结构体访问，编译失败已修正并保存。浏览器第一次在移动端未打开现有“详情”面板，scroll 检查超时；修正测试操作后通过，没有为此修改产品布局。首次 fixture 的 favicon 404 属于未映射资源，最终回放明确处理。

[原生结果](W02-09-codex-user-hooks/native/result.json.gz)、[原始目录列表](W02-09-codex-user-hooks/original-directories.json)、[独立审计](W02-09-codex-user-hooks/audit.json)、[浏览器结果](W02-09-codex-user-hooks/browser-final.json)、[桌面截图](W02-09-codex-user-hooks/initial-1440.png)及[移动截图](W02-09-codex-user-hooks/drift-390.png)已归档。协议中宿主名和安装身份脱敏，不复制凭据、SQLite 或整个 profile。hook 原文件仅为本测试的无害固定内容。

## 复跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app -run '^TestCodexUserHook' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(Codex|ClaudeLocal|ClaudeSettings)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags 'nativecodex nativeclaude' ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags nativecodex ./internal/runtime \
  -run '^TestCodexNative(InheritedHookSourceDrift|OriginalConfigurationRecoveryAcknowledgedRestart)$' -count=1 -v
python3 goals_W/evidence/W02-09-codex-user-hooks/audit.py goals_W/evidence/W02-09-codex-user-hooks
```

浏览器模板为 `tests/codex_user_hooks_browser.js`，用归档 Handler 响应替换 `__FIXTURE__`，当前 Web 构建路径映射替换 `__ASSETS__`。沿用[原生 hook 来源契约](W02-08-codex-hook-catalog.md)及此前官方资料，不重复受限的在线请求。

开发构建 SHA-256：`00b42c820b3130aa5156dbc75dbbd69366feb69023ea431c07532b6832eb183e`。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。4642 个旧证据文件与 Claude bridge 内容不变。差异来自本片前置备份，见 [source.diff](W02-09-codex-user-hooks/source.diff)、[manifest](W02-09-codex-user-hooks/manifest.json)和[收尾](W02-09-codex-user-hooks/cleanup.json)。自有进程和浏览器已关闭，所有测试句柄已收取，未提交或发布。

## 未完成范围

只绑定 profile 的默认用户 `hooks.json`，不覆盖项目/受管 hook 文件、已安装插件和引用脚本，不验证所有 JSON 的原生语义，不冻结原生运行中的文件读取。显式采纳用户 hook 新版本、受管规则与来源集合完整性仍需后片；旧快照未记录的来源也仍不可声称固定。

下一步核对项目 hook 来源路径/优先级与原 ID 采纳边界，再扩展有效来源范围。W02.07/W02.08 的实际方法/Gate、通用/异步和成功 hook 生命周期、安装来源控制、共享后台/生产 serve/proxy、输入独占、Windows 与五项既有 WSL 请求保持原要求。此片不构成 W02.09 或 W 系列整体验收通过。
