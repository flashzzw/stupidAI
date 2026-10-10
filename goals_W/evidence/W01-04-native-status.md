# W01.04 · Claude 原生会话清单与受限状态采样

日期：2026-10-02。接通固定 CLI 的会话清单查询、保存身份核对及界面采样；原生后台控制和完整工具/输入交接仍未完成。W01.04 in_progress，累计 8/40 done，Goal active。

## 接口核对与实际边界

官方 [Agent view](https://code.claude.com/docs/en/agent-view) 将 `agents --json` 定义为脚本状态查询入口，`--all` 保留已退出后台记录；它提供完整 `sessionId`、短 `id`、`kind`、`startedAt` 以及可选的 `pid/status/state/waitingFor`。`jobs/<id>/state.json` 不是稳定接口。资料通过 Context7 获取，见[摘录](W01-04-native-status-docs.json)；固定 CLI 2.1.280 的[帮助输出](W01-04-native-status-help.json)确认这些参数。

文档中的普通 `agents` 会按需启动 supervisor，因此最初不能据“列表”名称把它接入只读接口。进一步的真实固定 CLI 实验区分了 **带 `--json` 的非交互查询**：

- [空配置对照](W01-04-native-status-probe.json)：`agents --json --all --cwd` 返回 `[]`；前后 `daemon status` 都是 not running，未出现 daemon 日志/roster，未见该隔离配置的残留 Claude 进程。CLI 写入了自己的 `.claude.json` 与备份。
- [合成后台记录对照](W01-04-native-status-roster-fixture.json)：stopped/working/blocked/done 的隔离文件由测试构造，仅由真实 CLI 读取和归一化。`--all` 返回终止记录；SessionStart/apiKeyHelper/MCP 哨兵未运行，daemon status 仍 not running；CLI 另维护 `pins.json`。这不是存活后台 worker 验收。
- [过期记录失败](W01-04-native-status-native-stale-record-failure.txt)：测试最初把旧 `working` 文件当作当前状态，实际 CLI 返回 `failed`。改用新鲜记录后 A/B 对照通过；没有修改生产投影迎合文件原值，也不推断其内部过期阈值。
- [初始化前的存活 TUI](W01-04-native-status-live-before-setup.json)仍在合成 API-key 确认界面，清单为空。故“清单没有返回”不能解释成原执行不存在。

上述样本支持本片固定 JSON 路径的受限使用，不为普通 `agents`、`attach`、`respawn`、未来 CLI 版本或所有启动配置提供同样保证。生产不读取这些内部状态文件，也不使用任何隐藏开关。

## 生产变化

`Claude.ObserveNativeExecution` 复用既有可选观察接口，仅调用固定版本 `--version` 与 `agents --json --all --cwd`。它要求已有规范化状态目录、项目和完整 UUID，查询使用保存的规范化 profile，前后重核原配置别名。短 ID 相同但完整 UUID 不同、重复 UUID、错误目录、缺失身份、未知版本或非法 JSON 均拒绝；清单输出限制为 4 MiB，查询具有期限。没有 fallback 到 SDK Observe、attach 或 resume。

返回 `View.agent_roster` 保留清单的声明。未知状态/等待原因归为 unknown，不透传未知错误正文。Runtime 不将 `working/idle/done` 或 PID 转换为完整执行/空闲证据；Application 对该形态返回 `status: sampled`，强制保留 `Status=unknown`、`ExecutionClear=false`、`Evidence.Complete=false`，不写 Session/Operation，不授予输入权。

已登记 Claude 只读来源可显式启动状态采样。界面分列完整 UUID、短作业 ID、分类、PID、state/status/waitingFor，说明分类不代表创建来源、PID 尚无操作系统身份核对、idle/done 不排除旧工具仍活或输出丢失。沿用每次查询结束 3 秒后再查、停止/离开/失败后取消后续查询与忽略迟到响应的规则。

本轮浏览器还实际发现：只有只读来源、没有供应商路由的合法工作区返回 `routes: null`，前端 `.find` 抛错并白屏。现在 Workspace 沿用 Snapshot 的空数组约定返回 `routes: []`，HTTP 回归保留无路由配置。[原始白屏](W01-04-native-status-browser-initial-failure.json)和[原始 Handler 响应](W01-04-native-status-http-before-routes-fix.json)保留。

## 验证结果

| 层次 | 实际结果 | 证据 |
|---|---|---|
| Runtime 边界 | 19 个场景通过：工作/空闲并列、终态/无进程、前台、等待、未知值、UUID/短 ID/重复/目录拒绝、异常/超限清单、版本、取消、稳定及改指别名；未启动 SDK | [最终 race 日志](W01-04-native-status-runtime-race.txt) |
| 真实 CLI / 合成后台记录 | 同完整 UUID、同项目、A/B 独立 profile 分别 working/stopped；生产 Runtime 读取后保持各自状态，未提升完整执行证据 | [结果](W01-04-native-status-native-runtime.json)、[日志](W01-04-native-status-native-runtime.txt) |
| 真实存活前台 / 生产 Runtime | 6 项检查通过；确认该自有 TUI 的合成 key 提示后，真实 Runtime 和直接 CLI 查询均得到原完整 UUID/PID，查询前后原进程仍活；没有模型输入或新 supervisor roster | [最终可重跑探测](W01-04-native-status-live-final.json)、[Runtime 投影](W01-04-native-status-live-final-runtime.json)、[日志](W01-04-native-status-live-final-runtime.txt) |
| Application / Handler | 原绑定及账本不变；即使受控 Adapter 提供 idle/完整证据，sampled 分支仍清除其完整/空闲语义；send/resume/reply/interrupt 均不能取得权限；缺 UUID 明确不可用；无路由投影兼容 | [应用](W01-04-native-status-app-tests.txt)、[HTTP](W01-04-native-status-http-tests.txt)、[响应](W01-04-native-status-http.json) |
| 浏览器 | 22 项通过：显式启动、重复采样、停止后不再查询、迟到响应丢弃、state/status 并列、完整/短 ID 分列、无 PID/等待/前台变体、缺 UUID 停止、无控制请求、无 JS 错误；三尺寸无横向溢出并检查可读 | [结果](W01-04-native-status-browser.json)、[脚本](W01-04-native-status-browser-script.js)、[1440](W01-04-native-status-browser-1440.png)、[768](W01-04-native-status-browser-768.png)、[390](W01-04-native-status-browser-390.png) |
| 回归 / 工程 | Runtime race 63 个顶层/259 个子测试通过（排除需监听的 OpenCode）；app/console race、带标签全包 vet、Web typecheck/Vite 与开发 Go 构建通过 | [Runtime](W01-04-native-status-runtime-race.txt)、[app/console](W01-04-native-status-app-console-race.txt)、[vet](W01-04-native-status-vet.txt)、[构建](W01-04-native-status-build.txt) |

浏览器使用当前构建与真实 Handler 导出，CLI、原生绑定元数据、历史、HTTP/EventSource 传输和状态变体受控；不覆盖真实浏览器到原生前台的完整端到端连接。新增真实前台证据只到 Runtime 层，合成后台记录不等于存活 supervisor/worker。浏览器断言曾遗漏原页面自动发起的只读历史分页请求；补齐真实 Handler 历史响应及 `{cursor:""}` 的精确请求断言后完整重跑通过。

Runtime 的完整套件默认跳过 opt-in 原生测试，上表真实测试单独执行。历史 690 份证据没有重跑或改写。

## 重跑

固定 Claude 2.1.280；本片 Node 24.13.0，Go 1.26.2。目录与进程均为自有测试资源：

```bash
export GOCACHE=/tmp/netcap-w0206-auth-go-cache
go test -race ./internal/runtime -skip '^TestOpenCode' -count=1
go test -race ./internal/app ./internal/console -count=1
NETCAP_NATIVE_CLAUDE_ROSTER=/home/zzw/.local/share/claude/versions/2.1.280 \
  NETCAP_NATIVE_CLAUDE_ROSTER_OUTPUT=/tmp/netcap-roster.json \
  go test -race ./internal/runtime -run TestClaudeNativeRosterReadsIsolatedRecords -count=1 -v
python3 tests/claude_roster_probe.py --output /tmp/netcap-live-roster.json
NETCAP_CLAUDE_ROSTER_HTTP_OUTPUT=/tmp/netcap-roster-http.json \
  go test -race ./internal/console -run TestClaudeRosterHTTP -count=1 -v
go vet -tags=nativecodex ./...
make dev-build
```

`tests/claude_roster_probe.py` 创建隔离配置和真实 TUI，只确认合成 key 提示，不提交模型提示；运行生产 Runtime 的 opt-in 测试后关闭该 TUI。输出旁边另存 `-runtime.json` 与 `-runtime.txt`。旧环境的真实后台生命周期实验未重跑。

原始[空配置脚本](W01-04-native-status-probe.py)与[合成记录脚本](W01-04-native-status-roster-fixture.py)作为实验来源归档，均需从仓库根目录运行，结果写入 `.dev/w0104-native-status/`。浏览器脚本使用配置的 Playwright MCP `browser_run_code_unsafe`；其 Handler 响应及构建资源属于本片，未来重跑先核对资源哈希，截图输出到 `.dev/`，不覆盖正式证据。

## 收尾与保留条件

源码与产物的归档时点哈希见 [manifest](W01-04-native-status-manifest.json)，变更见 [diff](W01-04-native-status-diff.patch)。[清理核对](W01-04-native-status-cleanup.json)确认 690 个旧证据与旧 `bin/netcap` 不变；探测自有 TUI 在 finally 中关闭，最终退出 143，探测整体退出 0。本轮浏览器标签已关闭；未停止共享后台、未发送外部消息、未提交或发布。

原生后台控制/交接、认证过的执行器身份、真实在途工具/审批完整性、worker 崩溃后的工具结果连续性、独立前台严格实时接管和 Windows 仍未验收。下一片继续 W01.04/W01.05 的这些条件，不能将 sampled 或清单里的 PID 当作已通过。W02.06 的原生 MCP send/EPERM 条件未变化，本片没有重跑同一受阻矩阵。
