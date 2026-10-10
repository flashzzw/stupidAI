# W02.08 / W02.09 · 按平台策略辅助配置的来源与启动准入

日期：2026-10-09。W02.07/08/09 仍 **in_progress**，整体 **9/40 done、Goal active**。

## 问题与结果

固定 SDK 保留了受管来源里的 `policyHelpers`，生产读取器却只报告单数 `policyHelper`。这会让普通创建的设置校验和已安装插件的设置预览继续启动原生 CLI。真实隔离策略目录中，`policyHelpers.linux.path` 和 `.script` 两例均在 `--bare` 校验时执行了自有辅助程序；每例预览和创建各执行一次。原始失败已经重新复现，见[原生红灯](W02-09-claude-policy-helpers/resumed-native-red.log)及[实际调用与标记](W02-09-claude-policy-helpers/raw/runtime-red/result.json)。

现在读取结果分别保留单数与复数声明，并保留每个来源层的标记。Go 创建校验和预览在生成候选文件、启动原生校验前拒绝尚未核对的辅助配置；读取仍可用。界面说明按平台配置可能执行程序或采用备用策略，本轮两者都没有执行或应用。

另外补出了旧 worker 省略字段、或返回 `null` 时被 Go 默认解码成 false 的问题。四个[缺失证据红灯](W02-09-claude-policy-helpers/missing-policy-evidence-red.log)已修复：有效设置和每个来源层都必须返回明确的 `policy_helpers_present` / `has_policy_helpers` 布尔字段。旧 bridge 会收到需更新的错误，不能把未观察当作没有策略。

这片接续了工作区中尚未归档的测试和失败材料。开始时核对已有文件、日志和 OS 进程，上一轮属于 **progress**，没有仍在运行的原生工作。复用已有探针、私有设置文件、Application/Handler 和原查询清理流程，没有改动原 ID 配置事务。

## 实际验证

| 层次 | 实际结果与范围 |
|---|---|
| 固定 SDK 与生产读取器 | **38 例、444 项通过**。含单数、Linux/WSL/其他平台、脚本、静态/备用值、空对象、null 和组合声明，保留原文件/注册表/读取竞态回归。原始正文中的声明与 SDK 来源、输出标记分别核对。 |
| Runtime 与真实 CLI | **8 例、89 项通过**。辅助配置先于私有候选和原生校验被拒绝；普通受管权限配置仍能完成校验。最终 path/script 两例均无原生启动、无辅助程序标记。 |
| 真实 Application/Handler | **58 项通过**，其中包含原预览的 33 项和新增策略场景。真实安装登记、原会话、快照和人工接管保持；四类策略均只读可见、预览提前拒绝，恢复普通策略后可再次预览。原查询仅有 project/custom/plugin 三个启动 hook。 |
| 普通创建回归 | **25 项通过**。三种无效来源没有执行查询或启动 hook；两种有效配置保留原 user/project hook。 |
| 真实工具协议回归 | MCP **39 项**、插件 **33 项通过**。A/B、原 ID 恢复、单次审批、拒绝、来源漂移、实际工具/hook 和结果返回均使用自有本地供应商。 |
| 浏览器 | **20 项通过**。回放本轮最终 Handler 响应；“只有来源层声明”、读取失败和断线为受控界面条件。验证声明与准入分离、未创建临时文件、旧结果清除和 390px 布局。无 JavaScript 页面错误；控制台唯一 409 来自显式失败样本。 |
| 工程检查 | runtime/app/console 全包 race、nativeclaude/nativecodex tagged vet、bridge/Web 类型检查与构建、开发二进制构建通过；独立校验传输 **18 项通过**。 |
| 独立核对 | **120 项通过**。重读原始策略字节、SDK 输出、CLI 调用记录、hook/MCP 输出、供应商请求、SQLite 只读投影和浏览器请求；另核对当前产物及全部旧证据哈希。 |

原生策略测试使用 `bwrap` 的独立 user/PID/network/mount namespace，把自有目录只读映射为 `/etc/claude-code`。主机策略没有写入；WSL 注册表入口被隔离并明确返回受控的 HKLM/HKCU 缺席，**不是实际 Windows/企业策略验收**。真实 CLI 为 **2.1.280**，SDK 为 **0.3.280**；无真实供应商调用。

