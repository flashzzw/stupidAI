# W02.08 · Codex 本地 MCP 插件的独立版本副本

2026-10-04。新建 Codex 会话可选择仅含 MCP 的本地 CLI 插件整包。界面、Application/执行快照、Runtime、原生线程及 SQLite 贯通；资源版本进入独立原生缓存命名空间，同一资产更新不改变已保存执行。此片未完成全部来源、插件改配或 Skill/hooks 支持；W02.08 in_progress，9/40 done，Goal active。

## 原生对照决定的实现路径

前片已证实当前磁盘内容不一定是后台采用的定义。本片进一步在隔离目录中对照：

- `plugin/reconcile` 返回空变更，`config/mcpServer/reload` 返回成功，但两者之后的新线程仍采用旧定义；它们不提供可依赖的插件加载版本确认。
- 显式 `plugin/install` 重装同名同版本后，**已有线程也改为新定义**。不能把这个全局操作用于单个会话的版本替换。
- 未安装插件只有线程 enabled 覆盖时不加载；准备缓存副本后，线程 enabled 覆盖可加载。没有全局市场文件也成立，默认线程仍不加载该副本。
- 分别准备 `fixture@netcap-a` / `fixture@netcap-b` 缓存，两个线程可使用不同定义，原线程不变；另一个默认/禁用线程不加载，原 ID 冷恢复保留其选择。

原始探索保存在 [exploration](W02-08-codex-scoped-plugin/exploration/scoped-cache/result.json) 及同级目录。探索脚本复用上一片框架，其 `expectations_met` 主要覆盖原有检查/进程退出，不能当作上述每个新结论的断言计数；本片最终生产原生测试对实际加载版本、隔离和恢复另设断言。

## 产品行为与边界

1. Application 读取 `.codex-plugin/plugin.json` 的本地目录，保存资产版本、原生声明版本、完整文件/目录权限及摘要、MCP 组件。新建时选择 enabled / disabled；单纯引用不加载。当前不接入 portable 根 manifest、Skill/hooks/commands/agents 等其他组件，发现未核对组件或重复定义时拒绝创建。
2. 缓存命名空间由工作台 Session、资产 ID 和来源摘要派生，保存完整 `native_source` 与 `cache_path`。路径在绑定 profile 的 `plugins/cache/netcap-…/<name>/<version>/` 下；原始共享来源仍单独保存在快照中。不同会话和不同内容不复用同一原生缓存键。
3. 快照保存先于 Runtime。显式新建操作才从已核对资源准备缓存：使用受目录约束的文件操作、临时目录和原子重命名，保留文件/目录权限，拒绝链接、特殊文件、额外内容与越界路径。只写自有缓存目录，不改 profile 的 config.toml、已有安装缓存或项目市场文件；不调用原生 install、配置写入或全局 reload。
4. 线程创建/恢复传 `plugins.<完整来源 ID>.enabled`，保留模型、路由、权限及其他配置覆盖。原生插件功能未启用、同名继承插件、同名用户 MCP（即使该用户定义 disabled）、所选组件冲突时拒绝。初次失败可能留下尚未启用的自有副本，不将其加入全局默认配置。
5. 观察及输入/审批回复前核对固定缓存的完整文件集合、类型、权限和内容，以及线程 MCP 的完整来源、状态与工具清单；额外同源组件也会阻止一致性。禁用通过完整清单中的缺席核对，所显示来源键来自保存选择，不伪称原生返回了一个 disabled 服务条目。缓存缺失或变化不会被观察/恢复自动修复。
6. 原生插件 ID、MCP 资产归属与历史来源分离沿用前片保护。界面显示共享来源和本次加载副本；断连后显示待核对。Codex 不显示尚未接通的插件改配承诺，Claude 原有改配入口保留。

这里固定的是已登记插件资源。原生进程仍可能依赖外部可执行文件、环境或动态资源；快照的 `recorded_sources_only` 范围保持。完整权限、真实工具结果返回模型、方法 Gate 等不能由 initialize/list 或缓存摘要推定。当前生产后台入口仍由既有 peer 校验限定到已验证的 Codex 0.158.0。

## 分层验证

