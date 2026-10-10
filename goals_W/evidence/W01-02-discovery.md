# W01.02 · 原生元数据发现、预览与只读副作用修复

日期：2026-10-01。接续[已有记录来源首片](W01-02-access.md)。本片接通两类 CLI 的原生元数据发现/按 ID 预览及生产界面；完整历史与外部控制接入尚未完成，W01.02 保持 in_progress，累计 8/40 done，Goal active。

## 实现与真实边界

应用层新增[发现模块](../../internal/app/native_discovery.go)，只接受已登记项目/实例。查询前后核对真实目录、状态命名空间、配置指纹，分页游标绑定项目 ID、实例和筛选；每页 50 条。错误 ID/项目归属、重复行、超限/不前进游标、外部配置变化均不形成可用记录。已有记录按 Backend + 状态命名空间 + 原生 ID + cwd 关联，单个原生 ID 不跨配置合并身份。

[原生适配](../../internal/runtime/discovery.go)与 [Claude bridge](../../bridges/claude/src/discovery.ts)只读取元数据，不调用 create/send/resume。新界面提供选择、分页、UUID 预览和打开已有记录；外部未绑定记录明确保留控制限制。平台 Session/Operation 不因发现或预览新增。

同时复现并修复上一片只读预览的底层副作用：Codex 探测目录中的后台已退出时，原 `startCodexProbeServer` 会继续创建启动保留记录并调用后台程序，未遵守 `NoAutoResume`。受控程序[红灯](W01-02-discovery-preview-red.txt)确认发生了启动；现在只读路径在尝试启动前返回具体原因，[绿灯](W01-02-discovery-preview-green.txt)确认不启动、不写启动记录。原有存活后台的归属检查仍在此守卫之前。

## 协议事实与版本依据

尝试读取 OpenAI 官方 App Server 页面时，配置的网络代理不可达，[获取记录](W01-02-discovery-openai-fetch.txt)已脱敏保存；没有将该页面当成已获取的依据。随后在隔离目录运行固定后台 CLI `0.158.0` 的 `app-server generate-json-schema --experimental`，退出 0，[生成记录](W01-02-discovery-schema-info.json)与三个原始协议文件一并归档：

- [ThreadListParams](W01-02-discovery-ThreadListParams.json)：空/省略 `sourceKinds` 默认只含交互来源，因此实现显式列出该版本的来源枚举。`useStateDbOnly: true` 避免扫描修复索引，但不覆盖未入库的历史。
- [ThreadListResponse](W01-02-discovery-ThreadListResponse.json)：`source` 是原生创建来源；`threadSource` 是分析分类，不能互换。
- [ThreadReadParams](W01-02-discovery-ThreadReadParams.json)：显式 `includeTurns: false`，只读单个会话元数据，不加载完整历史或发送恢复指令。

这证明使用了固定版本声明的参数，不证明本环境已完成真实后台 RPC。

Claude SDK 0.3.280 的本地类型与 [Context7 返回的官方文档](W01-02-discovery-sdk-docs.txt)确认 `listSessions/getSessionInfo`、项目范围和元数据字段。固定版本类型另含 `offset` 与 `includeProgrammatic`；实现限定 `dir`、`includeWorktrees: false`、`includeProgrammatic: true`，并验证页大小/游标。SDK 元数据没有可证明前台/后台活跃状态的字段，因此返回 `native_history/unknown`。

## 实际验证