Context7 本轮返回 fetch failed。随后直接取得[官方设置说明](https://code.claude.com/docs/en/settings)、[受管策略](https://code.claude.com/docs/en/managed-settings)和[policyHelper 参考](https://code.claude.com/docs/en/settings-reference#policyhelper)，原文与[查询记录](W02-09-claude-policy-helpers/documentation.json)已保存。公开文档和固定 SDK 类型只描述单数入口；复数行为依据固定 CLI 的实际控制请求和辅助程序标记，不冒称公开 SDK 已定义该字段。

## 失败、复验与产物

所有初始失败保留。第一轮 Go 回归把 `TMPDIR` 放在仓库内，改变了 Git 来源图，六个原有文件数量断言失败；恢复原有临时目录隔离后通过。新原生 Application 夹具显式初始化自有 Git 根，后续工具回归使用自有 `/var/tmp` 目录，必要材料均已归档。

浏览器最初错误要求受管来源一定含 `path`，而 SDK 只给该层的 origin/hash；修正为检查真实可用字段。第二次在加载中的 status 出现时过早检查结果，改为等待结果时间后通过。两份失败脚本与错误保留，没有调整产品来掩盖测试问题。

~~~bash
npm --prefix bridges/claude run build
python3 tests/claude_startup_preflight_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -c -race -tags nativeclaude \
  -o .dev/w0209-policy-helper-admission-work/runtime-release.test ./internal/runtime
python3 tests/claude_policy_helper_admission_probe.py \
  --binary .dev/w0209-policy-helper-admission-work/runtime-release.test
python3 tests/claude_policy_helper_admission_probe.py \
  --binary .dev/w0209-policy-helper-admission-work/runtime-release.test --surface application
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime ./internal/app ./internal/console
NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude GOCACHE=/tmp/netcap-w0206-auth-go-cache \
  go test -tags nativeclaude -race ./internal/app -run '^TestClaudeNativeCreateRejectsInvalidSettingsBeforeStartup$' -count=1 -v
node tests/claude_settings_validation_test.mjs
npm --prefix web run build
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude,nativecodex ./cmd/... ./internal/... ./web
GOCACHE=/tmp/netcap-w0206-auth-go-cache go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
python3 tests/claude_mcp_selection_smoke.py --netcap .dev/bin/netcap
python3 tests/claude_plugin_execution_smoke.py --netcap .dev/bin/netcap
python3 goals_W/evidence/W02-09-claude-policy-helpers/audit.py --current
~~~

`--current` 核对当前源码/构建；后续版本改变后，去掉该选项可只复核已归档证据。浏览器通过 Playwright MCP 执行[最终脚本](W02-09-claude-policy-helpers/browser-release.js)，[20 项结果](W02-09-claude-policy-helpers/browser-release.json)、[桌面](W02-09-claude-policy-helpers/policy-source-1440.png)和[窄屏](W02-09-claude-policy-helpers/policy-source-390.png)可复核。

- 开发二进制 SHA-256：`601908c7727466f31cc483b919f58abc3ed056a6fe28e8fcfea48720a711458e`。
- settings_preflight.js SHA-256：`0a591d307071728c53e1022f7d882596205c6b78b860c0c38e88652202106390`。
- 旧 bin/netcap 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`；**8652** 份旧 W 证据及 **31** 份旧 goals 证据不变，其他 bridge 与依赖不变。

自有进程、浏览器和私有设置文件均已收尾，未提交或发布。原始目录为 `.dev/w0209-policy-helper-admission-work` 及其记录的自有回归目录；[归档索引](W02-09-claude-policy-helpers/archive-index.json)、[源码差异](W02-09-claude-policy-helpers/source.diff)、[manifest](W02-09-claude-policy-helpers/manifest.json)、[审计](W02-09-claude-policy-helpers/audit.json)和[收尾](W02-09-claude-policy-helpers/closeout.json)记录本次版本。未复制完整数据库、账号文件或控制台访问凭据。

## 尚未满足的条件

本片只补全已观察声明的拒绝边界，**不等于完整支持策略辅助配置**。受管/远端来源的真实性和新鲜度、辅助程序的执行准入、有效权限、读取后竞态、已运行查询的策略变化、正式已安装插件选择和原 ID 持久事务仍未完成，`complete` 继续为 false。

下一片复用当前隔离策略探针，核对受管来源的启动环境/凭据覆盖及有效配置边界，再连接持久候选与四配置联合。真实方法/Gate、独立前台、共享后台/输入独占、Windows 第二环境和全部首版硬要求继续保留。
