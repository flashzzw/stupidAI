# W02.01 · 两类 CLI 执行配置登记与隔离

日期：2026-09-30。完成当前固定 CLI / WSL 本地路径的生产登记、诊断、选用、四配置并发和恢复隔离。W02.09 的完整继承/权限合并、W01 的任意既有原生入口与 Windows 宿主验收仍保留，未据此扩大能力声明。

## 实现

沿用 `runtime.Instance` + `runtime.Route`，通过 Application 命令事务登记；没有新增平行运行模型。定义保存在 schema 4 的 `execution_profiles` 表，重启与启动配置合并。相同 command_id 和内容可重试；实例 / 路由 ID 不允许覆盖；启动文件与持久定义冲突时明确拒绝，避免重绑历史。显示名称可相同。

生产 UI 提供“执行配置”列表、登记预览、重新诊断和“使用此配置”。预览不持久化；登记与诊断不创建会话、不发送模型请求。CLI、默认配置、自定义 settings、bridge / Node、控制 socket、路由和凭据引用分别呈现。socket 存在只是本地文件检查，原生身份/凭据仍在连接时核对。

Claude `profile` 绑定原生 `CLAUDE_CONFIG_DIR`，`settings_file` 独立传给 SDK 的原生文件路径选项。保留 user / project / local 继承；初始化后、开放用户输入前，用 `applyFlagSettings` 叠加原有 read_only ask 要求，并保留自定义 permissions 其他字段。默认与自定义来源分别入不可变快照；任一来源漂移阻止旧会话新输入。隔离 probe 不加载工作 settings。

[原生冲突实验](W02-01-custom-conflict-before.json)证明 settings.env 可以覆盖 SDK 显式传入的 endpoint/key：A 没收到请求，B 收到 1 次且匹配 B 的凭据。已增加诊断及执行前拒绝：登记的默认 / 自定义 settings 不得覆盖路由、凭据或状态环境，也不得设置 `apiKeyHelper`；错误不输出值。普通 env / hooks 保持原生生效。该保护不等同于穷举所有继承来源。

目录别名通过真实路径显示共享状态。真实测试发现 Codex initialize 返回规范路径，旧代码直接比较字符串，导致合法符号链接被拒；已改为在字符串不同时解析双方真实路径后比较，解析失败或实际目录不同仍拒绝。

## 验收映射

| 条件 | 实际证据 | 结果 |
|---|---|---|
| 生产登记、诊断、选用 | [13 项 HTTP/native](W02-01-four-profiles.json)、[18 项浏览器](W02-01-browser.json) | Codex A/B、Claude A/B 全部通过生产命令登记；创建使用指定原生配置 |
| 四份配置真正同时运行 | 同上；本地供应商在四个模型请求处设置同一个 barrier，全部到达才放行，保存各到达/释放时间 | 通过；不是仅同时启动进程 |
| 停止一份不影响其他 | 四份第二轮均已流式输出后停止 Codex A；A 为 interrupted，其余三份 succeeded | 通过 |
| 重启保持原 ID / 快照 / 路由 | 重启后逐份显式恢复自动化，等待新核对终态，再在原 ID 完成第三轮；[持久快照/操作](W02-01-production-snapshot.json) | 通过；四份快照、凭据、模型及历史无串用 |
| 同名、同登记 ID | 四份相同显示名；同登记 ID 改绑被拒；Go 并发幂等 / 持久失败 / 启动定义冲突测试 | 通过 |
| 相同原生 ID、不同状态目录 | [Claude 5 项](W02-01-collision.json)、[Codex 5 项](W02-01-codex-collision.json) | 两类生产 Runtime Adapter 各用同一个实际原生 ID，在 A/B 目录独立执行三轮并重建 Adapter；无凭据或历史串用 |
| 目录别名恢复 | HTTP/native 13 项中的别名创建、重启恢复和链接改指测试 | 两类 CLI 经链接创建后保持原 ID；链接改指 B 后阻止输入，不按新目录恢复 |
| Claude 自定义 settings 与状态分开 | [原生结果](W02-01-custom-settings.json)、[完整执行日志](W02-01-custom-settings.txt)、四配置 SessionStart hook | 任意命名文件实际加载 env/hook；默认 user/project 规则仍在；custom allow Bash 仍需 read_only 审批；源文件不改写 |
| 不可用配置说明原因 | 文件缺失登记/创建拒绝、凭据冲突测试、CLI/bridge/Node/socket 诊断 | 通过；可先保存不可用定义，修复后重新诊断 |
| 历史与兼容 | schema 3→4 保留旧状态；默认无 settings_file 的指纹保持旧算法；快照漂移及旧功能回归 | 通过所列检查 |

