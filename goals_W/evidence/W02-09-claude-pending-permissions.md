# W02.09 · 原工具审批与原生策略变化

日期：2026-10-10。已修复**观察到权限规则变化后，仍可批准旧工具请求**的问题，并补齐来源变化后的原请求拒绝路径。W02.09 保持 **in_progress**，W 系列仍为 **9/40 done，Goal active**；完整动态权限、来源与外部竞态要求继续保留。

## 复现与交付行为

固定 Claude Code **2.1.280**、SDK **0.3.280** 的原查询先按受管 `ask` 规则请求读取自有文件。审批等待期间，将同一受管来源改为 `deny`，等原查询清单报告新规则，再批准原 request ID：旧实现仍读出文件。延迟批准 3 秒以及改用不附加只读审批 hook 的 workspace 配置，均复现相同结果；规则不变的对照通过。真实 Application → Handler → Runtime → SDK → CLI 路径随后也复现，失败材料保留。

当前实现为每条明确输入采样权限规则，并把观察身份、配置摘要、时间和规则观察摘要写入原输入日记。收到原生工具审批和批准前沿同一查询复核，不解释规则字符串，也不创建额外查询或模型输入。保留重复项、未知来源、忽略标记、目录与错误；菜单排序不构成变化。观察到变化、报告不可用、解析错误或冲突时，原请求不能批准。已观察到的变化不会因文件或清单还原而清除。

审批仍保留原 request ID、连接、工具参数和输入归属。网页显示不可批准的原因，并保留可用连接下的“拒绝本次”；也可明确中止原输入。拒绝仅略过会阻止收尾的来源版本读取，仍核对原快照、路由、凭据、状态目录、会话/连接/请求及唯一 deny 选项。拒绝只使用已有 bridge，不启动或恢复查询。并发允许/拒绝只完成原请求一次；没有自动拒绝、自动重发或工具重试。

缺少原输入观察、绑定不一致或 bridge 未声明 `pending_permissions: 1` 时不能批准。允许前的观察必须成功写入日记；明确拒绝不依赖诊断写入。原输入结束后，下一条明确输入取得新观察，不将前一条的失效永久施加到整个会话。

`pending.permission_guard` 的 `observed / changed / unavailable` 是观察状态，始终 `complete=false`。审批摘要使用独立的 `permission_approval_observation_v1` 格式，与面板 `sample_hash` 不是同一种摘要。字段、使用方式及限制同步在 [CONSOLE](../../CONSOLE.md)。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| 原生 Application / Handler / SDK / CLI | **7 场景、158 项通过**：不变、ask→deny、变化后还原、无效受管规则、用户解析错误、本地来源变化、原输入中止 |
| 原始材料独立审计 | **216 项通过**：188 次原查询权限采样、wire、bridge 日记、配置摘要、HTTP 回执、快照与供应商请求交叉核对 |
| 浏览器 | **26 项通过**：最终 Web 产物回放真实 Handler 数据，允许/拒绝携带原身份；1440/390 视口、还原、未知版本、已提交与断线边界 |
| SDK / 公共传输接缝 | **91 项通过**：新增17项审批场景及原 hook 清单27、指令记录33、审批hook4、认证恢复10项回归 |
| Go | **40 个相关子场景通过**；runtime/app/console 三包完整 race，随后补充的3个 bridge 声明场景定向 race 通过；双 native 标签的 runtime/app/console/cmd/netcap vet 通过 |
| 原权限清单回归 | 原生 **78 项**、原材料复核 **434 项**通过；本次包含53次显式面板读取，数量随热更新采样收敛变化 |
| 构建 | bridge TypeScript、Web TypeScript/Vite、开发 Go 构建通过 |

七个最终场景各使用独立 A/B profile 与原生会话。A 合计 **7 条原始输入、13 次本地脚本化模型请求、7 个 Read 和7个对应工具结果**；只有不变场景允许一次。其他场景明确拒绝或中止，文件正文未返回。B 无模型请求。每个场景保持原会话 ID、执行快照与独立状态归属；只读审计没有重放输入或审批。

浏览器使用真实生产资产与已捕获的 Handler 响应，发送三条受控回复以核对参数；没有重新操作原生会话。未知版本、已提交状态与浏览器断线属于显示契约夹具。桌面截图见本地归档的 `work/browser-final/changed-1440.png`；390视口中已实际点击拒绝，横向无溢出。

最终场景目录及逐项结果在本地归档 `work/native-final-matrix.json`，主审计为 `work/raw-audit-final.json`。原清单回归目录 `/var/tmp/netcap-permission-catalog-u4akps87`，审计为 `work/previous-raw-audit.json`。

## 新确认的原生限制

