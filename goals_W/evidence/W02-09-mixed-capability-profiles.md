# W02.09 · 四配置混合能力与显式版本隔离

日期：2026-10-10。修复混合改配预览把未变化的共享 MCP 列入差异的问题，并完成 Codex A/B、Claude A/B 同时选择共享/专属 Skill、共享/专属 MCP 和 CLI 插件的联合验证。每份执行保留原 ID、独立凭据与工作目录；共享 Skill 的新版本只在明确选择后生效。

本片为 **9/40 done，W02.07 / W02.08 / W02.09 in_progress，Goal active**。本地脚本化供应商验证协议、正文传递与真实工具调用，不证明自主方法采用或真实任务 Gate。完整来源、动态权限、其他插件/故障组合及 Windows 等首版硬要求继续保留。

## 用户可见修复

此前只禁用 Skill，也会出现 `identity：启用 → 启用`；完全不改选择也能生成候选差异。共享 MCP 新增的 `Definition` 是指针，查询副本中的等值定义有不同地址，结构体 `==` 因此误判。现在按完整内容比较 MCP 选择，资产版本仍单独比较；未变化的定义、环境引用及模式不再制造差异。

Application 回归通过真实创建、预览与保存入口，先捕获两种[失败](W02-09-mixed-capability-profiles/verification/mixed-preview-red.log)，再取得[通过结果](W02-09-mixed-capability-profiles/verification/mixed-preview-green.log)。保存 Skill 候选不改当前快照、MCP 绑定，也不增加原生创建或输入。

## 联合场景与依据

每份配置选择五项资产：同一条共享 MCP、自己的专属 MCP、同一条共享 Skill、自己的专属 Skill，以及适用于该 CLI 的 Skill/MCP 混合插件。使用只读权限和逐次 MCP 审批；所有实际模型请求都指向隔离空间内的模拟供应商。

| 层次 | 实际结果 |
|---|---|
| 四配置原生联合 | [547 项通过](W02-09-mixed-capability-profiles/verification/native-expanded-second.log)。四份模型请求实际重叠；共享、专属与插件方法正文到达各自模型，精确工具结果回到原调用。八次组合禁用/启用、两次 Skill 显式升级和两次工作台重启均保持原 ID、历史与未选择资源。 |
| 选择与排除 | 实际 Codex 工具注册表和 Claude 模型工具定义均与本轮选择一致；组合禁用期间插件 MCP 不暴露，专属 Skill 与显式 MCP 继续工作。每次实际 MCP 调用只取得一次本轮审批。禁用不宣称删除历史中的 Skill 正文。 |
| 依赖与版本 | 共享 Skill 相对资料变化拦截四份输入；Claude A 专属资料变化只拦截 A，其余三份仍实际运行。还原资料后继续同一执行。登记共享 Skill v2 不改任何旧快照；先仅升级 Codex A，其余三份仍 v1，再仅升级 Claude B，最终 A/B 两类各保留一份 v1 和一份 v2。重启后版本和正文归属一致。 |
| 浏览器 | [86 项通过](W02-09-mixed-capability-profiles/verification/browser-second.json)。使用真实 HTTP 帧和经过生产 Handler 核对的完整表单请求/预览，验证五项选择、精确差异、候选与生效版本、历史、依赖影响、明确升级和 390px 布局。没有派发原生命令或模型输入。 |
| 原始材料复核 | [609 项通过](W02-09-mixed-capability-profiles/verification/audit-third.json)。只读 SQLite、HTTP 帧、原生日记/rollout、资源字节、原始模型请求、工具日志和审批。最终联合运行共 **120 次本地模型请求、48 次原生输入、48 次实际 MCP 调用、48 次单次审批**；每份原生日记恰有 12 条输入。 |
| 原共享 MCP 回归 | [206 项通过](W02-09-mixed-capability-profiles/verification/shared-regression.log)。复用夹具新增工具注册表观察和未知状态等待后，原共享/专属 MCP、原 ID 改配与隔离场景继续通过。此运行与上述 120 次请求分开计数。 |
| 常规检查 | [app/runtime/console 全包 race](W02-09-mixed-capability-profiles/verification/race-first.log)通过，分别为 151.473s / 43.558s / 2.322s；[双 native 标签 vet](W02-09-mixed-capability-profiles/verification/vet-all-tags.log)、开发构建、Go 格式、Python/JavaScript 语法及 diff 检查通过。 |

Codex 的每次显式 Skill 输入还核对 `operation_id`、快照、资源版本、正文 SHA-256 和 `loaded_exact` 记录，并与原始请求中的正文对应。Claude 的原生日记记录实际 Skill 工具调用，首次选用的正文在调用前不存在、调用后到达模型；供应商明确安排了调用顺序。相对资料在本片作为资源依赖核对，没有执行资料 Read 或真实方法 Gate。