同原生 ID 样本的构造范围：Claude 使用生产 Adapter 创建相同 ID；Codex 仅在自有夹具停止后复制合成 transcript / state DB，保留 B 的独立配置，再通过生产 Adapter 原 ID 恢复。它验证 ID 碰撞隔离，不证明任意外部前台接管或用户数据迁移；工作台 HTTP 四配置流程单独实测。

## 验证与复现

固定 CLI：Codex proxy `0.157.1` / app-server `0.158.0`，Claude `2.1.280` / Agent SDK `0.3.280`；React `19.3.0`。使用隔离 `/tmp/netcap-*` home/profile/cwd/SQLite、真实 CLI 与本地模拟供应商。未调用真实供应商、停止用户共享后台、写入用户配置或迁移用户数据；只更新 `.dev/bin/netcap` 开发产物。

| 实际执行 | 结果 |
|---|---|
| `go test -race ./...` | [最终全包通过](W02-01-go-test.txt)；包含登记幂等、并发、存储失败、重启冲突、来源独立性与保留迁移 |
| `go vet ./...` | [通过](W02-01-go-vet.txt) |
| `make claude-build` / `make dev-build` | [bridge](W02-01-bridge-build.txt) / [TypeScript、Vite、Go](W02-01-build.txt) 通过 |
| `python3 tests/claude_custom_settings_smoke.py` | custom 原生语义、继承、单次审批及冲突拒绝通过 |
| `python3 tests/claude_settings_smoke.py` | [旧 A/B settings 与隔离 probe 回归](W02-01-legacy-settings.json)通过 |
| `python3 tests/execution_profiles_smoke.py` | 13/13；全部生产 HTTP 与真实 CLI |
| `python3 tests/execution_profiles_collision.py` / `execution_profiles_codex_collision.py` | 各 5/5；生产 Adapter 的真实同 ID 恢复 |
| `python3 tests/native_mcp_selection_smoke.py` | [28/28 原生 MCP 回归](W02-01-mcp-regression.json) |
| `python3 tests/execution_apply_smoke.py` | [18/18 W02.04 双向应用回归](W02-01-apply-regression.json)；本轮未重跑丢回执/未知版本故障组合，历史证据保留 |
| Playwright CLI，独立 `w0201-registration` 会话 | 18/18；完整 UI 登记四份配置、原生选用、来源展示、刷新、Escape、ID 校验、1440/768 布局；无 pageerror / console error |

新增 `make execution-profiles` 组合上述 custom / 四配置 / 两种 ID 碰撞入口；其中 collision runner 构建为 `.dev/w0201/collision-fixture`。浏览器启动 `tests/execution_profiles_smoke.py --empty --hold --info ...`，认证使用该夹具 token_file，仅存于 mode 0600 的临时浏览器状态；把 info 内的非敏感 profiles 注入 `tests/execution_profiles_browser.js` 的 PROFILES 位置后通过固定 Playwright CLI 执行。结束仅停止本轮夹具和浏览器会话。

界面证据：[1440 列表](W02-01-profiles-1440.png)、[768 列表](W02-01-profiles-768.png)、[768 登记预览](W02-01-registration-768.png)。样式沿用既有纸色背景、绿色操作、同一字体/边框/圆角；只扩展登记流程。源码、构建及证据归档时点的哈希见 [manifest](W02-01-manifest.json)。

本轮修复的浏览器问题：HTML pattern 使用 Unicode v 模式，字符类内未转义 `-` 导致 ID 前端校验失效；已改为兼容表达式，并验证非法字符拒绝、无控制台错误。测试本身另修正 label/select 定位和 SSE 到达时机；重启检查通过新的恢复 Operation 取得证据，没有把缓存 idle 当作新观察。

SDK 事实来源：[TypeScript settings / applyFlagSettings](https://code.claude.com/docs/en/agent-sdk/typescript)、本地固定版本 `sdk.d.ts`。`applyFlagSettings` 对顶层浅合并，permissions 整体替换，因此显式保留原 permissions 字段；env 优先级结论来自上述本地真实 CLI 实验。

## 剩余范围

W02.01 结束；下一项 W02.05。完整来源/受管权限/插件隐含配置与外部热更新仍在 W02.09，coverage 仍为 `recorded_sources_only`。不会把已登记文件的冲突保护扩称全部有效配置隔离。严格独立前台、Claude worker 工具结果连续性、用户试用、Windows 宿主链路及真实协作均未在本片通过，硬要求不变。
