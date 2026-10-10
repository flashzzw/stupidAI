# W02.08 / W02.09 · 私有设置文件生命周期与已安装插件设置预览

日期：2026-10-09（Asia/Shanghai）。W02.08、W02.09 保持 **in_progress**，全目标仍为 **9/40 done、active**。

## 用户可见结果

在“执行配置快照 → Claude 已安装插件登记”读取登记后，支持范围内的来源可点击“核对启用设置”或“核对排除设置”。界面显示目标键的原生有效值、格式诊断、原/临时文件、来源稳定性与回收状态。失败、断连和输入归属变化会清除旧结果；迟到响应不会恢复旧显示。

入口为 `POST /api/sessions/{id}/plugins/settings-preview`，请求包含完整 `source_key`、`mode=enabled|disabled` 和安装登记返回的 `source_version`。版本覆盖安装登记、已知市场登记、市场 manifest、插件 manifest 的实际字节、权限与解析路径；同版本排版变化也会拒绝旧请求。仅接受唯一且与当前项目匹配的本地声明。版本范围不包含插件全部组件文件；缓存路径仍不能当作实际加载路径。

此操作使用独立原生元数据进程，**始终 `complete=false`**。不保存执行候选，不改 Scope、原生 ID、快照、输入归属或执行操作，不开放原生已安装插件的正式启停。

## 实现和边界

- `claude_settings_file.go` 封装创建、保留、重开、回收与恢复。副本位于原 custom settings 的 lexical 父目录，未指定 custom 时位于 cwd，只合并目标 `enabledPlugins` 键；保留其他值、大整数和单 `/` 权限规则的目录语义。
- 原目录须由当前用户持有、owner 可写、group/other 不可写。使用 `O_TMPFILE` 创建 0600 inode，写入/fsync 后先记录身份，再用 `linkat` 发布名字。记录目录为 0700，收据为 0600；仅保存摘要、路径、inode/UID、Scope 摘要和所选键，不保存设置正文或 env 值。
- 使用者持共享 flock，经 Go `ExtraFiles`、Node stdio fd 3 传到原生进程。worker 实际核对 `/proc/<native-pid>/fd/3` 的 inode。回收须取得独占锁，并通过已核对的目录 FD 检查文件身份、全部模式位和字节摘要；变动或活跃文件保留诊断。
- 恢复只扫描已登记收据；单个坏文件不阻碍独立文件回收。创建失败后的 indexed publication、请求取消，以及删除后归档中断均有恢复路径。已完成收据移到 `released/`，不占用活动回收索引。请求取消后的回收使用独立有界 context。
- 复用普通创建的固定 CLI `--bare --no-session-persistence` 校验器，保留真实创建的原有拒绝与来源复读。预览额外返回所选键的 requested/actual/matches，不返回其他设置值。来源读取不完整或发现策略 helper 时拒绝进入校验。
- Application 前后核对会话 generation、Scope、配置登记和完整安装元数据版本。恢复旧损坏文件的诊断单列，不阻断其他 profile 的独立预览。

未提供不支持文件系统的异目录回退。稳定父目录符号链接和只读/共享父目录拒绝已覆盖；原文件自身符号链接的完整权限语义、所有父目录并发替换及不遵守锁的同 UID 外部写入竞态并未全部验证。这里的锁约束工作台管理的消费者，不宣称冻结外部文件。

## 实际验证

| 层次 | 结果 |
|---|---|
| 文件系统原语 | 当前 WSL 的匿名 inode、linkat、同 inode、0600、内容与 fsync 检查通过 |
| 文件模块与恢复 | 生命周期、双 holder、来源/文件/权限漂移、损坏收据、取消、回收隔离通过；四个持久边界真实 SIGKILL 通过 |
| Go → Node → 受控原生传输 | worker 被杀、Go 父进程被杀两类通过；存活消费者仍持锁，退出后才可回收。这一故障层使用协议夹具 |
| 固定原生 CLI / Application / Handler | **33 项通过**：启用/排除、真实原生 FD、同目录/无 custom/父目录别名、来源重读、格式错误、原绑定保持及实际文件回收 |
| 校验器传输 | **18 项通过**，含已有创建协议及新增键缺失、非布尔值、错误键、错误文件、原生关闭 FD 等边界 |
| Application / HTTP | 正常与来源中途漂移、四类原始元数据变化、输入归属变化、七类来源选择拒绝和新增五项 HTTP 边界通过 |
| 普通原生创建回归 | 五类 **25 项通过**；三类无效权限在真实 query 前拒绝，正常创建行为保留 |
| MCP / 插件调用回归 | 当前开发二进制分别 **39 / 33 项通过**，使用原生 CLI、隔离 profile、本地模拟供应商及测试 hooks |
| 浏览器 | **25 项通过**；真实 Handler 结果回放，另以受控数据验证不匹配、未回收、迟到响应与断连。1440 / 390 布局无横向溢出 |
| 独立核对 | **107 项通过**：原始字节/模式/解析路径、独立重算派生摘要与 Scope 摘要、SQLite 只读、原生日记、HTTP 路径、收据、调用/hook 日志与自有进程 |
| 工程检查 | `runtime/app/console` 全包 race、`nativeclaude` tagged vet、bridge/Web/开发构建通过 |

