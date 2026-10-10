# W02.08 · Codex hook 目录来源清单

2026-10-03。生产执行详情新增 Codex hook 只读入口，贯通 Runtime→Application/SQLite→Handler→当前构建界面。W02.08 仍为 in_progress，整体 9/40 done，Goal active。

## 行为与边界

保存的普通 Codex 会话可手动读取用户、项目和插件 hook 来源，保留原生 key、sourcePath、完整 pluginId、enabled、trustStatus、isManaged、async、timeoutSec 和带 `sha256:` 前缀的 currentHash。来源路径可指向真实安装缓存；不按同名插件猜资产，也不根据源预览版本推断缓存版本。共享 HookCatalog 组件保留 Claude 当前查询菜单及其策略展示。

`POST /api/sessions/{id}/hooks/catalog` 仍只接受空对象；Codex 只调用 `hooks/list` 并传保存的 cwd。此原生接口没有 thread ID 参数，catalog 的 NativeID 仅关联保存会话，scope 固定为 directory，complete/policy_reported 均为 false。读前后核对保存绑定和输入归属；读失败、会话切换或断连清空旧展示。项目未信任、策略过滤或解析失败都可能使来源缺席；空清单不表示不存在 hook。

命令正文不解码到产品响应，原始 RPC 错误和警告不转发。警告保留数量，错误仅保留合格路径和固定说明；校验完整数组、目录、2048 项上限、必要布尔/数值、唯一键、绝对路径及 hash 格式，拒绝不完整结果。timeout 允许原生 schema 支持的 0，拒绝负数及超出安全整数的值。未知来源/信任字符串保留原值。此入口不执行、授信、安装、启停或启动线程，也不报告完整策略。

## 实际验证

| 层次 | 结果与证据 |
|---|---|
| Runtime 公开接口 | 20 场景通过：原生来源/信任保留、禁用/空结果、未知枚举、缺数组/必要字段、重复键/cwd、路径/hash/timeout、失败脱敏和错误 scope；[测试源码](../../internal/runtime/codex_hook_catalog_test.go) |
| Application 公开接口 | 新增 Codex 8 场景通过：来源/身份/cwd/scope/policy/completeness、读取期间接管；保留 Claude 6 场景；[测试源码](../../internal/app/hook_catalog_test.go) |
| 固定 0.158.0 原生 stdio | 最终 [15 项](W02-08-codex-hook-catalog/native-result.json)通过：实际用户/项目/插件缓存来源、untrusted 保留、未信任项目缺席、损坏配置 warning 与项目来源省略、修复恢复、profile/应用状态不被读取修改、Handler 目录绑定 |
| Codex 浏览器 | 当前构建、真实 Application/Handler 回执受控 HTTP/SSE 回放 [19 项](W02-08-codex-hook-catalog/browser.json)通过；含缓存路径/插件 ID/hash、提示与修复、失败/切换/断连清空、移动布局、无执行路由；409 为合成传输故障 |
| Claude 回归 | 当前生产 Application/SDK 原生 [18 项](W02-08-codex-hook-catalog/claude-native-result.json)、当前界面 [23 项](W02-08-codex-hook-catalog/claude-browser.json)通过；策略异常等仍为受控展示样本，未冒充真实受管策略 |
| 检查与构建 | app/console 全包 race、Codex/Claude hook 与 Codex Skill 定向 race、三个相关包带 nativecodex/nativeclaude 标签 vet、`make dev-build`（含前端类型检查）全部通过；日志见附件 logs |

原生入口复用已有隔离 stdio fixture，仅测试配置安装一个本地插件；没有将开发中的 plugin/install 接入产品。最初 hooks/list 发现没有创建线程；随后真实 Application 创建两个隔离会话用于 Handler/界面核对，不能写成全程零线程。全程无 turn/start、模型输入、MCP 或 hook 执行；三个自有原生进程均退出 0。原生临时目录 PATH helper 警告及未信任项目提示保留，未修改用户 profile、共享后台或真实供应商。

原始目录：Codex `/tmp/netcap-w0208-codex-hooks-1776951060`；Claude `/tmp/netcap-w0207-claude-app-3395222177`。浏览器响应分别归档为 [Codex](W02-08-codex-hook-catalog/browser-responses.json) 与 [Claude](W02-08-codex-hook-catalog/claude-browser-responses.json)；模板为 [Codex](../../tests/codex_hook_catalog_browser.js) 与 [Claude](../../tests/claude_hook_catalog_browser.js)。截图：[桌面](W02-08-codex-hook-catalog/catalog-1440.png)、[移动](W02-08-codex-hook-catalog/catalog-390.png)、[Claude 回归](W02-08-codex-hook-catalog/claude-native-1440.png)。

## 失败记录与修正

- Application 首次误用不存在的 testConfig 导致编译失败，不算行为红灯；改为已有 fixtureConfig 后，原入口拒绝 Codex 的真实红灯保留。Runtime 原先没有 HookCatalogReader 的红灯、旧界面没有 Codex 入口的超时同样保留。
- 首次原生结果 `/tmp/netcap-w0208-codex-hooks-1091529918` 因产品错误沿用 Claude 裸 64hex 校验而失败；原生实际格式为 `sha256:<64hex>`，现保留前缀并校验后半部分。
- 第二次 `/tmp/netcap-w0208-codex-hooks-95099999` 是测试把损坏配置错误地预期为 errors；原生返回 warning_count=1，并省略项目来源，产品已正确保留 warning。修正测试预期，未改成虚构的 errors。
- `/tmp/netcap-w0208-codex-hooks-2385056892` 是首次 14 项通过；加入 profile 不变核对后最终为 15 项。早期浏览器 17 项不与最终 19 项累计。上述原生结果与日志均保留于附件。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags nativecodex ./internal/runtime -run '^TestCodexNativeHookCatalog$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(CodexHookCatalog|ClaudeHookCatalog|CodexSkillDiscovery)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags 'nativecodex nativeclaude' ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

本片 `.dev/bin/netcap` SHA256：`7397ec245838e4928e877722b2b9f8bed33a8388ca090d4e27561a29b4f9305c`。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。以切片前备份生成 [差异](W02-08-codex-hook-catalog/changes.diff)，包括 ClaudeHookCatalog 删除和 HookCatalog 新增；[manifest](W02-08-codex-hook-catalog/manifest.json)记录归档时源码/构建/证据哈希。1183 个既有证据文件未修改。未提交或对外发布。

## 剩余要求

生产 Unix proxy/peer、Windows 不由本片 stdio 验证；完整受管策略、逐线程插件选择/排除、实际工具/hook 权限、缓存与资产绑定及带模型历史恢复继续保留。Claude 完整改配和先前 Skill 探针仍待已请求的终端结果。本片只完成可独立验证的来源解释，下一片继续实际缓存/资产绑定和排除证据，不把发现、安装、信任、加载和使用合并成一个状态。
