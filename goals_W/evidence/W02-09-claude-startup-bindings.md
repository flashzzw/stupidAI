# W02.08 / W02.09：受管来源中的启动绑定冲突

日期：2026-10-09。状态：**progress**。普通 Claude 创建和插件设置预览现在会拒绝已观察到的路由、凭据、状态目录及环境格式冲突；W02.07–09 仍为 `in_progress`，整体 **9/40 done，Goal active**。

## 问题与范围

已有 Go 检查覆盖 user/project/local/custom 文件，却没有检查受管来源中的同类声明。固定 CLI 2.1.280 / SDK 0.3.280 的隔离实验确认，旧准入可以允许这些声明进入原生启动。读取成功不能证明登记的路由和状态目录仍被采用。

本轮把现有约束扩展到读取器已经观察到的声明，没有开放完整受管策略执行或已安装插件正式选配。`read_complete` 继续只表示这次读取的状态，`complete` 始终为 `false`；某条声明被观察到也不表示它最终生效。

所有原生受管实验在自有 `bwrap` user/PID/network/mount namespace 内运行，把自有目录只读映射到 `/etc/claude-code`；宿主策略和用户配置未改。Windows 注册表在原生夹具中为受控不存在，SDK 夹具另注入明确的注册表正文与失败条件，**不算真实 Windows 或企业策略验收**。仅使用假凭据和自有本地供应商。

## 实现

- `settings_preflight.ts` 从实际读取的 JSON 文件、注册表正文、SDK 来源层和预解析合并值提取 `startup_conflicts`。即使 SDK 丢弃无效 `env`，或者 HKCU 声明没有进入最终合并值，读取观察仍会保留冲突。
- 类别限定为 `api_key_helper`、`route_environment`、`state_environment`、`invalid_environment`。沿用既有 Go 边界：`apiKeyHelper`、`ANTHROPIC_*`、`CLAUDE_CODE_USE_*`、`CLAUDE_CONFIG_DIR`、`CLAUDE_CODE_PROJECT_DIR_NAME`、`HOME`、`XDG_*`，以及不是字符串映射的 `env`。输出只有类别和来源，不输出变量名、值或 helper 命令。
- Go 要求 worker 明确返回非 null 数组，并校验数量、类别、来源、绝对路径和注册表 hive。旧 worker 缺失该字段不能被当成“没有冲突”。创建和预览复用现有准入，在私有文件和原生校验进程创建前拒绝；原 `policyHelper` / `policyHelpers` 守卫保留。
- 界面单列冲突类别与文件、注册表或解析来源，并保留读取不完整、辅助配置和完整权限的限制。刷新失败和断线不继续展示旧观察。

最终源码及差异见 [manifest](W02-09-claude-startup-bindings/manifest.json)、[source.diff](W02-09-claude-startup-bindings/source.diff)。

## 实际反例与修复结果

普通创建采用真实 Application/Handler。因果对照仅用 Go overlay 把 `claude_settings_validation.go` 换成切片前文件，读取器和夹具为当前版本，**不是整个旧版本二进制**。原始 overlay、测试二进制哈希及切片前源码均保留。

| 受管声明 | 旧准入的实际结果 | 当前准入的实际结果 |
|---|---|---|
| `ANTHROPIC_BASE_URL` | 创建成功；启动 hook 的 `route_matches=false`；登记端点 0 次，另一自有端点 2 次请求 | `not_sent`、NativeID 空；无启动 hook，两个端点均 0 次请求 |
| `CLAUDE_CONFIG_DIR` | 创建成功；启动 hook 的 `profile_matches=false` | `not_sent`、NativeID 空；无启动 hook 或端点请求 |
| `apiKeyHelper` | 创建成功；实际 helper marker 存在 | `not_sent`，无 helper、hook 或端点请求 |
| 数值型 `env` 值 | 创建成功；原生忽略无效值，启动 hook 仍执行 | `not_sent`，无 hook 或端点请求 |
| 普通字符串环境变量 | 创建成功，profile/route 两个核对值均为 true | 同样成功，两个核对值均为 true |

这些创建用例没有模型输入。端点计数是启动探测，不能称模型请求或凭据外泄；该夹具用非 HEAD 计数断言核对无模型输入，没有保存逐请求 method 原文。Application 预览夹具另保存了唯一一次创建 `HEAD /api/hello` 的原始 method/phase 记录，预览没有增加请求。

原始结果与只读 SQLite 投影见 [旧创建](W02-09-claude-startup-bindings/raw/create-old-admission/result.json)、[当前创建](W02-09-claude-startup-bindings/raw/create/result.json)。各场景目录保存真实 hook、helper marker、策略文件和数据库投影。

## 验证

