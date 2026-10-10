# W01.02 · 外部原生来源登记

日期：2026-10-01。接续[历史读取](W01-02-history.md)，新增预览后明确登记原生只读来源、会话列表入口及按保存绑定读取历史。**这一步登记原生身份，不代表连接原执行器或取得控制。** W01.02 in_progress，累计 8/40 done，Goal active。

## 实现与决策依据

[应用模块](../../internal/app/native_binding.go)接受已登记项目/实例、原生 UUID、命令 ID 与预览摘要。登记前重新读取元数据，再核对项目、状态命名空间与配置指纹；摘要发生变化即拒绝。命令回执与新的 Session 在同一原有存储事务中提交，丢回执/重启重试保持幂等；并发相同原生身份只保留一份。已有记录保持原控制方式与代次；重复匹配、未知状态归属不任意选择。同 ID 的不同状态目录分开。

记录为 `origin=native`、`control_mode=native_readonly`、`connection=history_only`，保存 `native_binding` 的登记时间、元数据与配置绑定。Scope 保留原生 ID 和真实目录，没有选用新的 Route，没有创建 Operation 或执行配置快照。应用层阻断除本地归档/恢复显示以外的会话命令，启动和轮询跳过原生恢复。历史 API 根据保存的身份构建请求，调用方只能传游标；原配置变化时保留绑定并报错。

[发现界面](../../web/src/NativeSessionBrowser.tsx)仅在单项预览后提供“登记为只读原生会话”。命令 ID 在当前预览的重试间保留；成功后打开原记录。列表新增原生来源筛选，详情隐藏发送/控制/改配入口，显示权限与供应商绑定未验证，并复用原生历史分页界面。归档只改变本地可见性，刷新后身份保持。

本轮核对固定 Codex 0.158.0 的既有生成协议：[协议记录](W01-02-binding-protocol-notes.json)。`thread/read` 可报告来源、模型、环境选择和 `canAcceptDirectInput`，但本轮核对过的只读接口尚未建立外部原线程权限及完整供应商配置的证据。`thread/resume` 返回部分权限信息，却可能加载已卸载线程；不能用它冒充只读预览，也不能把新建时的路由配置直接赋给外部执行。后续仍需实际原执行接入与原配置继承验证，本片没有将只读登记替代该目标。

## 发现并修复

- 原事件处理将所有非 `observed` 记录当作托管执行，导致新增只读原生来源接收 hooks 时错误产生 `Managed=true`。[红灯](W01-02-binding-observation-red.txt)复现后修复：事件只进入时间线及活动时间，不替换原生状态、请求或控制证据；最终定向回归通过。
- 登记合并最初在多个已有匹配中返回第一条，选择随 map 遍历变化。[红灯](W01-02-binding-duplicate-red.txt)复现后改为核对全部匹配，关联不唯一直接拒绝；最终回归通过。

## 实际验证

| 层次 | 结果 | 范围限制 |
|---|---|---|
| 应用层 | [最终定向 race](W01-02-binding-final-targeted.txt)：15 个新场景通过，含原身份、控制拒绝、禁止自动加载、重启回执、并发、元数据/配置漂移、跨目录、旧记录保持、Claude 路径、观察事件和重复匹配 | 使用受控 Runtime；没有真实 CLI 执行 |
| HTTP | 同份日志：6 个新 token/origin/额外路由/null/原生 ID 覆盖边界及原有检查通过 | 进程内 Handler，无 TCP 监听 |
| Handler 流程 | [23 个不变量](W01-02-binding-handler.txt)、[响应导出](W01-02-binding-handler-responses.json)通过；新增来源登记、幂等、按保存身份读历史、拒绝控制、归档/恢复显示；只有原 fixture 的一次 create，零新增原生输入/操作 | 原生返回值受控；不代表模型调用或原执行器连接 |
| 浏览器 | [20 项](W01-02-binding-browser.json)通过：预览前不可登记、模拟丢回执重试复用命令 ID、保存绑定读历史、来源筛选、未知权限/路由、无控制控件、分页、归档/恢复显示、刷新、1440/768/390 无横向溢出、无 JS 异常 | 构建 + Handler 导出响应 + 模拟 EventSource/回执丢失；不是实时 HTTP/CLI |
| 回归与构建 | [app/console race](W01-02-binding-go-race.txt)、最终定向含观察/通知回归、[全包 vet](W01-02-binding-go-vet.txt)、[最终开发构建](W01-02-binding-build.txt)通过 | 全包 race 在观察分类和重复匹配加固前；最终定向覆盖这两处及 Claude 新场景。未重跑未修改的真实 SDK/CLI 协议实验 |

浏览器初轮因测试驱动的 label 定位、上轮 sessionStorage 残留、未模拟归档后的 SSE change 而中止；分别修正驱动后完整重跑。正常最终记录含一条刻意注入的登记 `net::ERR_FAILED`，没有 JavaScript 异常。正文提示的 JSX 编辑曾被类型检查拒绝，修正后最终构建通过。

截图：[1440](W01-02-binding-browser-1440.png)、[768](W01-02-binding-browser-768.png)、[390](W01-02-binding-browser-390.png)。最终 Web 资源 `index-N7Fv3q5U.js` / `index-orGpbq3d.css`。Go 1.26.2、Node 24.13.0、React 19.3.0；底层协议仍为固定 Codex 0.158.0 与 Claude SDK 0.3.280，依赖未改变。

## 重跑与归档

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -run 'TestNativeBinding|TestBoundNativeHistory|TestLocalAuthorizationAndOrigin|TestObservation|TestNotificationOwnership' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go run ./tests/sessionaccess --dump .dev/w0102-binding/handler-responses.json
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

[浏览器脚本](W01-02-binding-browser-script.js)交给 `browser_run_code_unsafe`；重新导出后按实际 Session ID、响应文件与资源名更新映射。浏览器末页历史复用同一 Handler 的原生历史第二页响应，保存身份的请求边界另由应用/HTTP 检查覆盖。

[清理记录](W01-02-binding-cleanup.json)确认自有 fixture 无存活进程、浏览器已关闭；367 个旧证据文件未改。仅使用临时配置/状态与 `.dev/bin/netcap`，未提交、运行真实供应商、停止共享后台或对外发布。[manifest](W01-02-binding-manifest.json)哈希仅代表本次归档时点。

## 未完成与下一步

尚未取得原执行器连接、输入权交接、外部原配置/权限继承和 WSL 环境身份的完整证据；没有为该来源生成可执行终端控制命令。保存来源与读取历史均不能把独立前台、共享后台或 Claude 原生后台提升为可控制入口。完整历史的超限/子会话等限制仍沿用前片。

下一步继续核对并实现原执行器接入，重点是明确原生控制端点、原权限/供应商来源和交接时不加载替代执行；新建、预共享后台、独立前台仍分别验收。既有 socket、Windows、Claude worker 工具结果、W02.06 认证/EOF 和 W03.02 用户反馈缺口保持，整条目标不收口。