原查询创建时只产生一次无正文 `HEAD /api/hello`。预览阶段零新增 HTTP、无模型输入、无新 query 初始化或新 hook。原有三个 SessionStart hook 各执行一次。完整受管/远程策略与方法/Gate 没有因此通过。

## 原始失败和修正

1. 初始模块把 `enabledPlugins` 中的 `null` 解成 false，且遇一个坏记录便停止回收。改为显式拒绝 null，并继续独立记录；临时只读测试目录的 cleanup 同时修正。
2. 四类损坏收据仍可触发删除。真实红灯后补齐 Scope 摘要、选择集合、原来源路径和元数据合法性检查。
3. 删除成功后、收据归档前 SIGKILL，已 released 记录残留在活动索引。真实红灯后补齐恢复归档；四个中断点最终全部通过。
4. 首次 TS 构建的 stdio 空值类型检查、App 夹具相对 Python 路径，以及浏览器工具不提供 `structuredClone` 的失败均保留；按实际接口修正，没有改变产品断言。
5. 原生样本最初把创建阶段的 HEAD 健康探测计成模型请求。追加请求路径、阶段及正文长度后确认原因；最终仍要求预览零请求、全程无模型输入。
6. 审计最初要求预览期日记副本等于最终日记，随后误认为追加 close 属于同一连接。实际尾部是一条无 PID、从未 initialized 的元数据 Session close；对照 `Session.restore/close` 和完整原始日记后，分别核对预览前缀和追加记录，未把它计作原生 query 重开。

原始失败未覆盖。浏览器控制台的连接失败来自第一次工具脚本中断，409 来自刻意测试的来源过期响应；最终无页面脚本错误。

## 版本、复现与归档

- Claude Code `2.1.280`，SDK `0.3.280`，当前 WSL2；生产来源与测试文件见[源码差异](W02-08-claude-settings-preview/source.diff)。`golang.org/x/sys v0.37.0` 从 indirect 改为 direct，未升级依赖或改变 go.sum。
- 开发二进制 `.dev/bin/netcap`：`47294370102da2dd7f9298cf8b877231c610aa192b90c1d9441d52c814446d2b`。
- worker `settings_validation.js`：`4d5f382fe8103e854e04e7e565f3a61764b346fd466b6334348715e0084bb88f`。
- 旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`；7,610 个 W 系列旧证据及 31 个旧 goals 证据未变。无关 Claude bridge 源码/构建未变；自有测试进程与浏览器已退出。

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags nativeclaude -race ./internal/runtime -run '^TestClaude(NativePluginSettingsPreview|SettingsPreviewOrphanFileLock|SettingsFileCrashRecovery)$' -count=1 -v
node --test tests/claude_settings_validation_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags nativeclaude ./internal/app -run '^TestClaudeNativeCreateRejectsInvalidSettingsBeforeStartup$' -count=1 -v
python3 tests/claude_mcp_selection_smoke.py
python3 tests/claude_plugin_execution_smoke.py
```

上述原生验证在暂停前的可用 IPC/本地监听环境完成；恢复后工具权限切换为 workspace-write、受限网络，未将历史通过解释为新权限环境的验证。后续需要原生执行时重新采样前置。

先按项目 Makefile 构建 bridge/Web/开发二进制。浏览器入口为 `tests/claude_plugin_settings_preview_browser.js`，注入原生 `result.json`、当前 Web assets 和截图目录；回放范围不等于浏览器现场执行原生事务。

持久工作目录 `.dev/w0208-settings-file-lifecycle-work`。归档包含[审计](W02-08-claude-settings-preview/audit.json)、[浏览器结果](W02-08-claude-settings-preview/browser.json)、[命令记录](W02-08-claude-settings-preview/command-results.json)、[原始材料索引](W02-08-claude-settings-preview/archive-index.json)及[manifest](W02-08-claude-settings-preview/manifest.json)。按索引保留必要文件，未复制完整 profile、数据库、访问令牌或私钥；测试自定义 settings 和元数据中仅含已知夹具内容。

## 尚未完成与下一步

原生已安装插件的正式启停、持久候选与原 ID 应用尚未接通。下一片核对完整受管/remote/helper 与执行权限的启动准入依据，再把本模块接入已安装来源的持久候选/故障恢复；不能凭 bare schema 或目标键相符放行。

W02.07/08/09 的真实方法与 Gate、完整动态来源/权限/四配置，以及独立前台、共享后台/输入独占、Windows 第二环境与日常生命周期等硬要求保持。没有提交、推送或发布，本切片不构成完整 W 系列交付。
