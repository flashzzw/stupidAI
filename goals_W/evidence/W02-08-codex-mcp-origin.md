# W02.08 · Codex MCP 插件来源与历史归属

2026-10-03。执行详情保留完整原生插件 ID，历史成功调用按服务及插件来源区分；插件同名服务不能冒充所选配置文件中的 MCP。此片是来源保护基础，尚未交付完整 Codex 插件选择或缓存版本绑定。W02.08 in_progress，9/40 done，Goal active。

## 产品行为

- 原生 `mcpServerStatus/list` 和历史 `mcpToolCall` 的 `pluginId` 贯通 Runtime、Application/SQLite、Handler 与界面。显式 `null` 表示已报告非插件来源；字段缺失、空值或无效类型保持 unknown，不将旧协议数据默认为非插件。
- 所选配置文件 MCP 只有在来源已报告为非插件且状态/工具清单一致时才标 verified。返回插件来源或来源不明时，保留诊断并阻止新输入。`configuration` 展示为“原生配置”，不猜用户/项目的具体层级。
- 成功调用按 `(server, pluginId, known)` 分开。当前资产只接收已核对的对应来源；其他插件和未报告来源的旧调用保留在“历史 MCP 来源”，不绑定当前资产，也不表示当前加载或当前工具清单为空。
- 继承的插件 MCP 显示完整 ID；来源一致不证明缓存内容或资产版本一致。原来没有显式 MCP 选择时的早返回仍保留，本片没有扩展成通用插件库存或选配入口。

## 原生契约与限制

固定 Codex 后台 0.158.0 的[正式探针](W02-08-codex-mcp-origin/probe/result.json) 21 项通过，最终目录 `/tmp/netcap-w0208-codex-plugin-mcp-sources-440om1lu`。仅运行自有 stdio MCP 的 initialize/list，零模型输入与 tools/call。

1. 插件 MCP 返回完整 `fixture@owned`；逐线程 `plugins.fixture@owned.enabled=false` 排除该插件 MCP，保留无关配置服务和另一线程，不写 profile。此项是原生实验，尚未接入生产插件选择。
2. 同名用户 MCP 遮蔽插件 MCP；禁用用户定义后保留 disabled 用户条目，不回退插件。不能只按服务器名称推断来源。
3. 源目录权限 0700，安装后的缓存目录 0755；文件内容及文件可执行权限保持。源目录完整摘要不能直接当作缓存目录摘要。
4. `plugin/installed.localVersion` 可显示源版本 2，而新线程仍使用缓存配置标记 1；显式安装后才使用标记 2。同版本重装改写同一缓存路径的 `.mcp.json`，新线程使用标记 3。版本标签或缓存路径都不能单独证明内容固定。
5. 探针的服务器脚本使用绝对源路径，标记通过缓存配置的 args 传入；这里证明的是运输定义来自缓存，不能宣称脚本本体从缓存执行。MCP 子进程 cwd 为项目目录，未收到 hook 的 `PLUGIN_ROOT` / `CLAUDE_PLUGIN_ROOT` / `CODEX_PLUGIN_ROOT` 环境变量。相关 ROOT/相对路径试验失败记录保留于 exploration，不据此宣称原生 bug。

契约依据沿用已保存的固定版本 schema（`McpServerStatus.ts` / `ThreadItem.ts`）及官方插件打包文档。文档的根目录环境变量针对 hook commands。`ThreadStartResponse` / `ThreadResumeResponse` 注释明确 `disabledPluginIds` 目前只保存而未过滤能力，所以探针没有将该字段当作排除证明。生产没有新增开发中的 `plugin/install` / `plugin/read` 管理调用。

## 分层验证

| 层次 | 实际结果 |
|---|---|
| Runtime 公开 Observe | 8 场景通过：插件冒充、6 种来源字段、5 类历史来源分离；相关 MCP 回归通过 |
| 原生生产链路 | [17 项](W02-08-codex-mcp-origin/native-result.json)通过，真实 Native→Runtime→Application/SQLite→Handler，两个会话分别启用/禁用配置 MCP，继承插件服务保持连接、ID 保留、没有虚构 usage |
| Codex 界面 | [18 项](W02-08-codex-mcp-origin/browser.json)通过：真实 Application 清单回放、完整 ID、历史来源分区、同名多来源、切换、移动布局和只读请求；历史行是明确合成的契约样本 |
| Claude 共用界面回归 | [15 项](W02-08-codex-mcp-origin/claude-browser.json)通过，使用已验收 WSL `/tmp/netcap-console-bp2rp6xu` 的真实工具/hook 历史回放当前构建；不是新的原生调用验收 |
| 竞争、静态与构建 | app/console 全包 race、Codex/MCP Runtime 定向 race、相关包带 nativecodex/nativeclaude 标签 vet、`make dev-build`（含前端类型检查）通过 |

生产原生最终目录 `/tmp/netcap-w0208-codex-mcp-origins-3394657015`，两个自有原生进程均退出 0，零模型/工具调用。SQLite 重开读取保持来源，但没有执行原生冷恢复。使用测试专用 stdio 传输，生产 Unix proxy/peer 与 Windows 不由此证明。浏览器已关闭。

红灯保留在 logs：原实现丢失插件 ID、接受缺失来源、把旧插件及缺字段调用归给当前同名资产，旧界面也不显示 ID。历史测试最初只在 turns/list 放入 items，而真实读取使用 thread/read(includeTurns:true)，导致一度出现 fixture 错误；修正 fixture 后重新对基线运行得到 `history-corrected-red.log`，最终 `history-final-green.log` 通过。早期 `history-green.log` 实为失败，未计作通过。最终探针还使检查失败与进程退出码一致。

## 重跑与产物

```bash
python3 tests/codex_plugin_mcp_source_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test ./internal/runtime -run '^Test(CodexMCPOrigin|MCPEvidence)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^Test(Codex|MCP)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags 'nativecodex nativeclaude' ./internal/runtime ./internal/app ./internal/console ./internal/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags nativecodex ./internal/runtime -run '^TestCodexNativeMCPOrigins$' -count=1 -v
```

浏览器模板为 [Codex](../../tests/codex_mcp_origin_browser.js) 和 [Claude](../../tests/claude_plugin_execution_browser.js)，分别注入附件 native-browser-responses.json 和上一片 WSL 响应，以及当前 web/dist 资源映射；截图路径使用本片独立目录。附件保留[桌面](W02-08-codex-mcp-origin/native-1440.png)/[移动](W02-08-codex-mcp-origin/history-390.png)截图、原生协议、试验失败、[差异](W02-08-codex-mcp-origin/changes.diff)、[审计](W02-08-codex-mcp-origin/audit.json)和[归档清单](W02-08-codex-mcp-origin/manifest.json)。原生 JSON 中宿主名称/安装标识已脱敏。

本片开发二进制 SHA256：`69ad33ebc6ad431dabbd0a676091ce2ee1e38168f34a64b1f5701471af44358a`；旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。1335 个旧证据保持不变，未提交或对外发布。

## 剩余工作

下一片继续实际缓存/资产绑定和生产插件选配。完整来源、组件排除、原生调用/权限、Skill 方法、带成功历史恢复、Codex hook 成功执行与异步可观测性、Windows/生产 proxy 均保留。Claude 完整改配及先前 Skill 探针仍待既有终端结果，不重复请求。不能用本片初始化与清单检查替代这些条件。