- 原生待批请求不会仅因随后出现 deny 自动失效；返回旧 allow 可直接放行。本片在工作台阻止已观察到的变化，未获得原子重新核验接口。
- 无效受管 `permissions.deny` 会让原规则从报告中消失，但 `errors` 仍为空。此次由规则变化使旧审批失效，不能据“无错误”声称配置完整有效。用户设置中的无效规则则报告解析错误。
- 中止原审批可直接产生中止结果，不必有第二次模型请求。因此工具请求/结果需从原生 wire 核对，不能只查看下一次供应商请求。
- 报告相同不证明磁盘、远端策略或执行器已同步；初次采样前、采样之间、核对之后的变化以及未进入 canUseTool 的工具仍保留缺口。任何相关报告变化都会保守地阻止旧批准，不尝试判断它是否与该工具无关。

公开 [SDK 用户输入说明](https://code.claude.com/docs/en/agent-sdk/user-input)、[权限顺序](https://code.claude.com/docs/en/agent-sdk/permissions)及固定安装版本的类型声明用于接口核对；本地归档保留查询结果。未找到公开的“原待批请求原子重判”方法；没有访问 SDK 私有成员，也没有把 applyFlagSettings 或只读清单当成这种接口。

## 失败与修正记录

原始 bridge 三个失败对照与 Application 红灯保留。随后又复现本地来源变化会连原拒绝一起拦截，补齐严格限定的拒绝路径。SDK 最初15项中13项失败；最终新增写入故障和后续明确输入两个边界，17项通过。

夹具曾误等无效受管规则的解析错误，原生证据表明应观察规则消失；中止审计曾仅从第二次供应商请求寻找工具，改为独立检查原生 wire。浏览器首次捕获了“未发送”后短暂 disconnected 的真实状态，正确禁用了拒绝；保留该帧，并在原生重新核对 ready 后采集可操作状态，完整浏览器流程通过。

首次独立审计把配置摘要当成未排序 JSON 哈希，七项失败；按既有递归排序契约独立重算后216项通过。另保留 TypeScript 可选值编译失败、custom fixture 空路径失败及一次 vet 包路径错误，均已修正并通过对应检查。多文件并发 Node runner 有两份文件被取消，未计为通过；使用项目已有逐文件入口后91项通过，组合 runner 的取消原因未定位。

全部原生实验在 `/var/tmp` 自有目录执行，使用 bwrap user/PID/network/mount 隔离、自有 `/etc/claude-code` 和本地模拟供应商。Windows 注册表入口为受控缺席夹具；未调用真实供应商、修改用户配置、操作用户后台或验证真实企业远端策略。

## 产物与复跑

本地归档：[manifest](W02-09-claude-pending-permissions/manifest.json)、[文件索引](W02-09-claude-pending-permissions/archive-index.json)、[归档核对](W02-09-claude-pending-permissions/archive-verification.json)。包含原始失败/成功材料、日记、HTTP/供应商请求、截图、测试/构建日志、被测源码与构建副本。Go 源码与二进制采用 gzip。原始归档由 `.gitignore` 排除，顶层报告及源码随 Git 同步。

- 开发 `.dev/bin/netcap`：`f656913cbf4f98447d55864f363275a5e885abb037a5c967258744b533602242`。
- 原生矩阵测试构建：`0eed9bfcb183d64de729590d9de6c706c17a463cc01cad97c01985f12a2970a5`；之后补充的 bridge 声明单元场景另以定向 race 执行，生产源码未变。
- 旧 `bin/netcap`：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，保持不变。

依赖保持原版本。原生矩阵执行生产 Application/Handler/Runtime；开发 CLI 完成构建，浏览器使用最终 Web 产物，不将其称为部署验收。

```bash
npm --prefix bridges/claude run build
npm --prefix web run build
node --experimental-test-module-mocks tests/claude_pending_permissions_test.mjs
go test -race ./internal/runtime ./internal/app ./internal/console
go test -c -race -tags=nativeclaude -o .dev/pending-native.test ./internal/runtime
python3 tests/claude_pending_permissions_native.py --binary "$PWD/.dev/pending-native.test" --case deny
python3 tests/claude_pending_permissions_audit.py --native <输出的原生目录> --output .dev/pending-audit.json
```

其他 `--case` 为 `unchanged / restore / invalid / user-invalid / local-drift / interrupt`；浏览器入口为 `tests/claude_pending_permissions_browser.js`，替换三个标记时使用对应原生目录的 Handler 帧与最终 Web 资产。

下一片继续首次采样前、采样后的策略变化及未触发审批工具的来源边界。完整来源/权限、remote/helper 新鲜度、四配置剩余组合、真实方法/Gate、独立前台与输入独占、Windows 实际控制等硬要求均未因此完成。
