# W02.08 / W02.09：已安装插件设置的原 ID 应用

2026-10-10。已安装的 `name@marketplace` 插件设置现在可从保存候选继续准备、明确应用并核对实际加载。新增安装选择保留已有选择；普通 MCP 改配、本地设置采纳、冷重开及列明条件下的显式恢复继续使用原 ID。保存不发布执行文件，不改变当前执行快照。

**W02.08/W02.09 仍为 in_progress；9/40 done、23 pending、8 in_progress、0 blocked，Goal active。** 本片只关闭列明的应用路径，完整来源/权限和四配置联合等硬要求不变。

## 实现与契约

- 格式 2 候选保存安装登记、声明资源树、启动来源，以及与快照 ID 对应的执行文件计划。`Scope`、操作、原生 metadata 和回执携带相同安装绑定；安装来源用完整 `NativeSource` 区分，`AssetID` 保持为空。旧候选缺少绑定或 bridge 缺少协议能力时拒绝准备，不补造历史依据。
- 准备只读核对原历史、空闲状态和来源。明确应用先持久化 Application 操作，bridge 再保存 applying，之后才重开冷原查询或关闭存活原查询。候选按原 ID 使用原 settings 同目录的派生文件，核对包路径、来源键、版本和 Skill/命令、Agent、hook、MCP；确认原历史、继承组件和空闲状态后原子保存原生结果，Application 才晋升快照。
- 预览文件仍用版本 1 收据自动回收；版本 2 文件随执行版本保留。SDK 子进程经 Go 私有 exec 入口继承 FD 3 与共享使用锁，核对真实 inode、路径、内容和父目录后 exec 成为 CLI。父进程退出或没有活查询都不能自动删除版本。Application 只清理已证明 not_sent、已撤销且无任何版本引用的文件，Runtime 再核对归属与独占锁。
- 普通 MCP 改配保留安装选择并规划新文件。本地采纳重新读取并记录新的启动来源，旧快照不变；非本地文件、注册表、策略、安装登记与资源仍比较。只读清单观察保留可用，输入与执行仍走严格来源检查。
- 应用失败保留 unknown 和原快照。显式恢复核对原操作、退出、历史和来源，安装覆盖回到继承时与保存的原组件清单比较；原安装版本恢复使用原文件。成功保留失败尝试和暂停，应用或恢复丢回执只读核对原持久结果，不重放。普通终端继续命令暂不向安装绑定执行提供，避免绕过覆盖和文件锁。
- 界面分别显示各安装来源、当前文件、安装选择的历史预览与本候选启动来源，并开放准备/应用/恢复。浏览器发现原有同级 React key 冲突，多次改配会重复生成 Skill 面板；三个组件改用独立 key 前缀，真实红灯后回归通过。

入口：[Application](../../internal/app/claude_installed_settings.go)、[Runtime](../../internal/runtime/claude_installed_settings.go)、[文件入口](../../internal/runtime/claude_settings_exec.go)、[bridge](../../bridges/claude/src/installed_settings.ts)。完整差异、源码审查与哈希见 [source.diff](W02-09-claude-installed-settings-apply/source.diff)、[source-review](W02-09-claude-installed-settings-apply/source-review.md) 和 [manifest](W02-09-claude-installed-settings-apply/manifest.json)。

## 原生依据与实际结果

固定 **Claude Code 2.1.280 / Agent SDK 0.3.280 / Node 24.13.0 / React 19.3.0**。三组真实 initialize 对照表明：已安装或已排除场景对声明目录、缓存、额外目录、缺失目录和组合的 `plugins_applied` 均为 false；inline 场景仅与其启动参数相符的额外目录为 true。没有模型输入、reload 或额外启动 hook。该 ACK 不能证明已安装来源；会加载插件的 `reloadPlugins` 因而置于明确执行操作内。原始请求/回复见 [initialize 对照](W02-09-claude-installed-settings-apply/raw/initialize-membership)，官方与固定 SDK 资料见 [摘录](W02-09-claude-installed-settings-apply/official-plugin-initialize.md)。

真实 Application / Handler / SDK / CLI 采用自有 A/B profile、假凭据、本地脚本供应商和 bwrap 独立 user/PID/network/mount namespace。`/etc/claude-code` 映射到自有策略，注册表为受控缺席；不算真实 Windows 或企业策略验收。安装声明目录与缓存目录分开，缓存 startup canary 没有运行，真实 hook 和包报告指向声明目录。实际 MCP 调用进入一次审批，结果回到正确的 A 供应商；B 的凭据、目录和原 ID 独立。

最终选定的十组结果如下；计数来自 cleanup 后写出的各 `result.json`，日志中更早打印的计数可能少一项：

