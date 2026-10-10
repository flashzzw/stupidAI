# W02.06 · OAuth 准备回执重试与来源界面

日期：2026-10-02。本片修复浏览器丢失 OAuth 准备回执后重复创建登录目录的问题，并补齐准备控件及来源标签的受控浏览器验证。W02.06 仍 in_progress，8/40 done，Goal active。

## 原始失败与修复

[修复前记录](W02-06-auth-ui-red.json)使用当前构建的浏览器发起真实 `prepare_mcp_login` 请求，将其原样交给生产 Application/SQLite/Handler。第一次准备已经保存成功后，浏览器传输丢弃响应；再次点击生成不同的 `command_id`，后端按第二个新命令创建第二个登录目录。两份记录绑定相同会话、快照与服务，`same_command_id=false`、`same_directory=false`、目录数为 2。原生输入仍为 0，原生 Operation 仍为 2；问题是准备回执重试语义，不是模型执行重复。

`MCPLoginDetails` 现在按会话/快照/服务在同一标签页的 sessionStorage 中保存待确认命令 ID，收到成功回执才清除。刷新、切换目标再返回和 Application 重启后均能按原 ID 取得既有回执；不同目标独立保留未确认请求。成功之后再次点击仍表示新的准备；只需旧命令时使用只读“查看已准备命令”。新一次准备或读取开始时清除旧命令展示，失败时不会将旧结果误呈现为本次成功。

后端、权限、原生认证与来源解释逻辑未改。只有 `web/src/MCPLoginDetails.tsx` 是生产代码变更；新增 `tests/mcploginui/` 为测试用实际 Handler 传输夹具和浏览器脚本。

## 实际验证

| 范围 | 结果 | 证据 |
|---|---|---|
| 浏览器 + 实际准备请求 | 50 项检查通过，含请求队列断言；两目标各丢一次回执后重启 Application/刷新页面，分别复用原命令 ID 和原目录 | [流程结果](W02-06-auth-ui-browser.json) |
| 绑定与目标 | A/B profile、当前/候选快照不串用；当前目标不能找回候选命令；复制的文本与持久回执完全相同 | 同上及 [Handler 往返](W02-06-auth-ui-exchanges.jsonl) |
| HTTP/SSE 准备边界 | 两种定义均真实创建私有单服务目录；stdio、headersHelper 与来源漂移返回 409，不增加目录；准备未增加原生输入/Operation | 同上 |
| 来源界面 | 32 项展示检查通过，覆盖 11 个已知来源值、未来未知值与缺字段记录；状态/工具权限含义不提升、未知值不透传、旧字段不补造 | [结果](W02-06-auth-ui-sources.json) |
| 真实文件/保存绑定 | 18 项检查通过：A/B 当前快照、候选与原 ID 不变，四目录各仅含目标服务、HTTP/SSE 类型正确、目录/文件仅属主可访问 | [文件检查](W02-06-auth-ui-preparation-files.json) |
| 布局 | OAuth 准备与来源两组各 1440/768/390 宽度，无页面横向溢出；六截图检查可读 | [准备 1440](W02-06-auth-ui-browser-1440.png)、[768](W02-06-auth-ui-browser-768.png)、[390](W02-06-auth-ui-browser-390.png)；[来源 1440](W02-06-auth-ui-sources-1440.png)、[768](W02-06-auth-ui-sources-768.png)、[390](W02-06-auth-ui-sources-390.png) |
| Go 回归 | 2 个现有 MCPLogin 测试及 app/console 全包 race 通过；包含持久失败清理与 shell 参数保真 | [定向](W02-06-auth-ui-app-tests.txt)、[race](W02-06-auth-ui-app-console-race.txt) |
| 工程 | Web typecheck、Vite/Go 开发构建、带标签全包 vet、3 个浏览器脚本语法检查通过 | [构建](W02-06-auth-ui-build.txt)、[vet](W02-06-auth-ui-vet.txt) |

其中完整流程包含：未确认结果不显示命令、目标间隔离、原生创建失败记录仍可准备认证、重启后不另建 Session、只读找回、离线禁用准备/找回按钮、清除失败操作的旧命令，以及来源/历史工具记录的不同含义。最终恰有四份成功准备：B 当前 identity、A 候选 identity、A 当前 identity、A 当前 stream。原生创建次数固定为夹具初始化的 2，输入 0，Operation 2。

