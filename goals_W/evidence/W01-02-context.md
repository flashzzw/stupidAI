# W01.02 · 保存绑定、登记定义与后台环境采样

日期：2026-10-01。列表、接续详情、原生发现和按 ID 预览现在分别展示保存的绑定、当前登记定义以及已取得的后台采样。该片完成展示与投影，不授予新的输入权限；W01.02 保持 in_progress，累计 8/40 done，Goal active。

## 实现与证据含义

- `access.context.binding` 来自 Session 保存的 Scope，包含执行配置 ID、CLI/profile/settings/bridge/端点、状态命名空间、项目、原生 ID 与权限选择。当前登记只比较定义，重命名不算执行配置改变；变化或缺失时保留原绑定并提示。定义相同不证明文件内容、连接健康或原配置生效。
- events/旧来源显示 `recorded_context`，不采用其环境字段或权限声明；probe 明确标为派生隔离配置。Context 是查询投影，不写入状态作为授权。
- 生产 Codex 观察复用已验证的 Unix peer，再核对绑定 profile、PID/birth 和 `/proc/<pid>/exe`。本地用户名按已验证的当前有效 UID 查询，不采用进程 `USER`。只展示选定的 `WSL_DISTRO_NAME`，标为进程环境声明；缺失不能判断是否为 WSL，其他环境变量不进入响应。
- 后台证据带来源与采样时间，只说明本次接入进程，不证明历史线程最初的执行环境、创建工具或当前输入独占。成功只读预览返回新样本但不持久化；原生只读观察停止后仍将样本标为上次记录。Claude 尚无此采样实现。
- 绑定/登记状态变化或切换会话时清除旧预览；仅样本时间更新不重置预览。列表提示定义差异，显示已有后台采样，支持 profile/状态目录/采样用户名与 WSL 声明搜索。原生发现展示所选配置，`vscode` 等改称原生分类，避免断言由 VS Code 创建。

新增 Context 没有加入 `NativeSession`，不改变原生预览/登记 token 的身份摘要。API 拒绝客户端提供的 Context 或 Executor，沿用原鉴权与空对象约束。

## 验证

| 层次 | 实际结果 |
|---|---|
| 新定向测试 | [通过](W01-02-context-targeted.txt)：4 个顶层、12 个子测试。5 个进程环境场景核对自有 Go helper 的真实 `/proc`；应用检查保存绑定、定义改名/变化/缺失、event/legacy/probe、列表/详情与只读预览不持久化 |
| Runtime race | [60 个顶层、233 个子测试](W01-02-context-runtime-race.txt)通过，明确排除 `^TestOpenCode` 监听测试，不称全仓测试通过 |
| 应用 / HTTP | [app/console race](W01-02-context-app-console-race.txt)通过，含新增 3 个客户端上下文/环境注入拒绝边界及事件来源投影检查 |
| 生产 Handler + 受控 Runtime | [33 项](W01-02-context-handler.txt)通过；[原始响应](W01-02-context-handler-responses.json)包含保存绑定、预览采样、事件未验证与发现配置；沿用只读、无原生输入、无新操作等断言。没有启动 HTTP 监听器 |
| 构建 UI + 受控响应 | [31 项浏览器检查](W01-02-context-browser-result.json)通过；[脚本](W01-02-context-browser-script.js)使用生产构建、Handler 导出响应和显式展示变体，拦截网络与 EventSource。覆盖旧预览清除、采样来源/时间、无 WSL 声明、事件/原生只读无控制、分类文案、发现/按 ID 配置、停止后旧样本。零原生写请求、零 JS 错误，1440/768/390 无横向溢出 |
| 真实 Runtime stdio 回归 | [42 项](W01-02-context-native.txt)通过，workspace/read_only 各 21 项；[workspace](W01-02-context-native-workspace.json) / [read_only](W01-02-context-native-read_only.json)保存实际 RPC 和进程回执。原 ID 创建来源恢复、权限/审批、在途拒绝重复输入、原轮次中断与 active Goal 占用未退化 |
| 静态 / 构建 | [全包带 nativecodex 标签 vet](W01-02-context-vet.txt)、[开发构建](W01-02-context-build.txt)通过，后者包含 Web 类型检查；Go 1.26.2、React 19.3.0、TS 5.9.3、Vite 8.0.10；固定 Codex 0.158.0 |