| 原始目录 | 检查通过 | 覆盖 |
|---|---:|---|
| [managed](W02-09-claude-installed-settings-apply/raw/managed/result.json) | 55 | custom、受管 only、安装启停、其他安装选择、普通 MCP、原 ID/历史、冷输入 |
| [user](W02-09-claude-installed-settings-apply/raw/user/result.json) | 55 | 未指定 custom，保留 user 设置并从 cwd 使用派生文件 |
| [lost](W02-09-claude-installed-settings-apply/raw/lost/result.json) | 61 | 应用层丢回执、重启只读核对、同操作不重放 |
| [adoption](W02-09-claude-installed-settings-apply/raw/adoption/result.json) | 20 | 安装绑定后采纳本地设置，新来源版本、旧快照保留及冷输入 |
| [recovery-installed](W02-09-claude-installed-settings-apply/raw/recovery-installed/result.json) | 49 | 候选失败、原安装版本恢复、失败尝试归档与冷输入 |
| [recovery-lost](W02-09-claude-installed-settings-apply/raw/recovery-lost/result.json) | 23 | 首次覆盖失败后的继承恢复、恢复丢回执持久核对 |
| [cold](W02-09-claude-installed-settings-apply/raw/cold/result.json) | 58 | 冷状态直接应用；原查询 hook 观察到新的 applying 操作 |
| [source-drift](W02-09-claude-installed-settings-apply/raw/source-drift/result.json) | 7 | 保存后启动来源漂移拒绝 |
| [abandoned](W02-09-claude-installed-settings-apply/raw/abandoned/result.json) | 11 | 文件发布后派发被拒，撤销后按持久 not_sent 记录回收；使用 race 测试二进制 |
| [legacy](W02-09-claude-installed-settings-apply/raw/legacy/result.json) | 7 | 旧 bridge 缺能力时拒绝准备 |
| 合计 | **346** | 各组原始 wire、metadata/journal、文件、供应商请求与 SQLite 只读投影分别保留 |

[consumer 原16项](W02-09-claude-installed-settings-apply/raw/consumer)覆盖正常关闭与杀死 Node 两组，测试直接断言真实 `/proc/PID/fd/3` inode 和锁：关闭 Go 描述符后文件仍忙，原生消费者退出后才可明确释放。此轮原结果未保存独立 FDInfo 原文。后来增强采集 executable、fd_link、fd_info、file_identity 和 busy_error 并编译，但权限变为 workspace-write / approval never 后，[补测](W02-09-claude-installed-settings-apply/consumer-native-final.log)报 `bwrap: loopback: Failed to create NETLINK_ROUTE socket: Operation not permitted`。CLI 未启动，增强的原始 FD 采集**未执行**；没有去掉隔离重跑，也不把原16项扩称为原始FD详情验证。

## 验证与材料

| 层次 | 实际结果 |
|---|---|
| Go | runtime/app/console 三包完整 race 通过，分别 31.754s / 106.133s / 1.751s；`nativeclaude nativecodex` 四包 vet 通过，删除多余测试入口后再验 app |
| SDK 合约 | 九文件、Node runner 60 tests 通过；最后删除无调用的 `packagesKnown=false` 分支后，相关 36 tests 再通过 |
| 构建 | bridge TypeScript、Web typecheck/Vite、开发二进制及对应 native 测试二进制通过 |
| 浏览器 | [41项通过](W02-09-claude-installed-settings-apply/browser/result-final.json)，81条请求记录、无页面异常；真实 Handler 响应回放，覆盖保存/准备/应用、双来源、移动布局、旧格式、unknown、恢复、本地采纳和重复面板回归 |
| 独立材料审计 | [358项通过](W02-09-claude-installed-settings-apply/audit.json)，从原始请求回复、设置文件、包路径、实际工具调用、SQLite投影、恢复记录和哈希重算 |
| 未执行/未覆盖 | 额外 FD 原始详情补测、真实外部供应商、真实 Windows/企业策略、全部动态来源/权限、独立前台输入权和完整四配置联合 |

浏览器不是实时连接后台；仅缺失单项回执和旧格式显示使用受控变体。截图：[应用](W02-09-claude-installed-settings-apply/browser/applied-1440.png)、[移动布局](W02-09-claude-installed-settings-apply/browser/applied-390.png)、[待核对](W02-09-claude-installed-settings-apply/browser/unknown-1440.png)、[恢复](W02-09-claude-installed-settings-apply/browser/recovered-1440.png)、[本地采纳](W02-09-claude-installed-settings-apply/browser/adopted-1440.png)、[重复面板红灯](W02-09-claude-installed-settings-apply/browser/duplicate-catalog-red-1440.png)。React 仅采用稳定唯一 sibling key 规则，[官方摘录](W02-09-claude-installed-settings-apply/official-react-keys.md)未引入 canary API。

开发 `.dev/bin/netcap` SHA-256 为 `25c3fa5f595c9bd3323f369fe1758cdfd8e9b446b992c13c5cd9ab03b910d3e3`；旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。manifest 记录归档时的源码/构建哈希，不能倒推为每个历史运行阶段的版本。部分最终批次在删除未用分支之前运行，之后重新构建并回归 SDK、cold、来源拒绝、旧 bridge 与回收；阶段性二进制有覆盖，原始观察均保留。最后 Go 生产修改后 race 已通过，后续只增强原生测试采集。

## 复跑入口

在仓库根目录，普通 Go 检查使用可写的自有 `/tmp` 目录；不要继续使用权限切换前的 `/var/tmp`：