| 层次 | 实际结果 |
|---|---|
| Application | 启用/禁用的新建快照与独立来源绑定，5 类未核对结构拒绝；相关执行/Claude 插件回归通过 |
| Runtime | 14 场景通过：显式创建先备缓存、来源/清单投影、5 类缓存损坏阻止 send/respond 且不修复、3 类继承冲突拒绝、4 类禁用完整性检查 |
| 真实生产链路 | [最终 28 项](W02-08-codex-scoped-plugin/final/result.json)通过：Native→Runtime→Application/SQLite→Handler，原生启用/禁用、完整 ID、实际定义 v1/v2、无关服务保留、同一资产资源库更新不升级旧执行、三个会话的持久绑定与 Runtime 原 ID 冷恢复 |
| Codex 界面 | [18 项](W02-08-codex-scoped-plugin/browser.json)通过：当前构建回放最终真实 Handler 响应、来源与缓存、版本切换、启/禁、选择序列化、移动布局、断连及无虚构使用；HTTP/SSE/创建回执为受控传输 |
| Claude 回归 | [15 项](W02-08-codex-scoped-plugin/claude-browser.json)通过，使用已验收 WSL 的真实工具/hook 历史回放共用当前界面；不是新的原生调用 |
| 竞争/静态/构建 | app/console 全包 race、Codex/MCP/ClaudePlugin Runtime 定向 race、相关包 nativecodex/nativeclaude 标签 vet、`make dev-build`（含前端类型检查）通过 |

最终原生目录 `/tmp/netcap-w0208-codex-scoped-plugin-2818295374`；初始 27 项目录 `/tmp/netcap-w0208-codex-scoped-plugin-2491007530` 保留，但不与最终计数累加。最终补验的是**同一个资产 ID**升级资源库版本；两个会话仍各自保留原生定义。三个自有后台退出 0，零模型输入、零 tools/call、零 hook；冷恢复前通过测试接口注入明确标记的合成历史以物化线程。它证明 Runtime 真正关闭/重启后台后按原 ID 恢复初始化，不证明带成功模型/工具历史的恢复。SQLite 重开读取另行核对；生产 Unix proxy/peer 与 Windows 仍不由测试 stdio 传输证明。

红灯保留：Application 原来只接受 Claude 插件；Runtime 原来没有准备缓存或线程覆盖，且没有插件观察证据；旧界面误显示 `@inline`。对应修复后通过。一次浏览器资源脚本生成的 Python 括号错误导致文件缺失，修正辅助脚本后重跑；不是产品构建故障。截图已检查，浏览器已关闭。

## 重跑、产物及后续

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test ./internal/app -run '^Test(CodexPlugin|ClaudePlugin|.*Execution)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test ./internal/runtime -run '^TestCodexPlugin' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(Codex|MCP|ClaudePlugin)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags 'nativecodex nativeclaude' ./internal/runtime ./internal/app ./internal/console ./internal/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags nativecodex ./internal/runtime -run '^TestCodexNativeScopedPluginSelection$' -count=1 -v
```

[浏览器模板](../../tests/codex_plugin_selection_browser.js)注入附件 final/browser-responses.json 及当前 web/dist 资源映射。附件保留[桌面](W02-08-codex-scoped-plugin/enabled-1440.png)/[移动](W02-08-codex-scoped-plugin/enabled-390.png)截图、原始协议、[差异](W02-08-codex-scoped-plugin/changes.diff)、[审计](W02-08-codex-scoped-plugin/audit.json)与[归档清单](W02-08-codex-scoped-plugin/manifest.json)。原生 JSON 中宿主名称及安装标识脱敏。

开发二进制 SHA256：`661379af55957dde429a91c4eb0b783f6a5ea1263d2f586ae24f38d74f57d0ac`；旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。1443 个既有证据未改；未提交或对外发布。

下一片继续 Codex 插件原 ID 版本事务与完整组件选择；使用独立新缓存键，先核对卸载/恢复是否会保留旧线程覆盖，再沿既有候选事务接入。受管/已安装来源、Skill/hooks、实际调用/权限、成功历史、Codex hook 超时/异步、Windows/proxy 和先前待收取的 WSL 结果均保留硬要求。本片不将 W02.08 整包或 W 系列标为完成。