| 层次 | 实际结果与依据 |
|---|---|
| SDK 文件/注册表观察 | 46 场景、537 项断言通过；最初三个场景的缺失冲突红灯保留。涵盖无效 env 被 SDK 丢弃、HKCU 非最终层声明及普通环境对照，见 [原始结果](W02-09-claude-startup-bindings/raw/sdk/result.json) |
| 原生 Runtime 准入 | 18 场景、210 项通过；切片前 37 项失败转绿。九类冲突没有原生启动记录，benign 仍可校验，见 [红灯](W02-09-claude-startup-bindings/raw/runtime-red/result.json)、[绿灯](W02-09-claude-startup-bindings/raw/runtime/result.json) |
| 真实 Application/Handler 预览 | 82 项通过；原 NativeID、快照、人工接手和暂停保留，只有原查询的三个启动 hook，恢复普通策略后预览可用，见 [结果](W02-09-claude-startup-bindings/raw/application/result.json) |
| 真实普通创建 | 当前五例 25 项通过；旧准入对照 9 项失败，原始副作用见上表 |
| Go 边界 | 定向日志 93 条 PASS 记录，包含顶层及子用例；`internal/runtime`、`internal/app`、`internal/console` 全包 race 通过，耗时分别 36.821s、114.141s、2.098s |
| 工具回归 | 当前 `.dev/bin/netcap` 的 MCP 39 项、插件 33 项通过；真实工具调用、独立绑定、逐次审批、原 ID 恢复和漂移拒绝保留 |
| 原生设置传输 | `claude_settings_validation_test.mjs` 18 项通过 |
| 浏览器 | 18 项通过；使用真实 Handler 响应回放，读取失败和断连为受控条件。1440 / 390 截图已目视核对，只有显式失败样本的一个 HTTP 409，无 JavaScript 错误 |
| 静态与构建 | tagged `go vet`、gofmt、Python/JavaScript 语法、bridge、Web 类型检查和构建、开发二进制构建通过 |
| 独立原始材料核对 | 146 项通过；同一代理另行核对声明、marker、端点计数、原生调用、SQLite 和基线哈希，不以汇总 `passed` 代替原始事实，见 [审计](W02-09-claude-startup-bindings/audit.json) |

全包 race 后新增的 tagged 普通创建夹具已单独编译和实际执行，随后 tagged vet 也通过；生产代码没有再变，未重复全包 race。恢复上下文时直接取得四个原有进程的退出 0，没有因等待重启任务，见 [终态记录](W02-09-claude-startup-bindings/command-results.json)。

审计首轮错误地把 Runtime 的无输入 `--bare` 放行等同于 helper 已执行。原始 marker 不支持这个推断；已保留 [首轮失败](W02-09-claude-startup-bindings/audit-first.json)，改为分别核对 Runtime 的实际放行和普通创建中的实际 helper 执行。检查 SDK 空 stdout 的读取假设也已修正为读取 fixture 的 `result.json`，见 [检查修正记录](W02-09-claude-startup-bindings/inspection-notes.json)。这些是检查脚本假设错误，没有据此修改生产行为或重跑原生任务。

界面证据：[桌面](W02-09-claude-startup-bindings/binding-conflict-1440.png)、[窄屏](W02-09-claude-startup-bindings/binding-conflict-390.png)、[请求与断言](W02-09-claude-startup-bindings/browser.json)。

## 复跑入口与产物

在仓库根目录执行；原生入口自建策略 namespace，需要本机已经固定的 Claude 2.1.280、SDK 0.3.280 和可用 `bwrap`。普通 Go 测试的临时目录留在仓库外，避免改变来源图。

```sh
make claude-build web-build
go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
python3 tests/claude_startup_preflight_probe.py
python3 tests/claude_policy_helper_admission_probe.py --surface runtime
python3 tests/claude_policy_helper_admission_probe.py --surface application
python3 tests/claude_policy_helper_admission_probe.py --surface create
go test -race ./internal/runtime ./internal/app ./internal/console
go vet -tags nativeclaude,nativecodex ./cmd/... ./internal/... ./web
node tests/claude_settings_validation_test.mjs
python3 tests/claude_mcp_selection_smoke.py --netcap .dev/bin/netcap
python3 tests/claude_plugin_execution_smoke.py --netcap .dev/bin/netcap
```

SDK 支持 `--worker` 指向切片前读取器；普通创建的旧准入对照使用 [overlay](W02-09-claude-startup-bindings/old-admission-overlay.json) 编译测试二进制后传给 `--surface create --binary`。浏览器源为 `tests/claude_startup_bindings_browser.js`，实际已执行替换后的脚本和 Handler fixture 一并归档。原始自有目录见 [目录索引](W02-09-claude-startup-bindings/original-directories.json)，复制与脱敏规则见 [归档脚本](W02-09-claude-startup-bindings/archive.py)。官方设置文档复用上一片固定归档与哈希，没有重复外部检索，见 [资料来源](W02-09-claude-startup-bindings/documentation.json)。

归档时 `.dev/bin/netcap` SHA-256：`54e08a3737997c9877d2f6a88813ca498306f80c34c9cc8bc8bf4ae8b508ca89`。旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。其他构建、源码和夹具哈希见 manifest，复制来源见 [archive-index](W02-09-claude-startup-bindings/archive-index.json)，报告及附件见[完整证据哈希](W02-09-claude-startup-bindings/artifact-hashes.json)。

9,130 份旧 W 证据、31 份旧 goals 证据、硬要求文档、无关 bridge 与依赖未变。没有复制完整数据库、账号文件或控制台访问 token；控制台访问设置行已脱敏。自有原生/回归目录没有存活进程或私有 settings 遗留，浏览器已关闭；未提交或推送。

## 剩余条件

当前只补齐**已观察声明中的启动绑定冲突**。完整受管/remote 来源的真实性与新鲜度、helper 执行准入、有效权限、读取后竞态和已运行查询的动态策略仍未完成。正式已安装插件持久候选、原 ID 应用事务与故障恢复、四配置联合、真实方法/Gate、独立前台、共享后台与输入独占、Windows 第二有效环境和全部首版条件继续保留。

下一片在现有证据上继续完整启动准入与持久候选的连接；不把这次守卫修复或普通环境正例提升为完整配置支持。