```bash
netcap_checks_tmp=$(mktemp -d /tmp/netcap-installed-checks.XXXXXX)
TMPDIR="$netcap_checks_tmp" GOCACHE="$netcap_checks_tmp/cache" go test -race -count=1 ./internal/runtime ./internal/app ./internal/console
TMPDIR="$netcap_checks_tmp" GOCACHE="$netcap_checks_tmp/cache" go vet -tags 'nativeclaude nativecodex' ./internal/runtime ./internal/app ./internal/console ./cmd/netcap
npm --prefix bridges/claude run build
npm --prefix web run build
TMPDIR="$netcap_checks_tmp" GOCACHE="$netcap_checks_tmp/cache" go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
```

原生隔离可用时，构建 `go test -c -tags nativeclaude -o .dev/w0209-plugin-startup-attestation-work/runtime.test ./internal/runtime`，再执行：

```bash
TMPDIR="$PWD/.dev/w0209-plugin-startup-attestation-work/owned" \
  python3 tests/claude_installed_settings_apply_probe.py \
  --binary "$PWD/.dev/w0209-plugin-startup-attestation-work/runtime.test" \
  --case happy --settings-source custom --policy managed-only
```

其余参数分别为 `--settings-source user`、`--case lost|adoption|recovery-installed|recovery-lost|cold-apply|source-drift|abandoned|legacy`；仅 happy 的 managed 组使用 `--policy managed-only`，其他组选 `ordinary`。`abandoned` 对应原记录的测试二进制另用 `-race` 构建。运行前仍须保留 bwrap 隔离和固定依赖，当前权限失败不能当通过。

SDK 回归使用 `node --experimental-test-module-mocks --test` 加 `tests/claude_{configuration_failure,configuration_recovery,mcp_configuration,plugin_selection,settings_admission,settings_guard,settings_sources,skill_change,tool_approvals}_test.mjs`。浏览器模板在 [测试入口](../../tests/claude_installed_settings_apply_browser.js)，`render_browser.py` 从 managed/recovery-lost/adoption 三份原始 Handler 记录填充，交由 Playwright 执行。本片自有页面已关闭，共享浏览器保留。

归档、只读投影和审计脚本在同名目录；开发副本为 `.dev/w0209-plugin-startup-attestation-work/{archive,audit}.py`。先归档再审计，审计输出写回开发目录；最终再次归档稳定结果，复核附件哈希。原数据库、账户文件、access-token、锁和外链未复制到正式材料；[索引](W02-09-claude-installed-settings-apply/archive-index.json)保留原路径/哈希，[附件哈希](W02-09-claude-installed-settings-apply/artifact-hashes.json)覆盖归档产物。

## 失败记录与剩余要求

- 本地采纳最初因校验副本丢失本地绑定而 HTTP409，修复后原清单观察又因旧启动版本被拒；两份真实红灯保留，改为记录新来源版本、保持只读观察可用后通过。
- 安装候选恢复入口最初返回409；后续恢复使用原组件清单，未把继承安装包误当成待删除的 inline 包。Go 恢复回执条件一度误接受缺失 `SettingsPrevious` 的旧记录，先失败后收紧，相关合约与完整 race 通过。
- 冷原查询 hook 最初读到上一次 verified 操作；改为先持久化新的 applying。夹具等待缓存离线状态过早的问题另行修正。撤销已发布的 not_sent 文件最初未回收，现接入持久操作驱动的清理。
- 浏览器 URL、页面重复初始化、异步刷新和候选 ID 前缀的夹具失败保留；重复 Skill 面板另有真实产品红灯。最初应用/丢回执/失败夹具的时序与字段错误也归档，未据其失败推断产品行为。
- 审计首轮只因 Go 对 absent 原文件写空路径、bridge 省略空路径而失败；仅规范化这两个空字段。随后审计函数局部名称冲突、inline 首次初始化携带插件被重复计数的失败亦保留。最终从原始 wire 分别核对首次初始化与五个挑战，没有修改原始结果。
- [收尾](W02-09-claude-installed-settings-apply/closeout.json)核对 11,815 个旧 W 证据和31个旧 goals证据、旧bin与依赖不变，自有进程退出、浏览器收尾，以及62份0600执行版本文件由版本2收据保留。保留文件包含历史与失败尝试，不按“无活进程”清除。未提交或推送。

安装绑定后的**本地采纳失败恢复**尚未完成：还原内容不恢复纳秒文件身份，需要独立恢复版本/身份记录，不能重写旧快照或放松比较。**冷重开原查询失败且尚无 original_plugins** 时也仍缺恢复依据。当前仅回收无引用的 not_sent 候选，完整历史版本退役没有完成。

完整受管/remote/helper 真实性与新鲜度、动态权限、外部竞态、四配置原生联合、真实方法/Gate、独立前台/输入独占、Windows与其他既有硬要求继续保留。下一片沿恢复版本和早期失败的证据边界推进，隔离权限恢复后补对应原生验收，不把本片当作完整 W02 或首版完成。
