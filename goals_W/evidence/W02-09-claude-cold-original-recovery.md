# W02.08 / W02.09 · 冷原查询在候选启动前失败的明确恢复

2026-10-10。已贯通生产 Session、Runtime、Application/SQLite 和恢复界面：安装设置改配在原查询重开或原组件采集阶段失败、尚未取得原组件清单时，可以先只读核对，再明确按原 ID 重开原配置并核对能力。需要版本 1 的持久阶段记录证明候选尚未开始；旧记录、阶段冲突和无法确认退出仍不放行。

这是一种独立恢复方式。`original_plugins` 保持缺失，首次成功的组件清单写入 `recovery.plugins`，后续输入和重开与这份恢复结果比较。候选已经开始后的普通恢复仍比较原组件清单，不能靠删除清单转成早期恢复。原快照、历史、失败候选和暂停保留；如同时需要记录还原文件的新身份，继续沿上一片的独立恢复版本处理。

## 原始失败与修复依据

- [原实现红灯](W02-09-claude-cold-original-recovery/sdk-original-red.log)：继承配置、已有安装绑定 **2/2 失败**。原失败阶段 `open_original`，退出已确认，五项条件通过，只有 `original_settings` 因没有原组件清单而拒绝；未启动候选。
- [新增边界红灯](W02-09-claude-cold-original-recovery/sdk-original-boundaries-red.log)：16 项通过，恢复后的继承包变化未被拒绝。修复后以独立恢复清单约束后续重开；没有回填或伪造失败前清单。
- 继承设置首次测试把 SDK 的正常只读权限对象当成空值，[失败日志](W02-09-claude-cold-original-recovery/sdk-original-first.log)保留。Application 初始夹具不能提供原生文件消费收据来做第二次插件预览，[诊断](W02-09-claude-cold-original-recovery/app-installed-diagnosis.log)保留；已有安装绑定场景改走普通能力改配，仍使用生产 Application 的保存、准备、派发和恢复入口。
- 浏览器首次把恢复后常驻的新改配表单误当作待恢复候选；已按实际 DOM 改为核对旧候选和“恢复中”提示消失。[原始红灯与截图](W02-09-claude-cold-original-recovery/raw/browser-red/inherited/result.json)保留。

## 已验证的契约

1. 首次原查询重开前持久化 `progress={version:1,phase:original}`；候选配置赋值和启动之前先持久化 `phase=candidate`。早期恢复依据包含完整、固定字段的原失败记录，匹配操作、基线、时间、阶段和确认退出状态。
2. 只读核对仍检查原绑定、设置与安装来源、原历史、未决工作和读取稳定性。Go 另外核对实际 snapshot 的阶段、原配置、缺失清单与原失败，不能只相信桥接端给出的“通过”。
3. 界面明确说明候选尚未启动、没有失败前清单，按钮为“按原配置重开并核对能力”。请求必须带 `original_query_retry=true`，Application 在派发前保存完整恢复依据。Runtime 要求 bridge 声明 `original_query_recovery:1`，重新核对当前依据并匹配回执。
4. 缺失/错误回执保持 unknown，重启和重复命令不重放。恢复自身失败保留原失败及独立恢复失败记录；只有明确 not_sent 才释放本次恢复操作供新的明确请求使用。无需文件身份恢复时，原快照和生效历史均不升级。

## 验证结果

| 层次 | 本轮结果与范围 |
|---|---|
| SDK / Session | 六组 **68 tests** 通过，其中原查询恢复 20、文件身份恢复 10（含组合场景）。真实自有文件，生产 Session，SDK query / 子进程 I/O 受控。见 [逐组结果](W02-09-claude-cold-original-recovery/sdk-final-result.json)；另采集带场景名的 20 份原始记录，不重复计数 |
| Runtime | 阶段/原失败/清单/原绑定拒绝、历史与当前投影、旧 bridge、实际 SDK 报告读取通过；直接 Go→stdio peer 的 7 个请求/回执场景通过。见 [边界](W02-09-claude-cold-original-recovery/runtime-original-first.log)、[wire](W02-09-claude-cold-original-recovery/runtime-wire-recorded.log)、[跨语言](W02-09-claude-cold-original-recovery/runtime-sdk-final.log) |
| Application / SQLite | 继承、已有安装、缺失/错误回执、not_sent、缺失/错误持久确认共 7 场景通过；派发前的 store 导出与重启后结果可比较，原快照/历史/候选保留。见 [首次完整通过](W02-09-claude-cold-original-recovery/app-original-second.log)和 [race 导出](W02-09-claude-cold-original-recovery/raw/application-race/inherited.json) |
| 浏览器 | 继承/已有安装各 **43**、旧文件身份恢复回归 **35** 项通过，共 121 项；1440×900 和 390×844，含不匹配依据、明确命令、未知回执、刷新、恢复历史。使用真实 Application DTO 与构建资产回放，HTTP、事件和 Runtime 受控；不是 Handler 或 CLI 验收。见 [继承](W02-09-claude-cold-original-recovery/raw/browser-second/inherited/result.json)、[已安装](W02-09-claude-cold-original-recovery/raw/browser-second/installed/result.json)、[回归](W02-09-claude-cold-original-recovery/raw/browser-identity-regression/result.json) |
| race / vet / 构建 | app **130.525s**、console **1.745s** 全包 race；Claude 定向 runtime **7.392s**；四包 `nativecodex nativeclaude` vet、bridge、Web typecheck/Vite、开发构建及 nativeclaude 测试二进制编译通过。新增 wire race **1.515s**。见 [检查记录](W02-09-claude-cold-original-recovery/validation-results.json) |
| 原始材料复核 | [独立审计](W02-09-claude-cold-original-recovery/audit.json)从 metadata、journal、源文件身份、SQLite 导出、stdio 请求/回执和浏览器命令重算；归档索引记录原始路径、摘要与压缩方式，不把受控进程 PID 当真实 CLI 证据 |