第一次修复后运行在剪贴板检查中误用工具进程的 `window`，验证脚本抛 ReferenceError；改为页面上下文读取后，从新夹具完整重跑通过。该中断不是产品故障，也未把局部通过当完整结果。

## 证据边界

- 原生 Runtime 完全受控：A 返回固定原 ID 与来源样例，B 在创建前返回模拟的 OAuth 未认证错误；没有启动 CLI、MCP、供应商或 OAuth 回调，没有运行生成的登录命令。历史工具 `past_call` 仅为展示样例。
- 浏览器每个 POST 的实际 body 通过工具转发到运行中的 Go 夹具，再由带本地 cookie/同源头的真实 Handler 处理；返回真实状态码、准备回执、文件和 SQLite 结果。此路径未启动 HTTP listener，不验证浏览器真实网络鉴权或 SSE 传输。
- GET 使用每次真实 Handler 导出的响应缓存；EventSource 与剪贴板明确模拟。重启是真的关闭/重开 Application 和 SQLite，浏览器 reload 也实际执行；不是整进程强杀、断电或关闭标签页恢复。
- 来源补充脚本只变更受控读响应中的 source 字段并恢复；不证明这些来源在当前原生 CLI 中成功连接、受管策略执行或工具获准。
- sessionStorage 保证同一标签页的刷新/切换范围；关闭标签页后使用后端持久记录找回，未声称浏览器跨设备或新标签页自动重试相同请求。

## 重跑入口

从仓库根目录使用新的自有目录；保留旧 `bin/netcap`：

```bash
export GOCACHE=/tmp/netcap-w0206-auth-go-cache
make dev-build
go test -race ./internal/app -run TestMCPLogin -count=1 -v
go test -race ./internal/app ./internal/console -count=1
go vet -tags=nativecodex ./...
go run ./tests/mcploginui --root /tmp/netcap-mcplogin-ui-new
```

最后一条在自有 PTY 中运行并接受一行一个 JSON 请求；目录必须尚不存在。夹具将初始化响应写入 `ready.json`，每个请求的完整结果写入 `latest.json`、`exchanges.jsonl`；stdout 给出小型确认回执。支持 HTTP 请求对象 `{method,path,body}` 和测试专用 `{action:"restart"|"drift"|"restore"|"quit"}`，这些动作不是产品 API。

在配置的 Playwright MCP 新标签页运行 `tests/mcploginui/browser.js`（注入 `ready.json` 与当前构建资源映射），随后由 `tests/mcploginui/verify.js` 自动推进。后者是 `functions.exec` 工具宿主脚本，需在该宿主先保存 `authUISession`（实时 exec PTY 句柄）、`authUIRoot`（夹具绝对目录），读取脚本作为表达式执行；它调用浏览器工具和 `write_stdin`，把结果保存为 `authUIResult`。之后运行 `tests/mcploginui/sources.js` 补查来源展示。脚本需要现有配置的工具宿主，不是独立 Node 测试命令；无需安装新的浏览器依赖。

复验失败会抛错并保留已完成检查；需要结束时向已确认存活的夹具发送 `{"action":"quit"}`。目录中仅为受控数据，不将其用于真实登录。

## 收尾与下一步

[差异](W02-06-auth-ui-diff.patch)、[manifest](W02-06-auth-ui-manifest.json)保存归档时生产/测试源码、构建和证据哈希。[清理记录](W02-06-auth-ui-cleanup.json)核对 668 个既有证据与旧 `bin/netcap` 未变；本轮所有自有夹具句柄均在 quit 后返回 exit 0，关闭了本轮浏览器测试标签页。未改共享后台、未提交或发布。

准备控件与来源解释的受控界面缺口已覆盖。SSE 真实登录、完整来源/权限冲突、动态 helper 实际认证与原 ID 不重启恢复仍未验收；[stdio 的 send/EPERM](W02-06-stdio-startup.md)限制未变化，本轮未重复受阻的原生矩阵。W02.07/08 前置仍未完成。后续转回前置独立的 W01.04 原生后台接入与 W01.05 工具故障边界；继续保留六入口、输入独占和 Windows 的硬条件。
