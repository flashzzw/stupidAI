# W01.02 · 原生历史只读读取与分页

日期：2026-10-01。继[元数据发现](W01-02-discovery.md)后，接通两类 CLI 的原生接口可读历史、绑定分页、错误与原始字段展示。W01.02 保持 in_progress，累计 8/40 done，Goal active；本片没有完成外部原执行绑定、实时控制或六入口验收。

## 实现与范围

[应用入口](../../internal/app/native_history.go)复用发现模块的项目/实例、真实目录、状态命名空间与配置指纹核对。新 `POST /api/native-sessions/history` 只接受已登记项目/实例和原生 UUID，不接受任意 cwd、路由或命令。每次扫描上限为 5000 条、8 MiB、单条 1 MiB，请求超时 20 秒。每页显示 50 条；查询两遍有界历史，内容与元数据一致才返回。游标包含绑定摘要、内容摘要与 offset，绑定原生 ID、项目 ID、配置与读取模式。发生漂移、分页循环、重复身份、缺失/摘要条目或超限时拒绝返回部分成功。

分页采用重新扫描，时间成本随历史规模增长。两次一致只证明两次观测相同，不保证原生原子快照；在持续写入时可能需要稍后重新查询。总数与末页标记仅对应本次接口范围，空结果有单独的未知说明。历史结果只在请求/页面中存在，不新增 Session/Operation，也不授权输入、恢复或控制。

[Codex 适配](../../internal/runtime/native_history.go)只调用元数据 `thread/read(includeTurns:false)` 和 `thread/turns/list(itemsView:full, sortDirection:asc)`，读取完全部原生分页。轮次头保留状态、错误与其他字段，条目保留工具输入/结果及未知类型；轮次和消息分别计数。请求 full 却返回 summary/notLoaded 时明确失败，没有回退为普通文本。范围限于 App Server 可见的持久轮次/条目，不含完整原始事件、子会话与外部文件。

[Claude bridge](../../bridges/claude/src/native-history.ts)在创建/恢复 Session 之前分派只读方法。固定 SDK 的 `importSessionToStore` 使用限定 `dir`、`includeSubagents:false`、`batchSize:50`，把主会话 JSONL 的解析记录收集到受限临时内存中；不创建 query、不写入另一个持久 store。记录按文件顺序返回，保留顶层系统正文、工具调用/结果、元数据、未知类型、无 UUID 记录与重复 UUID 更新，原生 UUID 保留在原始字段，位置作为本次基线内的显示身份。旧分支与压缩前记录只在仍存在于主文件时可见，不等于当前有效消息链；SDK 跳过的无法解析行、子会话文件和外部文件均不在范围内。

[界面](../../web/src/NativeHistory.tsx)在发现列表和 UUID 预览中提供显式读取、下一页、重新读取和关闭入口；切换筛选/会话会丢弃迟到结果。文本以文本显示；结构化内容使用服务端保留的 `raw_json`，避免 JavaScript 对大整数的舍入。页面始终显示读取范围，不呈现外部会话发送控件。

## 核对版本与发现的缺陷

复用上片固定 Codex 0.158.0 生成的协议 schema，新增归档 [ThreadTurnsListParams](W01-02-history-ThreadTurnsListParams.json) 与 [ThreadTurnsListResponse](W01-02-history-ThreadTurnsListResponse.json)。`full` 的定义是该轮次持久历史中可见的所有 ThreadItem，响应 `itemsView` 默认 full。本片没有重跑协议生成或真实后台 RPC，也没有把上片失败获取的官方页面当作已读取来源。

