# W01.02 · 已有记录的接续来源与只读预览

日期：2026-10-01。W01.02 前置 W01.01 已有，接续 [W03.03 映射](../../docs/workbench-implementation-map.md)中的来源/输入权语义。本片有生产实现与分层验证，但未完成外部会话发现和接入，W01.02 保持 in_progress，累计 done 8/40，Goal active。

## 实现范围

- 会话列表/详情与三个读取接口统一返回查询投影 `access`，区分工作台创建、观察事件、隔离探测、未知旧来源；列表可按来源筛选，展示执行配置名称。事件中的原生 ID、伪造 origin/control_mode 都不赋予实时连接或控制权。
- 新增 `POST /api/sessions/{id}/access/preview`，只接受已有本地 Session 与空请求体，复用鉴权。沿原绑定核对来源/路由/状态目录，再 `ObserveFresh` 且 `NoAutoResume=true`；重新锁定后核对 generation 与绑定变化。预览不写 Session/Operation、不授予输入、不恢复自动化，不导入任意原生 ID。
- 证据不完整、身份不符、断连、在途工具、未决审批、配置变化、较晚的转人工均有具体限制。已保存历史、实时观察、恢复控制、发送输入分别展示；产物/配置/Gate 等业务未接入。
- Claude 原生投影补 `sdk_live/sdk_offline`；完整历史不再被当作在线查询。与 Codex `notLoaded` 一起用于只读预览：离线历史可查看，实时观察/输入不可用；显式恢复仍需原有准入。新建继续使用既有独立入口。

生产入口：[session_access.go](../../internal/app/session_access.go)、[queries.go](../../internal/app/queries.go)、[server.go](../../internal/console/server.go)、[SessionAccessDetails.tsx](../../web/src/SessionAccessDetails.tsx)、[SessionList.tsx](../../web/src/SessionList.tsx)。使用说明见 [CONSOLE](../../CONSOLE.md)。新 `access` 是读模型，不是持久授权，原有控制命令及原生写入准入保持原契约。

## 实际验证

| 层次 | 结果与证据 | 范围限制 |
|---|---|---|
| Application 接续来源/预览 | [最终定向 race](W01-02-access-targeted-race.txt)：12 个新场景通过，含事件伪装、调用方修改投影、原身份/NoAutoResume、空闲/在途/审批/不完整/错误 ID/断连/离线、转人工竞争和配置漂移 | 受控 Runtime，不是 CLI 实验 |
| HTTP 鉴权/边界 | 同份日志：5 个新边界（无令牌、异源、任意原生 ID、null 请求、未知 Session）及既有用例通过；观察来源通过三种生产读取/预览路径仍只读 | `httptest.NewRecorder` 请求真实 Handler，不开启网络监听 |
| 生产 Handler + 执行器计数 | [受控驱动](../../tests/sessionaccess/main.go)导出[实际响应](W01-02-access-handler-responses.json)，[5 项不变量](W01-02-access-handler.txt)通过：一次创建、零原生输入、仅受控会话读原生、无新 Operation、人工控制不变 | 执行器返回明确的 fixture 证据，不宣称真实模型或原生连接 |
| 既有回归 | [app/console 全包 race](W01-02-access-go-race.txt)通过；最终离线区分后重跑上述定向 race 和 [Claude 投影/控制回归](W01-02-access-claude-projection-regression.txt)通过 | 全包 race 在离线区分前；最终定向检查覆盖后续修改，不声称最后版本全仓测试 |
| 前端类型/开发构建/静态检查 | [最终构建](W01-02-access-build.txt)、[全包 vet](W01-02-access-go-vet.txt)通过；输出 `.dev/bin/netcap` | 未覆盖真实 CLI 连接效果 |
| 浏览器 | [16 项](W01-02-access-browser.json)通过，1440/768/390 截图见下；来源筛选、观察记录无发送/预览能力、受控只读预览、暂停保持、键盘展开、切会话丢弃旧预览、窄窗无横向溢出、无新 JS 异常 | 使用真实构建资源与导出的 Handler 响应拦截，并模拟 EventSource；无真实 HTTP/SSE 服务 |
| 监听服务 | [启动失败](W01-02-access-listener-blocked.txt)：`listen tcp 127.0.0.1:0: socket: operation not permitted` | 没有将拦截响应的浏览器测试算作监听服务或原生链路通过 |

截图：[1440](W01-02-access-browser-1440.png)、[768](W01-02-access-browser-768.png)、[390](W01-02-access-browser-390.png)。测试环境用 Go 1.26.2、Node 24.13.0、React 19.3.0、Vite 8.0.10；本片没有调用真实 CLI 或模型。React 文档通过 Context7 查询官方 `react.dev` 的请求清理及组件 key 说明；界面使用身份/代际/连接/状态 key 丢弃旧预览，未增加依赖。

浏览器准备曾因工具 VM 不提供动态 import/URL 全局而失败，改用标准 Playwright `route.fulfill({path})` 加载授权工作区内的测试响应/构建文件，并用字符串提取请求路径后重跑。早期仅加载 HTML 的资源错误不计入最终 16 项；最终脚本重新注册页面异常收集，结果无异常。此调整没有开启服务或改变网络权限。

## 重跑入口与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console ./internal/runtime -run 'TestSessionAccess|TestLocalAuthorizationAndOrigin|TestObservationCredential|TestClaudeResult|TestResume|TestSessionHistory|TestObservations' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run 'TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
GOCACHE=/tmp/netcap-w0206-auth-go-cache go run ./tests/sessionaccess --dump .dev/w0102/handler-responses.json
```

浏览器使用 [归档脚本](W01-02-access-browser-script.js)交给 `browser_run_code_unsafe`。脚本引用归档的五份响应与当前构建文件名，均纳入 [manifest](W01-02-access-manifest.json)；重建后先按新资源名更新脚本，重导响应后按新的 Session ID 更新映射。固定回放仅用于界面验收，CLI 和真实请求效果须另测。

隔离根目录记录在 Handler 响应中；只创建临时 profile、状态和受控执行器，没有读取用户账号、停止共享后台、发送外部消息、提交或发布。浏览器已关闭，监听驱动已失败退出，268 个既有证据文件保持原样。

## 保留的完成条件

当前只处理已登记工作台记录。尚缺原生历史/正在运行会话的发现列表、按来源预览并接入原执行、六入口完整能力矩阵、WSL 环境归属与原生执行器证据。独立前台、Claude 原生后台和 worker 工具结果连续性仍分别由 W01.03–W01.06 关闭；观察事件不会被当作已实现这些入口。

下一片继续 W01.02 的原生发现/接入前预览，沿现有 Runtime 身份守卫与只读能力推进；所需真实连接/监听受限时分开保存受控接口与未执行证据。W02.06 的 stdio EOF、完整认证/权限/UI 等缺口全部保留，W03.02 继续等待用户试用反馈。
