# W02.08 / W02.09：安装绑定的本地采纳失败恢复

2026-10-10。已安装插件设置的执行在本地设置采纳失败后，可在原内容、路径和权限还原后，核对并明确按原 ID 恢复。还原文件不会恢复纳秒身份，因此恢复成功会产生独立配置版本；原快照、原历史和失败候选继续保留。

**W02.08/W02.09 仍为 in_progress；9/40 done、23 pending、8 in_progress、0 blocked，Goal active。** 本轮真实 CLI 被隔离环境权限阻止，新增原生路径尚未验收。

## 行为与依据

原生产 Session 复现只有 `original_settings` 未满足，其余五个恢复条件通过，query 启动数为 0、metadata 未改。逐项检查确认原内容、路径、权限和资源相符；只替换本地文件身份后校验通过。保留 [首次复现](W02-09-claude-installed-settings-recovery/repro-first.log)、[原因核对](W02-09-claude-installed-settings-recovery/repro-second.log) 和 [只读恢复计划通过](W02-09-claude-installed-settings-recovery/repro-read-green.log)。这些使用受控 SDK history，不是原生 CLI 结果。

- 恢复计划记录完整原/新启动来源，只允许失败采纳实际改变的本地路径重新记录身份。正文、解析路径、权限、未采纳的本地文件、非本地来源、注册表、策略和安装资源仍严格比较。
- 界面先显示将创建的版本、文件和来源版本。明确命令携带 `installed_source_version`；核对后文件再次变化时拒绝，需重新核对。旧 bridge 缺少新能力时也拒绝。
- Application 派发前持久化新恢复快照及完整计划，bridge 打开 query 前保存新绑定。成功确认后，Scope 与当前快照一起晋升，`restored_from_snapshot_id` 指向原快照；原快照原样进入历史，失败尝试和暂停保留。
- 未知、缺失或不匹配的回执保留原快照、失败候选和恢复计划；重启只读核对持久结果，不重放。恢复启动失败保留独立诊断。已证明未发送的计划可释放；新命令使用新版本，旧命令重试继续引用原操作。
- 新版本文件继续使用同目录计划、v2 保留收据与使用锁。当前、历史、失败尝试及在途恢复都算引用；无引用的 not_sent 恢复文件沿原回收入口处理。后续普通改配不继承“恢复版本”标记。

入口：[Application](../../internal/app/claude_installed_settings_recovery.go)、[Runtime](../../internal/runtime/claude_installed_settings_restoration.go)、[Session](../../bridges/claude/src/session.ts)、[界面](../../web/src/ExecutionRecoveryDetails.tsx)。归档中的 [source.diff](W02-09-claude-installed-settings-recovery/source.diff)、[源码复核](W02-09-claude-installed-settings-recovery/source-review.md) 和 [manifest](W02-09-claude-installed-settings-recovery/manifest.json)记录本片最终版本。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| Application / SQLite | 原安装应用→本地采纳失败→还原→显式恢复；来源版本确认、再次漂移拒绝、派发前持久化、丢回执重启核对、缺失/旧绑定回执、not_sent 回收对象和明确重试、原快照及失败尝试保留均通过。最终 [记录](W02-09-claude-installed-settings-recovery/application-result.json)含查询 DTO 及 SQLite 派发前/恢复后记录，Runtime 受控 |
| Runtime 合约 | 新来源与完整报告比较、恢复/失败投影、回显绑定和旧 bridge 能力检查通过；Claude 相关 race 通过（6.420s），最终新恢复合约另以 race 重跑 |
| SDK / Session | 五组 **47 tests** 通过，其中新增恢复 **9/9**：显式新版本、原 ID 冷重开、持久重试不启动、再次身份漂移拒绝、七类拒绝以及恢复启动失败。生产 Session、真实自有文件，query/进程 I/O 受控；[结果](W02-09-claude-installed-settings-recovery/sdk-regression-result.json) |
| 浏览器 | **35/35** 通过；真实构建资产回放 Application 查询 DTO，覆盖版本说明、三类无效回显、未满足条件、键盘触发、未知回执、刷新不重放、历史及失败候选。1440×900 / 390×844 无横向溢出或页面/控制台异常；[结果](W02-09-claude-installed-settings-recovery/browser-result.json) |
| app / console race | app 全包最后生产修复后通过（116.819s）；console 全包通过（1.857s） |
| vet / 构建 | runtime/app/console/cmd 四包 `nativecodex nativeclaude` vet、bridge TypeScript、Web typecheck/Vite、开发二进制及 nativeclaude 测试二进制编译通过 |
| 原始材料复核 | [138 项通过](W02-09-claude-installed-settings-recovery/audit.json)，从 SQLite 导出、原/新快照、SDK metadata/journal/文件及浏览器请求独立重算；不将受控数据算作 CLI、Handler 或 FD 原生证据 |

截图：[恢复前版本](W02-09-claude-installed-settings-recovery/browser/review-1440.png)、[窄屏操作](W02-09-claude-installed-settings-recovery/browser/review-390.png)、[待确认](W02-09-claude-installed-settings-recovery/browser/uncertain-1440.png)、[恢复关系](W02-09-claude-installed-settings-recovery/browser/recovered-390.png)、[历史与失败尝试](W02-09-claude-installed-settings-recovery/browser/history-1440.png)。HTTP 状态和事件通知由回放脚本提供；该浏览器链路未执行真实 Handler。

