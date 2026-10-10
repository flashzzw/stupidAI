# W01.02 · 已登记原生来源的只读执行观察

日期：2026-10-01。接续[身份与接续实验](W01-02-executor.md)。本片把 Codex 原后台的轮次、工具和占用查询接到已登记来源，并提供显式开始/停止的页面观察。**这是按原身份轮询查询，不是取得输入权或完成原执行器控制接入。** W01.02 in_progress，累计 8/40 done，Goal active。

## 实现

[Runtime 可选接口](../../internal/runtime/native_observation.go) `ObserveNativeExecution` 专用于外部来源的只读查询。Codex 检查已保存状态目录与原生身份、清除新建路由/能力覆盖并强制 `NoAutoResume`，复用当前已核对的原后台端点和执行投影。元数据还须返回相同的原 ID 与规范项目目录。未实现该接口的 Backend 明确拒绝，不退回可能启动执行的普通 Observe；Claude/OpenCode 外部观察没有因此开放。

外部线程使用 `thread/turns/list` 完整分页；工作台已有 legacy 会话仍保留能读取异步问题的原历史入口。队列、Goal、后台终端、子会话沿用现有查询与失败规则。观察结果移除终端控制命令；不调用 resume/start/turn/start，不改变原权限，不从空闲推导输入权。

[应用入口](../../internal/app/native_observation.go) `ObserveBoundNative` 只接受本地来源记录 ID；查询前后重新核对项目、实例、namespace、配置指纹、原 Scope/绑定和 generation。结果有读取时间、原身份、状态、错误与 View，单独返回，不持久化为 Session 控制状态，不创建 Operation/快照。原有只读来源的发送/恢复/响应/改配禁令保持。

HTTP `POST /api/sessions/{id}/native/observe` 只接受 `{}`，沿用 token/origin 与严格 JSON 校验。身份/配置变化返回错误；已读取到的未加载、状态未知、查询不完整或失联结果明确为 unavailable，保留可用的观察材料，不把它标为可控制。

[界面](../../web/src/NativeObservation.tsx)只在 Codex 原生只读来源中提供“开始只读观察”。每次查询完成 3 秒后刷新，不并发安排固定间隔请求。停止、离开面板、失联或不可用时停止后续请求并忽略迟到响应；重连需显式重启观察。保存的上次采样显示过期/刷新中状态。页面展示原身份、已返回占用、最近轮次/消息和工具输出；请求只读展示，没有审批/发送/恢复控件。原生工具内容按文本显示。

顶部 Session 仍是保存的来源记录，可能保持待核对；观察采样有自己的时间与状态。`live_observe` 能力为 conditional；`resume` 和 `live_input` 仍 unverified。

## 发现与修复

- 初版复用 legacy 历史读取，在仅提供分页轮次的外部 fixture 中丢失工具输出。[失败记录](W01-02-observe-history-reader-red.txt)后拆出内部历史读取接缝：外部用分页接口，原工作台 legacy 行为保留。该日志同时包含一个测试驱动缺少同源 Origin、因此先被 CSRF 检查拒绝的样例，修正驱动后最终 HTTP 检查通过。
- [同连接红灯](W01-02-observe-sibling-red.txt)复现：某个线程的读取 RPC 被拒绝时，原 Observe 无条件关闭共享代理，使其他会话未决请求也失效。修复区分原生 RPC 拒绝与传输故障：前者保留健康连接；后者继续关闭连接并标记请求待核对。[最终回归](W01-02-observe-sibling-final.txt)用连接绑定的原生命令审批及传输中断对照，3 个场景通过。
- 未加载线程常伴随后台终端不可查询，应用现在优先说明“原执行未加载、没有恢复执行”，不只显示通用查询失败。

## 实际验证