Claude SDK 0.3.280 的 [本地类型摘录](W01-02-history-sdk-types.txt)与 [Context7 官方文档结果](W01-02-history-sdk-docs.txt)用于确认接口与范围。另实际查询了 [Session Storage](https://code.claude.com/docs/en/agent-sdk/session-storage) 的 SessionStore append/load 契约；固定类型声明确认 `importSessionToStore` 是本地文件读取、分批 append，且 `includeSubagents` 默认 true，因此生产明确关闭。该接口标记 alpha，兼容结论仅覆盖当前固定 SDK。

首次采用 `getSessionMessages(includeSystemMessages:true)` 时，真实 SDK 合成文件中的系统 UUID/类型返回了，但顶层 content 未返回；[原始失败](W01-02-history-sdk-history-initial.json)保留两份账号的正文断言失败。没有放宽断言：改成主文件记录读取后，[最终 26 项](W01-02-history-sdk-history.json)保留系统正文并通过，同时扩展到旧摘要、无 UUID 和重复 UUID 记录。

截图复核还发现结构化数据经过浏览器 JSON 数字解析后显示了近似值；最终结构化展示和原始字段都使用服务端 JSON 字符串，[最终浏览器](W01-02-history-browser.json)确认 `9007199254740993` 未变成 `9007199254740992`。这保留的是接口返回的 JSON 值，不承诺 SDK 解析前 JSONL 的字节一致性。

## 实际验证

| 层次 | 执行与结果 | 证据边界 |
|---|---|---|
| 应用层 | [最终定向 race](W01-02-history-final-targeted.txt)：16 个新场景通过，含 50+3 分页、跨会话/同目录不同项目游标拒绝、内容/配置漂移、缺失/重复条目、数量/字节限制、空结果、Claude 范围及过期请求 | 受控 Runtime，不代表真实执行 |
| Codex 协议 | 同份日志：10 个新场景通过，51 个轮次的两页读取、错误/工具/未知字段、只读方法限制、foreign 元数据提前阻断、summary/循环/缺页/超限拒绝 | 受控 RPC；没有真实 Codex 后台 |
| HTTP 边界 | 同份日志：6 个新 token/origin/cwd/route/null/非法 ID 检查及原有检查通过 | 进程内 Handler，不是 TCP 监听 |
| 真实 SDK + 生产 bridge | [26 项](W01-02-history-sdk-history.json)通过：A/B 相同 ID、各 57 条主文件记录、完整工具与系统正文、未知字段/旧摘要/重复 UUID、稳定重读、内容变化、跨项目/缺失/非法 ID 和超限拒绝；读取前后文件字节不变，无 query 日志 | 输入是合成 JSONL；没有模型或真实 CLI 会话执行 |
| 生产 Handler 完整流程 | [16 个不变量](W01-02-history-handler.txt)与[响应导出](W01-02-history-handler-responses.json)通过：发现/预览/历史/分页/漂移/空结果、零新增输入/操作及暂停保持 | 受控 Runtime；供浏览器回放 |
| 浏览器 | [26 项](W01-02-history-browser.json)通过：显式读取、范围/正文/工具/大整数、分页末尾、409 后重读、空结果、关闭/迟到响应、暂停不变、1440/768/390 无横向溢出与无 JS 异常 | 真实构建 + Handler 导出响应 + 模拟 EventSource；预期漂移请求产生一条 HTTP 409 console 记录 |
| 回归/构建 | [app/console race](W01-02-history-go-race.txt)、[Runtime 定向 race](W01-02-history-runtime-race.txt)、[原 bridge 认证/来源/恢复](W01-02-history-bridge-regression.txt)、[16 项 SDK 元数据回归](W01-02-history-sdk-discovery-regression.json)、[全包 vet](W01-02-history-go-vet.txt)、[最终开发构建](W01-02-history-build.txt)通过 | app/console 全包在最后新增总字节/Claude 测试前；最终定向覆盖新增测试。未执行全仓所有测试 |

新 Claude 应用测试首次漏配 fixture 的 bridge 路径，被现有能力检查拒绝，[失败记录](W01-02-history-targeted-fixture-missing-bridge.txt)保存；补齐与已有 Claude fixture 一致的占位文件后最终定向通过，生产准入未放宽。浏览器首轮使用过严的空结果 exact 文本定位，后一次重建后脚本未更新资源名，均是驱动错误；修正后完整重跑通过，不计为产品失败或成功证据。

截图：[1440](W01-02-history-browser-1440.png)、[768](W01-02-history-browser-768.png)、[390](W01-02-history-browser-390.png)。最终资源为 `index-C8mxGzIr.js` / `index-orGpbq3d.css`。开发二进制仅 `.dev/bin/netcap`，Go 缓存为 `/tmp/netcap-w0206-auth-go-cache`；SDK 0.3.280、Go 1.26.2、Node 24.13.0、React 19.3.0，依赖锁未改变。

## 可重跑入口

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/runtime ./internal/console -run 'TestNativeHistory|TestCodexNativeHistory|TestNativeDiscover|TestNativePreview|TestCodexDiscovery|TestReadOnlyProbeInspection|TestLocalAuthorizationAndOrigin' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run 'TestCodexNativeHistory|TestCodexDiscovery|TestReadOnlyProbeInspection|TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache make claude-auth-contracts
python3 tests/claude_history_probe.py --output .dev/w0102-history/sdk-history.json
python3 tests/claude_discovery_probe.py --output .dev/w0102-history/sdk-discovery-regression.json
GOCACHE=/tmp/netcap-w0206-auth-go-cache go run ./tests/sessionaccess --dump .dev/w0102-history/handler-responses.json
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器将 [脚本](W01-02-history-browser-script.js)传给 `browser_run_code_unsafe`；重导出/构建后须按实际响应 ID、文件和资源名更新映射。脚本只回放进程内 Handler 的归档结果，不是实时服务。新片证据使用独立前缀，326 个既有证据文件未改；源码/构建/证据哈希只代表本次归档时点，见 [manifest](W01-02-history-manifest.json)。

## 剩余条件

本环境此前已确认 socket 监听受限，本片没有无新条件重复启动监听器；真实 HTTP/SSE、真实 Codex RPC、真实 CLI 控制和 Windows 均未执行。历史读取不替代外部原执行绑定、输入争用与在途工具核对。超限历史、子会话/外部文件与 SDK 不可解析记录仍有明确范围限制，不能称全部原始历史已验收。

下一片继续 W01.02 的显式原执行绑定/接入及执行器/环境来源展示；W01 独立前台、Claude 原生后台/worker 工具结果、W02.06 的 EOF/认证/动态头与 W03.02 真实用户反馈缺口继续保留。自有 SDK 进程和浏览器已关闭；未提交、调用真实供应商、停止共享后台或对外发布。