详情截图：[1440](W01-02-context-browser-1440.png)、[768](W01-02-context-browser-768.png)、[390](W01-02-context-browser-390.png)。发现/预览截图：[1440](W01-02-context-discovery-1440.png)、[768](W01-02-context-discovery-768.png)、[390](W01-02-context-discovery-390.png)。均已人工查看，长路径可换行，新增信息沿用现有米白/绿色、折叠详情和窄屏侧栏结构。

浏览器显式变体包括登记变化/缺失、列表已有采样、vscode 分类、无 WSL 声明和迟到预览；这些只证明展示行为，不冒充 Handler 或真实 CLI 返回。三个尺寸按构建资源 index-CKGHeG8D.js / index-cXcDRM69.css 执行。

新进程测试使用手动构造的 peer 接缝与自有 helper，未走真实 Codex Unix socket 握手。真实 stdio 测试连接不实现新采样接口，因此 **42 项回归不验证新 Unix peer 环境采样链路**。该回归使用隔离 profile/项目、合成历史与不可用的 `127.0.0.1:9` 供应商，不调用真实供应商。

## 验证过程中的失败

- [最初定向失败](W01-02-context-targeted-initial.txt)来自测试假设：`exec.Cmd` 启动环境会去重同名变量，只保留最后值；改为按实际运行进程的最后值验收，未为此修改产品行为。
- [最初 Handler 失败](W01-02-context-handler-initial.txt)要求刚创建的持久 Session 已有采样，受控 Runtime 不保证这一点。断言移到实际获取新样本的 access/preview 响应；应用测试单独证明预览不持久化，浏览器另测无样本状态。
- 浏览器脚本初版使用了工具隔离环境未提供的 `URL`，随后又等待了组件重置后折叠的详情。改为解析受控 URL，并在等待新定义附着后显式展开。最终 31 项均实际执行，没有以跳过断言换取通过。

## 重跑入口与隔离

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test ./internal/runtime ./internal/app \
  -run 'TestCodexExecutorEvidence|TestSessionContext|TestAccessPreviewShows' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -skip '^TestOpenCode' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go run ./tests/sessionaccess --dump .dev/w0102-context/handler-responses.json
NETCAP_NATIVE_CODEX_EVIDENCE="$PWD/.dev/w0102-context/native" \
GOCACHE=/tmp/netcap-w0206-auth-go-cache make codex-runtime-native \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器脚本通过配置的 Playwright `browser_run_code_unsafe(filename)` 执行，内嵌本次导出的固定响应，资源路径指向本次 `web/dist`；后续构建更新时需重新导出/生成脚本并另存证据。组件重置沿用 React 的 key 语义；本轮先前通过 Context7 核对 [React 官方说明](https://react.dev/learn/you-might-not-need-an-effect)。UI 工作使用 web-design-engineer 的 Extension 模式及 React best practices，保持既有布局，不新增导航或设计系统。

[清理记录](W01-02-context-cleanup.json)确认 559 个旧证据哈希不变，4 个自有原生协议进程均已退出且 exit 0，浏览器 tab 已关闭。只更新 `.dev/bin/netcap`，未动旧 bin、共享后台或用户配置，没有提交或对外操作。[差异](W01-02-context-diff.patch)和 [manifest](W01-02-context-manifest.json)记录归档时点。

## 仍未完成

生产 Unix proxy/peer/认证与新环境展示的完整端到端、Claude 原执行环境、完整配置/权限继承与输入独占、真实工具接续、六入口与 Windows 验收仍保留。无 rollout 或创建来源不唯一仍是 unknown；本片不改变恢复语义。W02.06 EOF/认证、W03.02 用户反馈和其他硬要求继续按 PROGRESS 推进，整条 W 系列目标尚未完成。