| 层次 | 实际结果与限制 |
|---|---|
| Runtime | [最终 race](W01-02-observe-runtime-race.txt)：46 个顶层测试、144 个子测试通过，明确排除 `^TestOpenCode` 的既有监听测试。新增 10 个原执行状态/身份场景、3 类未实现适配器拒绝，含真实原生审批的最终同连接故障回归另见上文；测试使用受控 RPC，无生产 socket |
| 应用 | 12 个状态/漂移场景及未登记/不支持来源拒绝通过；观察不写 Session/Operation、不授予输入，含读取前后配置变化、generation、取消、外来身份。[最终定向](W01-02-observe-final-targeted.txt)覆盖最后的未加载诊断，以及来源登记/历史/来源预览相关回归 |
| HTTP / Handler | 5 个新鉴权/Origin/身份覆盖/控制字段/null 边界通过；[29 项 Handler 不变量](W01-02-observe-handler.txt)及[最终响应](W01-02-observe-handler-responses.json)通过。含运行、空闲、未加载、不完整、失联观察；只有既有 fixture 的一次创建，没有新增模型输入或操作。无 HTTP 监听 |
| 全包回归 | [app/console race](W01-02-observe-app-console-race.txt)通过；最后未加载错误文案和优先级另由最终定向覆盖。Runtime 的排除项如上，未声称整仓全部 race 通过 |
| 浏览器 | [23 项](W01-02-observe-browser.json)通过：显式启动、正确绑定/空请求体、顺序刷新、停止、迟到响应不应用、断线、重连不自动恢复、切换页面、无控制请求、文本输出、无 JS 异常及 1440/768/390 无横向溢出。使用构建、Handler 响应、模拟 EventSource/传输，不是实时 HTTP/CLI |
| 构建 | [全包 vet](W01-02-observe-vet.txt)退出 0；[最终开发构建](W01-02-observe-build.txt)通过，含 Web 类型检查/Vite 和 `.dev/bin/netcap`。资源为 `index-DyX1tA6S.js` / `index-orGpbq3d.css` |
| 原生 stdio | [固定 CLI 对照](W01-02-observe-native-protocol.json)8 项检查通过；具体范围见下节。没有真实工具或模型轮次 |

浏览器测试最初因工具执行上下文缺少 `URL` / `setTimeout` 全局而中断，改用现有路径解析与 Playwright 等待后完整重跑。最终控制台的一条 `ERR_FAILED` 来自刻意注入的网络失败，没有页面 JavaScript 异常。截图：[1440](W01-02-observe-browser-1440.png)、[768](W01-02-observe-browser-768.png)、[390](W01-02-observe-browser-390.png)。

## 真实协议范围

[原生脚本](../../tests/codex_native_observation_probe.py)复用固定 Codex 0.158.0 stdio fixture，在独立 HOME/profile/项目创建明确标记的合成历史；没有执行模型轮次。进程重启后，分别查询原线程的 metadata、分页轮次、queue、Goal、后台终端和子会话。

- 未加载时 metadata 返回原 ID 与 `notLoaded`；后台终端查询返回 `-32600 thread not found: <原 ID>`。`thread/loaded/list` 查询前后都不含原线程，证明这组读取没有加载它。
- 随后在**独立 fixture 准备步骤**中显式 `thread/resume` 原 ID，再执行同样的只读查询：六类 RPC 均成功，原 ID 与 idle 状态保持。
- 两段观察区间的方法均在只读白名单中。准备阶段的 resume 不属于产品观察路径，不能用它证明产品会恢复线程。

本实验使用 stdio，未经过生产 Unix proxy、后台 peer 核对、HTTP 或浏览器；活动工具/审批/队列在途显示只有受控测试证据，没有真实在途执行样本。原权限继承仍保留上一片冷恢复 sandbox 变化的失败结论。供应商配置指向本地不可用端口且未发起模型调用，未连接真实供应商或停止共享后台。所有自有进程通过 fixture 等待退出，未及时响应停止信号者以 -9 退出，不当作正常 CLI 退出验收。

## 重跑与归档

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -skip '^TestOpenCode' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -run 'TestBoundNativeObservation|TestNativeBinding|TestBoundNativeHistory|TestSessionAccess|TestLocalAuthorizationAndOrigin' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go run ./tests/sessionaccess --dump .dev/w0102-observe/handler-final.json
python3 tests/codex_native_observation_probe.py --output .dev/w0102-observe/native-protocol.json
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

[浏览器脚本](W01-02-observe-browser-script.js)交给 `browser_run_code_unsafe`；重新导出 Handler 后按实际 ID 和资源文件更新映射。[差异](W01-02-observe-production.patch)基于本片开始的既有文件，新增文件以源码链接与[manifest](W01-02-observe-manifest.json)为准。哈希只描述此归档时点。Go 1.26.2、Node 24.13.0；依赖没有变化。

[清理记录](W01-02-observe-cleanup.json)确认 432 个旧证据未变、自有原生进程退出、浏览器关闭。没有提交、真实供应商调用、共享后台切换或对外发布。

## 保留缺口与下一步

原端全部审批是否可见、终端草稿、原权限/供应商继承和独占输入权均未由此解决；观察空闲不能放行控制。没有订阅全部原生通知，没有实现外部 Claude 原执行观察，也没有完成生产 socket、独立前台、真实在途工具、Windows 或六入口联合验收。

下一片继续原执行接入与原配置/权限证据，尤其区分正在运行的线程与冷恢复。正常工作台 Codex 冷恢复目前也未显式重传 Session.Permission，需针对该生产路径建立回归，不能假定已有配置快照就保证恢复权限。外部来源仍不能套用新建路由；W02.06 的 EOF/认证及其他硬要求继续保留。