已查看[待应用差异](W02-09-mixed-capability-profiles/verification/browser-second/mixed-pending-1440.png)、[Claude 来源](W02-09-mixed-capability-profiles/verification/browser-second/claude-mixed-1440.png)和[390px Skill 版本](W02-09-mixed-capability-profiles/verification/browser-second/codex-mixed-390.png)。界面沿用现有实现，本片没有修改 Web 源码或构建资产。

## 失败反馈与边界

除上述生产比较错误外，测试中的失败也保留：

- 首轮审批 command ID 含 `/` 而被接口拒绝；改用合法独立 ID。第二轮将 Claude 插件的状态服务名 `plugin:suite-claude:bundle` 误作工具名，按真实请求改为 `mcp__plugin_suite-claude_bundle__whoami`。
- 第三轮在输入约 200 毫秒后把 `uncertain` 当终态，提前关闭夹具。原日记已记录 `started` 与 `requesting`，没有失败回执。现沿原期限继续观察同一操作；[基础 216 项](W02-09-mixed-capability-profiles/verification/native-fourth.log)和最终联合运行都记录了 `acknowledged → uncertain → running → succeeded`，没有重发输入。生产未知状态语义没有改变。
- 扩展首轮已收到依赖漂移的 HTTP 409，夹具错误处理却写入只读 `HTTPError.reason`；改用可写的 `msg` 后完整重跑通过。
- 浏览器首轮的工具运行环境没有全局 `URL`，路由解析改用已有字符串方式；第二轮 86 项通过。原材料复核先误要求初始快照显式带 `revision`，随后把 MCP 数组排序变化误作绑定变化；按已有首版版本语义及资产/服务键逐项比较后，609 项通过。失败结果不覆盖。

固定版本为 Codex CLI 0.157.1、app-server 0.158.0、Claude Code 2.1.280、SDK 0.3.280。沿用已有接口与文档依据，没有新增第三方 API 实现。基线 `5a0139e`；原生测试二进制和最终 `.dev/bin/netcap` 均为 SHA-256 `4facfa3d1a06d57ef04a198392d323c74bb5587f16ebcaba2eaf1db7c68c3bea`，见[manifest](W02-09-mixed-capability-profiles/manifest.json)。

所有实验使用 `/var/tmp` 自有目录、bwrap user/PID/net/mount 隔离、自有策略、模拟注册表和本地供应商；自有实验进程与浏览器页面已收尾。旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，依赖、锁文件、bridge、Web 和既有证据保留。

本片没有开放 Codex 共享 MCP 的新增改配、恢复继承、同名定义版本替换或远程传输，也不补造完整已加载命令/环境证据。完整受管/remote/helper 来源新鲜度、动态权限与外部竞态、全部 hook/插件组合、共享后台多客户端与输入交接、故障恢复矩阵、真实方法/Gate 和 Windows 第二环境仍需后续验收；不能据本片勾选整个 W02 或 P11–P15。

## 复跑与归档

```bash
go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
python3 tests/mixed_capability_profiles_native.py --netcap "$PWD/.dev/bin/netcap"
python3 tests/mixed_capability_profiles_audit.py --native <输出的Fixture目录> --output <审计JSON路径>
python3 tests/shared_mcp_profiles_native.py --netcap "$PWD/.dev/bin/netcap"
go test -race ./internal/app ./internal/runtime ./internal/console -count=1
go vet -tags=nativeclaude,nativecodex ./cmd/... ./internal/... ./tests/...
```

浏览器入口 `tests/mixed_capability_profiles_browser.js`：`__FIXTURE__` 为最终联合脚本的 `mixed-result.json`，`__ASSETS__` 为 `web/dist` URL 到文件的映射，`__OUTPUT__` 为截图目录，通过 Playwright 执行。它比较实际表单请求与原生测试保存的生产 Handler 请求，再回放对应真实预览。

最终联合原目录为 `/var/tmp/netcap-mixed-capability-profiles-64zq1rb_/netcap-console-_hforkiz`，共享回归目录为 `/var/tmp/netcap-shared-mcp-profiles-r_aw2d4k/netcap-console-9l2j43ol`。失败和成功运行、源码与构建副本、日志及截图均列入[归档索引](W02-09-mixed-capability-profiles/archive-index.json)，Go 源码副本使用 `.go.gz`。Git 同步源码、测试、本报告和进度；原始材料与开发产物由已有 `.gitignore` 保留在本地。
