# W02.09 · Codex 原 ID 明确采纳 hook 文件版本

2026-10-06。完成用户/项目 hook 文件及项目来源集合的显式采纳切片，W02.07/08/09 仍为 `in_progress`，9/40 done，Goal active。固定 app-server 0.158.0、CLI 0.157.1，复用生产 Application/SQLite/Runtime/Handler 及现有改配与恢复事务。

## 行为与边界

“采纳当前用户/项目 hook 文件版本”默认不勾选。预览列出原文件与目标文件的摘要、权限、解析路径及配置层/原生发现关系。新增来源和不存在的文件分开呈现。保存仅持久化候选；准备只读核对原线程、历史、全部执行范围和原连接的配置建立依据，保持暂停。明确应用才取消当前连接订阅，按原 ID 恢复并核对候选；成功后晋升快照，原版本留在历史中。

采纳保留原生信任配置，不自动信任新 hash。本文的调用样本在创建会话前已对测试的两个精确 key/hash 授信；v1 文件尚未包含第二个 key，v2 增加它。因此可观察到 v1 → v1+v2，且整个配置文件及信任均未改动。它不是自动授权新 hook 的功能。

普通输入仍核对原文件版本；采纳时的临时目标只存在 Runtime 内部，不接受 JSON，也不持久化或放行普通输入。仅文件漂移不会抹掉“该连接曾建立原配置”的依据，但该依据不能单独恢复输入就绪。准备及应用同时复核候选文件/来源与其他有效配置。原连接依据缺失、其他配置变化、候选再次漂移、身份不符或仍有执行均拒绝。当前不保证断开原连接后直接应用保存的候选；需先取得可证明的原配置边界。

原生应用回执丢失时保留旧快照、失败候选与 unknown，不重复应用。明确恢复原配置需要旧文件确实还原、原线程未加载、原历史与配置核对通过；本功能不自动写回旧文件。恢复操作使用原文件版本观察，修复了此前误拿失败候选版本检查空闲状态的问题。明确恢复原 ID 后，实际调用重新只出现 v1。

## 固定原生加载契约

新增 [原生探针](../../tests/codex_hook_file_lifecycle_probe.py)。[目录映射](W02-09-codex-hook-adoption/original-directories.json)中的 `lifecycle-first` 为探索记录；`lifecycle` 为含严格预期与退出码的最终记录。

| 操作 | 实际调用版本 |
|---|---|
| 创建后第一轮 | 用户/项目均为 v1 |
| 文件改为 v2，无 resume | 仍为 v1 |
| 热 resume，提供 v2 信任 | 仍为 v1 |
| 退订后 resume，保留 v1 信任 | 新定义未获相符信任，无 hook 调用 |
| 退订后 resume，明确提供精确 v2 信任 | 用户/项目均为 v2 |
| 另一已加载线程 | 仍为其 v1 |
| 冷恢复省略线程覆盖 | 无 hook 调用 |
| 冷恢复明确提供 v2 覆盖 | v2 |
| 删除文件，或将文件还原 v1，未退订 | 已加载线程仍为缓存的 v2 |
| 退订后按原定义/信任恢复 | v1 |

目录 `hooks/list` 的变化不能证明当前线程已采用新定义。该区别也是不能直接替换快照 hash 的原因。既有 [OpenAI Docs 摘录](W02-08-codex-hook-trust/docs.txt)只支持“信任绑定当前 hash”，没有规定上述缓存/加载边界；这些结论来自固定二进制实测。

## 验证结果

| 层次 | 本轮结果 |
|---|---|
| 原生文件生命周期 | 77 项通过；11 轮输入，逐轮显式中止，不可用本地供应商 |
| 生产用户文件采纳 | 9 项通过：保存/准备不覆盖、原 ID 晋升、实际新增调用、冷恢复 |
| 生产项目文件采纳 | 9 项通过；同样逐轮核对实际版本归属 |
| 原先缺失的项目目录采纳 | 9 项通过：空来源集合 → 新来源，应用/冷恢复后的实际调用 |
| 原生应用回执丢失及明确恢复 | 12 项通过；单次派发、幂等、旧文件未恢复时阻止恢复、原版本实际调用 |
| 既有已确认恢复跨重启回归 | 108 项通过 |
| App/Console race | 通过；来源专测及混合插件候选再补测通过 |
| Runtime 相关 race | 通过；新增 7 类原连接/身份/其他配置/候选漂移边界 |
| 带标签 internal/cmd vet | 通过 |
| Web 类型检查、构建与开发 Go 构建 | 通过 |
| 浏览器 | 22 项通过；真实 Handler 快照/预览，命令与 SSE 回放 |
| 独立核对 | 172 项通过，包含原生调用、信任来源、冷恢复、丢回执、原始文件摘要 |

测试 hook 写入自有 marker 后故意超过 1 秒期限；所有完成事件均为 `failed` 超时。它们证明加载版本与调用归属，**不证明 hook 成功执行、真实模型效果或方法 Gate**。没有启动 HTTP listener、调用真实供应商、修改用户配置或共享后台。

[桌面预览](W02-09-codex-hook-adoption/preview-1440.png)、[390px 准备状态](W02-09-codex-hook-adoption/prepared-390.png)覆盖明确选择、原/新摘要、来源关系、保持暂停、原 ID 和新的快照。浏览器只回放三种预期命令，没有向原生发送输入。勾选项布局在截图检查后局部修正，22 项已复跑。

保留原始失败：初版单元夹具只接受 `includeLayers:true`，不能用于完整配置指纹；首轮原生断言误把并行 hook 顺序作为契约，并过早使用冷恢复旧观察；原生恢复实际因候选文件检查而被拒后修复。独立审计最初把 `SourceStamp` 的“文件条目/权限/内容”摘要当成字节 SHA，随后按该契约及实际原生 user layer/完整 TOML 联合核对，未放宽产品校验。

## 重跑与产物

```bash
python3 tests/codex_hook_file_lifecycle_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/... ./cmd/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags nativecodex ./internal/runtime -run '^TestCodexNativeHookSourceAdoption$' -count=1 -v
```

持久恢复回归同环境使用 `^TestCodexNativeOriginalConfigurationRecoveryAcknowledgedRestart$`。浏览器入口 [脚本](../../tests/codex_hook_source_adoption_browser.js)填入真实 `browser-responses.json` 与当前 Web 资源路径，经配置的 Playwright MCP 执行。

开发二进制 SHA-256：`f2c875840279ab47c5b942b5d89813954c25923d535a2bfe67d35911a5298eea`。旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。最终 [源码差异](W02-09-codex-hook-adoption/source.diff)、[命令结果](W02-09-codex-hook-adoption/command-results.json)、[独立审计](W02-09-codex-hook-adoption/independent-audit.json)、[manifest](W02-09-codex-hook-adoption/manifest.json)。4781 个旧证据与 Claude bridge 保持不变，没有提交或推送；自有执行器和浏览器已收尾。

## 尚未完成

混合插件候选在 App 层验证，全部混合资源的原生联合采纳、worktree 原生采纳矩阵、原连接已丢失后的候选应用、准备/提交崩溃窗口尚未完整验收。受管文件、引用脚本及成功/通用/异步 hook、完整有效配置与四配置联合验收继续保留。真实模型/方法/Gate、安装来源、共享后台/生产 serve/proxy、输入独占、Windows 和五项既有 WSL 请求也未被本轮结果替代。

下一片继续用户/项目来源采纳的混合资源与原连接丢失边界，复用相同原 ID 事务；不凭保存的快照推定已加载，也不静默授信或回滚。
