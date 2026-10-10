# W02.06 · 动态认证命令与原生来源边界

日期：2026-10-01。接通 Claude HTTP/SSE MCP 的 `headersHelper` 校验和原生传递。**15 种组合、58 项真实 CLI 连接前检查通过；远程请求认证与模型工具链路尚未验收。** W02.06 仍为 in_progress，done 7/40，Goal active。

## 实现

- `headersHelper` 只接受非空、无 NUL 的字符串，用于 HTTP/SSE；stdio 和其他未知字段仍拒绝。新建/候选选择复用资源来源检查与固定引用，命令、展开凭据和 helper 输出不进入快照。
- 生产 `mcpDefinitions` 原样传递原生命令，不在 Go/bridge 执行或预展开；禁用项不注入。原生 CLI 使用所选项目 cwd，shell 相对路径不改写成资源目录。能力库诊断提示运行环境绑定，并拒绝无效 helper 字段。
- `mcpInventory` 继续只投影名称、状态、来源和工具名，原始 config/error 不外传；工具调用仍沿用原单次 ask 规则。相同资产在 A/B 的命令使用各自配置环境。
- 独立 OAuth 准备会改变工作目录，当前对带 helper 的定义明确拒绝，避免相对命令静默指向错误目录。原生 OAuth 与 helper 的组合流程保留为未完成项。
- `make claude-auth-contracts` 增加字段传递、禁用、不执行/不展开、非法字段与诊断脱敏检查。真实网络验收入口增加 `--headers-helper`：静态认证头故意使用失效环境引用，要求 helper 提供正确认证；另有失效 helper、A/B、cwd 和重启后再次执行检查。

## 固定版本的实际边界

Claude Code **2.1.280**、Agent SDK **0.3.280**、Node **24.13.0**、Go **1.26.2**。SDK 的公开 `McpHttpServerConfig` / `McpSSEServerConfig` 类型没有 `headersHelper`；真实 SDK 运行时仍传递该字段，固定 CLI 会执行。生产通过已校验的额外属性传递，没有更换 SDK 或改写安装包。

通过 `find-docs`/Context7 核对 [Claude MCP 文档](https://code.claude.com/docs/en/mcp)。文档说明项目/本地来源 helper 需要对应项目的持久信任，SDK 对 hooks 的自动信任不能代替它，见[文档摘录](W02-06-headers-helper-docs.txt)。具体以下列实测为准：

| 来源 | helper 是否执行 | 原生环境与路径 |
|---|---|---|
| 显式动态选择，HTTP/SSE，A/B | 执行 | 各自 cwd、配置目录和测试凭据环境正确；通过生产定义转换后传入真实 SDK |
| 同一动态资产禁用 | 不执行 | 原生清单无该项，没有连接 helper 的副作用 |
| 项目来源，无信任 | 不执行 | 原生提示 `headersHelper not run`，保留 project 来源 |
| 项目来源，只信任父目录 | 不执行 | 父目录信任不代替原生项目根的信任 |
| 项目来源，信任实际项目根 | 执行 | cwd/配置保持；`NETCAP_HELPER_KEY` 被原生清理，不能把动态来源的凭据可见性套用于项目来源 |
| 动态选择的相对脚本 | 执行 | `./project-helper.py` 从所选项目解析，不从能力资源目录解析 |

初始无 Git 的临时目录实验中，原生信任提示指向 `/tmp`，而不是传入的叶子 cwd；直接给叶子目录写信任并没有使 helper 执行。后续在各自自有临时目录初始化 Git，明确项目根后验证无信任/父目录/项目根三种情况。信任写入只发生在自有测试配置目录，生产代码不修改用户信任记录。项目来源清理测试凭据也是实测结果，未通过额外注入补回。

本探测不提交模型输入，没有启动监听服务；MCP URL 为不可达的 loopback 端口，实际连接状态都是 `failed`。这允许在受限环境下确认连接前的原生命令行为，**不能证明生成的认证头已被远程服务接受、动态头覆盖规则、令牌轮换或工具结果返回模型**。

## 验证结果

| 层次 | 实际结果 | 证据 |
|---|---|---|
| 原生连接前边界 | **58/58**，覆盖上表 15 种组合，没有模型 result；被排除/未信任的 helper 没有执行 | [完整结果](W02-06-headers-helper-native.json) |
| 生产 Application | 字段正反例、固定引用、不复制连接载荷、helper/cwd 不适用于 OAuth 侧目录、能力库运行绑定诊断通过 | [Go 定向](W02-06-headers-helper-app-tests.txt) |
| Bridge | 原样传递、不执行、不展开、排除、无效输入和原始 config/error 脱敏通过；OAuth 诊断及 **10 项**恢复生命周期回归通过 | [结果](W02-06-headers-helper-bridge-tests.txt) |
| 来源与工程 | **7 项** MCP 来源核对、相关 Application/Runtime **race**、全包 **vet**、bridge TS/web typecheck/Vite/Go 构建及脚本语法检查通过 | [来源](W02-06-headers-helper-source-tests.txt)、[race](W02-06-headers-helper-go-race.txt)、[vet](W02-06-headers-helper-go-vet.txt)、[构建](W02-06-headers-helper-build.txt) |
| 完整 HTTP 认证流程 | **受阻**：启动第一个本地供应商 socket 时 `PermissionError: [Errno 1] Operation not permitted`，尚未运行到 CLI | [受阻记录](W02-06-headers-helper-full-blocked.txt) |
| 完整 SSE 与浏览器 | 未执行；保留完整网络链路和浏览器环境缺口 | [前片限制](W02-06-oauth.md) |

实施前生产 Application 的 HTTP/SSE helper 正例均被未知字段检查拒绝，见[失败记录](W02-06-headers-helper-app-red.txt)。加入有界字段支持后同一入口通过。探测中的原生信任失败记录保留为[早期结果](W02-06-headers-helper-trust-before.json)，没有改写为通过。

源码/构建/附件哈希、原始 `/tmp/netcap-claude-helper-*` 目录和进程收尾见 [manifest](W02-06-headers-helper-manifest.json)。只使用自有配置、Git 测试目录、合成凭据和 `.dev/bin/netcap`；没有调用真实供应商、停止共享后台、提交或发布。

## 重跑与后续

```bash
make claude-auth-contracts
python3 tests/claude_mcp_helper_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/runtime -run 'TestClaude|TestMCPLogin|TestCapability' -count=1 -timeout=120s
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 tests/claude_remote_mcp_smoke.py --transport http --headers-helper
python3 tests/claude_remote_mcp_smoke.py --transport sse --headers-helper
```

继续补齐动态头完整认证/失效/重连、helper 与 OAuth 登录目录组合、完整受管/插件/账号来源和权限冲突。已有原 ID 不重启恢复、SSE 完整 OAuth 准备及新增 UI 的原生/浏览器验收缺口保持。可以继续用本地 stdio 核对不依赖远程网络的来源规则；其余 W 系列硬要求没有因连接前检查通过而缩减。