目标版本继续固定 **Claude Code 2.1.280 / SDK 0.3.280 / Node 24.13.0 / React 19.3.0**。本轮 SDK/Node/React 与依赖文件已核对；CLI 未启动，不能借用上一片的原生通过结论。最终 `.dev/bin/netcap` SHA-256 为 `4c1da5b2b90afe338f31af219dc4ddd4f28ccae9f4e39a343dc42a53ba608578`；旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。

## 失败与未执行

- [原生尝试](W02-09-claude-installed-settings-recovery/native-recovery-first.log)在 bwrap 报 `Failed to create NETLINK_ROUTE socket: Operation not permitted`；测试程序与 CLI 都未启动。`adoption-recovery`、`adoption-recovery-lost` 已实现并编译，但真实运行仍未验证；未去掉隔离重跑。
- [runtime 全包 race](W02-09-claude-installed-settings-recovery/race-final.log)在既有 OpenCode `httptest.NewServer` 的 TCP6 监听被 EPERM 阻止；该命令整体失败。后续 Claude 定向 race 通过，不称 runtime 全包通过。
- [全仓库 tagged vet](W02-09-claude-installed-settings-recovery/vet-final.log)扫描到旧证据中的不完整 Go 源码副本，出现未定义类型；旧证据未改。四个相关生产包的限定 vet 通过。新归档的 Go 源码使用 `.go.gz`，避免新增类似包。
- 首轮 Web/Go 同时构建曾造成 console embed 读到被清除的 JS；Web 完成后顺序重跑通过，后续保持 Web→Go 构建顺序。
- SDK 夹具最初没有保证快速同内容重写实际改变时间身份；改为明确更新时间并断言身份变化后 9/9 通过，失败日志保留。
- 浏览器脚本的 `URL` 全局、窄屏详情抽屉和首次加载等待假设曾失败；修正脚本后通过，原始失败与截图保留。
- 源码复核发现普通候选重新复制快照，覆盖了先前清空的恢复标记。Application 后续采纳测试 [先失败](W02-09-claude-installed-settings-recovery/ordinary-change-marker-red.log)、[后通过](W02-09-claude-installed-settings-recovery/ordinary-change-marker-green.log)，app 全包 race 已复跑通过。
- 首次材料审计与尚未完成的归档写入重叠，文件校验失败；同时把 Go 省略的 false 字段当作必有字段而中断。失败结果保留，后续严格等归档结束再审计，并按 JSON 契约读取可省略字段。

## 复跑与收尾

在仓库根目录，沿项目相关包运行；使用独立、可写的临时目录：

```bash
netcap_recovery_tmp=$(mktemp -d /tmp/netcap-installed-recovery.XXXXXX)
npm --prefix bridges/claude run build
npm --prefix web run build
TMPDIR="$netcap_recovery_tmp" GOCACHE="$netcap_recovery_tmp/cache" go test -race ./internal/app ./internal/console
TMPDIR="$netcap_recovery_tmp" GOCACHE="$netcap_recovery_tmp/cache" go test -race ./internal/runtime -run '^TestClaude|^TestInstalledRecovery'
TMPDIR="$netcap_recovery_tmp" GOCACHE="$netcap_recovery_tmp/cache" go vet -tags 'nativecodex nativeclaude' ./internal/runtime ./internal/app ./internal/console ./cmd/netcap
TMPDIR="$netcap_recovery_tmp" GOCACHE="$netcap_recovery_tmp/cache" go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
node --experimental-test-module-mocks tests/claude_installed_settings_recovery_test.mjs
```

Application 证据采集设置 `NETCAP_INSTALLED_RECOVERY_REPORT` 后执行 `go test ./internal/app -run '^TestInstalledSettingsRecoveryPublishes' -count=1 -v`。浏览器入口为 [测试模板](../../tests/claude_installed_settings_recovery_browser.js)，开发目录 `.dev/w0209-installed-settings-recovery-work/render_browser.py` 填充记录和当前资产，Playwright 按生成文件执行；不依赖监听端口。

原生隔离恢复后，编译 `go test -c -tags nativeclaude -o .dev/w0209-installed-settings-recovery-work/runtime-final.test ./internal/runtime`，在独立 `TMPDIR` 中运行 `python3 tests/claude_installed_settings_apply_probe.py --binary "$PWD/.dev/w0209-installed-settings-recovery-work/runtime-final.test" --case adoption-recovery --settings-source custom --policy ordinary`；丢回执变体为 `--case adoption-recovery-lost`。保留固定版本和 bwrap 隔离条件。

归档/审计脚本与材料在同名目录；[附件索引](W02-09-claude-installed-settings-recovery/archive-index.json)保留原路径和哈希，[收尾 15 项](W02-09-claude-installed-settings-recovery/closeout.json)核对 **13,907 个旧 W 证据、31 个旧 goals 证据**、旧 bin、依赖和自有进程/浏览器，文档检查 12 项通过。旧 `.dev/w0209-plugin-startup-attestation-work` 原始材料未改写。归档不复制账户文件、数据库或锁；测试数据可用于复核受控行为，不能替代原生验收。未提交或推送。

下一步先补冷原查询重开失败、尚无 `original_plugins` 时的恢复依据，权限恢复后完成新增原生路径。完整受管/remote/helper 真实性与新鲜度、外部竞态、动态权限、四配置联合、方法/Gate、独立前台/输入独占、Windows 及所有既有硬要求继续保留。