| 层次 | 已执行与结果 | 不覆盖的内容 |
|---|---|---|
| 应用层 | [最终定向 race](W01-02-discovery-targeted-race.txt)：14 个场景通过，含同目录不同项目 ID 的游标拒绝、配置漂移、跨项目/跨 ID、重复/异常分页、未知适配器和已登记命名空间关联 | 使用受控 Runtime；不代表真实 CLI 接续 |
| Codex 协议调用与副作用 | 同份日志：元数据列表/单项读取只调用预期方法；创建来源不被 analytics 字段覆盖。只读探测后台启动红→绿通过 | RPC 由受控协议端返回；没有启动真实 Codex 后台 |
| HTTP 边界 | 同份日志：6 个新增鉴权/异源/任意 cwd/路由/null/非法 ID 检查及原有检查通过 | 进程内 Handler 请求，不是 TCP 验收 |
| 真实 SDK/生产 bridge | [16 项](W01-02-discovery-sdk.json)通过：A/B 各 53 条合成 transcript，50+3 分页、相同 ID 不串目录、标题/项目正确、状态未知、非法输入拒绝、无 query 日志、原历史字节不变 | 真实 SDK 文件读取，输入是合成历史；没有真实模型/CLI 会话执行 |
| Handler 完整读流程 | [10 项不变量](W01-02-discovery-handler.txt)及[导出响应](W01-02-discovery-handler-responses.json)通过，包含发现、游标第二页、外部 UUID 预览、已有记录关联、零输入/零新增操作和暂停保持 | 受控 Runtime；导出供界面回放 |
| 回归与构建 | [app/console race](W01-02-discovery-go-race.txt)、[Runtime 定向 race](W01-02-discovery-runtime-race.txt)、[bridge 原有认证/来源/恢复](W01-02-discovery-bridge-regression.txt)、[全包 vet](W01-02-discovery-go-vet.txt)、[最终开发构建](W01-02-discovery-build.txt)通过 | app/console 全包 race 在最后项目 ID 游标加固前；最终定向 race 覆盖该改动。未运行全仓测试 |
| 浏览器 | [18 项](W01-02-discovery-browser.json)通过：显式查询、绑定范围、来源分列、外部不获控制、已有记录仍暂停、按 UUID 预览、分页、切筛选清旧结果、1440/768/390 无横向溢出、零 create/send 请求、无 JS 异常 | 真实构建 + 导出 Handler 响应拦截 + 模拟 EventSource；不是实时 HTTP/CLI 链路 |

截图：[1440](W01-02-discovery-browser-1440.png)、[768](W01-02-discovery-browser-768.png)、[390](W01-02-discovery-browser-390.png)。测试工具为 Go 1.26.2、Node 24.13.0、SDK 0.3.280、React 19.3.0；固定 Codex 0.158.0 只用于协议生成。本片未重试已知受限的监听服务，不将 SDK 元数据读通外推为 stdio、MCP 或原生控制已恢复。

## 可重跑入口

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console ./internal/runtime -run 'TestNativeDiscover|TestNativePreview|TestCodexDiscovery|TestReadOnlyProbeInspection|TestLocalAuthorizationAndOrigin' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run 'TestCodexDiscovery|TestReadOnlyProbeInspection|TestClaude|TestProbeScope' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache make claude-auth-contracts
python3 tests/claude_discovery_probe.py --output .dev/w0102-native/sdk-discovery.json
GOCACHE=/tmp/netcap-w0206-auth-go-cache go run ./tests/sessionaccess --dump .dev/w0102-native/handler-responses.json
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器把[归档脚本](W01-02-discovery-browser-script.js)交给 `browser_run_code_unsafe`。脚本绑定归档响应/会话 ID 和当前构建资源名；重建或重导后先按实际文件名更新映射。fixture 的响应不充当真实原生状态。

只在临时 profile、项目、状态与 `.dev` 构建中操作；合成 SDK 历史根目录与 schema 根目录分别保存在原始结果。自有驱动已退出、浏览器已关闭，291 个既有证据文件保持原样。源码/构建/原始记录哈希见 [manifest](W01-02-discovery-manifest.json)。未提交、调用真实供应商、停止共享后台或对外发布。

## 未完成条件与下一步

元数据发现是接入前的一步。目前没有导入外部原生会话、读取其完整对话或授予实时控制；Codex 尚缺真实后台发现/预览验收，Claude 合成历史读取也不证明真实创建入口、后台或独立前台能接续。下一片继续原生历史读取与显式原执行绑定/接入，保持恢复前输入权、配置/权限和在途工具核对。

WSL 环境归属、六入口矩阵、独立前台、Claude 原生后台/worker 结果及 W02.06 的连接/认证/UI 等硬缺口继续保留。W03.02 仍待用户反馈；W01.02 和整条 Goal 不因本片通过而收口。
