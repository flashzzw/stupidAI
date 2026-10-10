# W02.08 · 当前查询的 hook 来源清单

2026-10-02；W02.08 in_progress，整体 9/40 done，Goal active。上一轮的插件协议实验改变了接入判断，属于有效进展。本片补齐生产核对隐含 hook 所需的只读入口；插件资产的启停与版本事务仍待后续接通。

## 生产行为

新增 `POST /api/sessions/{id}/hooks/catalog`（仅 `{}`），贯通执行详情页、Handler、Application、Runtime、Session 和当前 CLI 查询。读取用户、项目、会话及插件 hook 的事件、匹配范围、完整来源键、原生禁用状态、后台/单次/条件执行属性和定义摘要；显示受管策略、原生模式与配置解析错误导致的覆盖限制。

固定 SDK 0.3.280 没有公开 `get_hooks_listing` 方法，但提供 `spawnClaudeCodeProcess` / `SpawnedProcess` 接口。本片通过该公开进程接口，在同一 stdio 通道添加仅用于 hook 菜单的请求；按每个查询的专有 request ID 截取回复，其余字节继续交给 SDK。分片按字节拼接，保留 Unicode；超时、关闭和原生错误返回不可用，迟到/重复的自有回复不会进入 SDK 的请求表。没有访问 SDK 私有对象，也没有新通用控制或第二个 CLI 进程。

读取不调用 reload、不执行 hook、不发模型输入、不加载凭据，不创建/恢复 query 或 Runtime transport。读取前后核对配置、原生 ID、connection、工作台 generation 与保存绑定；读期间改配、关闭或人工接管拒绝过期结果。清单不写入执行快照、不授予输入权。

桥接边界只输出选定的元数据，命令、提示词、URL、条件正文、可编辑配置和原始错误文本不传给工作台。定义摘要根据原生报告的定义计算，只用于识别变化，不代替资产 SourceStamp、内容冻结或执行证据。插件 hook 的完整来源键不会按显示名猜测资产或文件路径。

页面沿用现有详情布局，手动读取；失败、会话/快照切换及断连清除旧结果。`complete` 始终 false，“已登记”不等于执行成功或可独立选配。共享的目录/查询绑定核对辅助函数仅改为能力清单通用名称，既有 Skill 入口行为保留。

## 实际验证

| 层次 | 结果与范围 |
| --- | --- |
| 原生 bridge/Session | `python3 tests/claude_hook_catalog_native.py`：13 项通过。真实本地市场安装，用户/项目/插件三源、全局禁用、原 ID/日志保持、敏感正文排除、关闭后拒绝且不重开。最终目录见 `native-final.log`。初始请求返回 unsupported，失败记录保留。 |
| Application/SQLite/Runtime | `NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags=nativeclaude ./internal/app -run '^TestClaudeNativeApplicationHookCatalog$' -count=1 -v`：18 项通过（4 项已有 Skill 选择、14 项 hook 清单）。A/B 的原生身份、输入归属和执行快照不变；各有三源 hook，B 的原生禁用状态与策略可核对。目录 `/tmp/netcap-w0207-claude-app-2629791831`。 |
| SDK/进程边界契约 | Node 模块模拟下 7 项通过：中文/emoji 分片、180 KB SDK 控制回复保留、错误/损坏元数据拒绝、内容不外传、safe/bare/策略未知、关闭中取消，以及超时后的迟到回复隔离。SDK 与进程为内存夹具，不算原生通信验收。 |
| Go 边界 | Runtime 10 个元数据场景及无 transport 不启动检查通过；Application 6 场景验证只读与错误 ID/cwd/source、虚假完整性和接管期间的拒绝。Handler 新增 5 项鉴权/Origin/空对象/缺失会话约束，现有套件通过。 |
| 浏览器 | 23 项通过；真实 Application 导出覆盖用户/项目/插件/全局禁用，受管未知、未知来源、解析错误与条件属性使用明确的契约夹具。覆盖失败、切换、断连、1440/390 布局、无执行命令和空对象读取。HTTP/EventSource 为受控重放，没有 listener 验收。截图已逐张查看。 |
| 回归与构建 | app/console 全包 race、Runtime Claude 筛选 race、相关 nativeclaude 标签 vet、bridge tsc、`make dev-build`、Python AST/Node syntax 通过。原生 Skill 改配 32 项、资源包 Node 18 项、认证恢复 Node 10 项通过；它们不代替实际 MCP/模型请求验收。 |

Go 统一使用 `GOCACHE=/tmp/netcap-w0206-auth-go-cache`。浏览器入口为 `tests/claude_hook_catalog_browser.js`，以原生 `hook-browser-responses.json` 与构建静态文件替换占位参数，通过 Playwright MCP 执行。结果保存在本前缀附件。

初始浏览器脚本依赖执行环境未提供的全局 `URL`，随后遗留路由使导航未完成；已改为已有脚本的路径提取方式，清理自有测试路由并设置等待上限。首次 Node 子进程夹具没有返回初始化，独立采样显示该夹具子进程退出 0、无输出；没有据此断言是产品故障，也没有修改系统/IPC 限制。分片与错误注入改用内存进程夹具，原生覆盖由真实 CLI 测试分别证明。临时调试输出未留在源码中。

## 证据与下一步

附件包含原生结果、Application 返回、浏览器检查/截图、红绿日志、回归结果、差异与归档时点 manifest。旧 `bin/netcap` 及此前 979 个证据文件未变；开发构建已更新。所有自有执行句柄结束，浏览器已关闭。未操作共享后台、Windows 配置、真实供应商或发送外部消息。

仍未验收真实受管 hook、实际触发及失败传播、hook 与权限的关系、重复 MCP 和插件带历史恢复。此次清单没有接通插件资产选择，也不将 W02.07 的纯 Skill 加载包提升为 W02.08 插件支持。下一片在此来源/状态核对基础上接插件 CapabilityReference、内容戳、原 ID 执行版本事务与组件选择；保留同名来源补位和不可拆分组件拒绝要求。

W02.07 的 Claude 方法/Codex 选择完整探针仍没有新结果，已有请求继续有效；不重新探测已知的环境限制。W02.08 与完整 W 系列均未完成。