固定要求继续为 **Claude Code 2.1.280 / SDK 0.3.280 / Node 24.13.0 / React 19.3.0**。SDK、Node、React 与依赖文件已核对；本轮原生 CLI 未启动，不能借用旧版本实测宣称通过。开发二进制 SHA-256：`3731615cacce04dd12d47cff1759e9763df21d6e7007dd2b7f412a75009f9efc`；旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。构建和源码摘要见 [manifest](W02-09-claude-cold-original-recovery/manifest.json)，仅对应本次归档时点。

## 原生限制与未完成要求

[原生尝试](W02-09-claude-cold-original-recovery/native-original.log)在 bwrap 报 `Failed to create NETLINK_ROUTE socket: Operation not permitted`，测试程序与 CLI 均未启动。没有移除隔离重跑。`original-recovery`、`original-recovery-installed`、`original-recovery-lost` 已准备；只有第一种尝试进入 bwrap，其他变体未执行。探针本身的 4 个注入边界通过自有 delegate 检查，不能算作 CLI 通过。注入只针对已有持久原阶段的 `--resume` 查询，不作用于 bare 预检、候选或恢复查询。

本轮没有重跑全仓库 race/vet。此前 runtime 全包的 OpenCode `httptest.NewServer` 监听 EPERM、全仓库 vet 扫描旧证据中的不完整 Go 源码失败仍保留在[上一片](W02-09-claude-installed-settings-recovery.md)，不修改历史材料来消除它们；新增 Go 证据源码采用 `.go.gz`。

**W 系列仍为 9/40 done、23 pending、8 in_progress，Goal active。** 冷原查询恢复的真实 CLI、上一片文件身份恢复原生路径、完整受管/remote/helper 来源真实性与新鲜度、动态权限、外部竞态、四配置联合、真实模型方法/Gate、独立输入权、Windows 和其余硬要求均未被本片替代。14,433 个既有 W 证据和 31 个旧 goals 证据保持不变；没有提交或推送。

## 重跑入口

```bash
npm --prefix bridges/claude run build
node --experimental-test-module-mocks tests/claude_original_query_recovery_test.mjs
node --experimental-test-module-mocks tests/claude_installed_settings_recovery_test.mjs
go test -race ./internal/runtime -run '^TestClaudeOriginalQueryRecovery'
go test -race ./internal/app -run '^TestClaudeOriginalQueryRecoveryApplicationDurability$'
npm --prefix web run build
```

Application 原始 DTO 采集使用 `NETCAP_ORIGINAL_RECOVERY_APP_REPORTS` 指向已建立的自有目录；SDK 对应 `NETCAP_ORIGINAL_RECOVERY_EVIDENCE`，wire 对应 `NETCAP_ORIGINAL_RECOVERY_WIRE_REPORTS`。浏览器模板为 [原查询恢复](../../tests/claude_original_query_recovery_browser.js)，用采集到的 DTO 和当前构建资产填充后由 Playwright 执行。

隔离权限恢复后，先重新构建 nativeclaude 测试程序，再在新的独立目录运行：

```bash
go test -c -tags nativeclaude -o /tmp/netcap-cold-original-runtime.test ./internal/runtime
python3 tests/claude_installed_settings_apply_probe.py --binary /tmp/netcap-cold-original-runtime.test --case original-recovery --settings-source custom --policy ordinary
```

其他新变体仅替换 `--case`，保留 bwrap 和版本限制。下一片继续前置独立的来源真实性/新鲜度缺口，隔离可用时补齐列出的原生恢复链路，不能将本片受控结果提升为整包完成。
