# W02.09 · 来源检查覆盖与原生观察区分

日期：2026-10-06。范围是已保存来源的只读核对与展示；W02.09 仍为 in_progress，W 系列 9/40 done。本片没有完成有效配置合并、权限冲突或原生四配置联合验收。

## 用户可见变化

“核对当前来源”现在按主配置、自定义 settings、Claude 项目/本地设置、Codex 用户/项目 hook、显式能力引用分组。每组区分已记录、快照未记录、未显式选择与尚未覆盖，可以展开查看全部文件检查结果，变化组自动展开。已记录只说明记录存在；格式错误、文件缺失或漂移仍显示失败。

旧快照缺失字段不补造来源；记录的空项目 hook 集合与缺少记录不同。`unchanged` 仍仅表示已记录来源及绑定未发现变化。完整继承配置、来源外依赖、账号动态能力与权限合并继续列为未覆盖。来源检查时间与最近保存的原生观察单独显示，原生观察完整也不代表完整有效配置。旧接口没有覆盖字段、未知状态及版本已替换的响应均不会被解释为检查成功。

`GET /api/sessions/{id}/execution` 新增 `source_coverage`，原有 `sources` 新增稳定 `group`。读取沿用现有文件检查，不刷新原生状态、不改快照、不释放暂停。没有改变原生合并规则、输入许可、改配或恢复契约。`CONSOLE.md` 同步当前来源范围，并修正旧 MCP 段落对 Skill/CLI 插件能力的过时表述。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| Application 状态 | `TestExecutionSourceCoverageFourBindings` 通过：两份 Codex、两份 Claude 配置共用资产，专属文件变化只影响对应配置，共享目录资源变化影响四份；关闭并读取持久状态，保持旧原生观察、hold 与快照，无派发或持久写入。此处 Runtime 是状态夹具，不是四 CLI 原生并发。 |
| 旧记录与无效记录 | `TestExecutionSourceCoverageLegacyAndInvalidRecords` 的 Codex/Claude 两例通过：未知格式报漂移；缺少字段显示未记录；历史 coverage 标签不扩大本读取范围；无快照保留 unrecorded。 |
| 真实 Codex + Handler | 最终 **24 项通过**。固定 0.158.0 server / 0.157.1 proxy，隔离 stdio 创建原线程，关闭连接后读取真实 Handler 的原始/新增用户 hook 文件/恢复三组响应；原 ID、快照、原生观察均保留，原始 trace 无新增请求、没有模型输入。 |
| 浏览器 | 最终 **24 项通过**。原生 Handler 三组响应用于桌面/窄屏回放；旧快照、旧接口、未知状态和版本冲突使用明确转换的响应夹具。未变化路径、漂移展开、四类缺口、观察边界、断线禁用、无页面异常及仅 GET 均通过。 |
| 回归与构建 | app/console 全包 race 通过；最终相关状态测试、带 nativecodex 标签的产品包 vet、TypeScript/Vite 与开发二进制构建通过。最后只修改缺少记录时的说明，随后重跑相关状态/原生/浏览器/vet/构建。 |

浏览器前两次失败分别是工具 VM 没有 `URL` 和 `structuredClone` 全局函数。测试脚本改为既有 URL 路径解析方式与 JSON 复制后通过；原失败摘要保留。这两次是测试入口兼容问题，未据此更改产品行为。

## 重跑入口与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/... ./cmd/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeExecutionSourceCoverage$' -count=1 -v
```

浏览器入口为 [execution_source_coverage_browser.js](../../tests/execution_source_coverage_browser.js)：将 `__FIXTURE__` 替换为原生测试的 `result.json`，`__ASSETS__` 替换为当前 `web/dist` 路由到绝对文件路径的映射，通过已配置 Playwright MCP 执行；截图输出目录为 `.dev/w0209-source-coverage`。工具使用请求拦截读取实际构建，不启动监听服务。最终原生目录为 `/tmp/netcap-w0209-source-coverage-1480535356`，首次原生目录为 `/tmp/netcap-w0209-source-coverage-1096069222`；工作目录为 `/tmp/netcap-w0209-source-coverage-work`。

归档包含 [原生结果与 trace](W02-09-source-coverage/native-final.json.gz)、[浏览器结果](W02-09-source-coverage/browser-verified.json)、[桌面漂移截图](W02-09-source-coverage/drift-1440.png)、[窄屏旧记录截图](W02-09-source-coverage/legacy-390.png)、[源码差异](W02-09-source-coverage/source.diff)、[产物核对](W02-09-source-coverage/audit.json)与[最终哈希](W02-09-source-coverage/manifest.json)。5514 个旧证据逐文件哈希保持不变；生产 Runtime、Claude bridge 与旧 `bin/netcap` 未改。两次原生运行各一个自有进程正常退出，浏览器已关闭；未提交、推送或调用真实供应商。

## 剩余范围

本片只汇总现有文件检查覆盖，不提供新的原生有效配置读取契约。完整受管来源、来源外脚本/可执行程序、账号动态权限与合并优先级、原生四配置并发和实际方法/Gate 尚未验证。既有 W02.07/08、成功/通用/异步 hook、安装来源、存活共享后台/输入独占、硬崩溃、Windows 与五项待返回 WSL 结果均保留。下一片沿这些真实来源与合并证据缺口推进，复用已验收的改配和恢复契约。
