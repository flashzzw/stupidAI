# W02.07 · Claude 当前查询的 Skill 来源清单

2026-10-02；W02.07 in_progress，整体 9/40 done。本片推进来源与选配限制可见性，不代替完整磁盘发现、真实受管来源排除或方法 Gate。

## 实现及原生观察

新增 `POST /api/sessions/{id}/skills/catalog`（仅 `{}`），贯通界面、Handler、Application、Runtime 和原生 Session。读取当前已开启查询的 `supportedCommands()` 与 `getContextUsage({detail: 'summary'})`；不调用目录 reload、不施加配置、不启动或恢复查询、不发送模型输入。原桥接器不存在时 Runtime 不调用创建 client 的入口；已关闭 Session 也明确返回不可用。

结果区分命令清单与 Skill 描述，保留别名、来源、插件名、名称/来源冲突，以及工作台当前的选配限制。只有唯一匹配已保存选择的条目显示资产和“快照绑定入口”，不会按显示名猜测其他文件路径。读取前后核对原生配置、查询 connection 和工作台输入归属；清单不写入执行快照或授予输入权。描述文本仅在此手动读取路径中获取，既有执行核对仍不携带该文本。

本次固定 Claude Code 2.1.280 / SDK 0.3.280 实测：除用户、项目和所选插件外，还有 `source: built-in` 的 Skill 描述，以及 `builtin: true` 的纯命令条目。前者不是同名用户 Skill 冲突，已单独分类；两类都保留“工作台尚无单项选配入口”。用户/项目来源显示需先登记并核对入口，未知和受管来源不自动升级为可选配。界面把内置 Skill 与纯命令条目折叠显示；失败、切换会话或断连清除旧结果。

固定 SDK 的 `skillFrontmatter` 只给名称、source 和估算 token；本次原生输出另报告 pluginName。接口没有完整路径及全部隐藏项，因此 `complete` 始终 false。这里的“描述报告”不是正文加载/使用证据，也不能用全局白名单替代独立排除。

## 验证

隔离用户配置、项目与状态目录，真实 CLI、自有 stdio、无服务的本地供应商地址；零模型输入，无真实供应商、共享后台或 Windows 变更。

| 层次 | 结果与复现 |
| --- | --- |
| 原生 bridge/Session | `python3 tests/claude_skill_catalog_native.py`：12 项通过。无显式 Skill 选择时仍返回用户/项目来源；插件归属/别名；查询/日志不变；原桥接器关闭后再次读取不重开查询。最终目录 `/tmp/netcap-w0207-catalog-jd7dilgi`。 |
| Application/SQLite/Runtime | `NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags=nativeclaude ./internal/app -run '^TestClaudeNativeApplicationSkillCatalog$' -count=1 -v`：18 项通过。A/B 身份、保存选择、输入归属保持；继承来源和原生内置分类；只有 A 报告的插件关联到资产，B 禁用项不被凭空加入。最终目录记录于 `native-app-verified.log`。 |
| Runtime 边界 | 10 场景通过，覆盖来源分类、路径未知、名称/别名/内置覆盖冲突、损坏元数据、未来未知类别、无 transport 不启动，以及多个保存资产不能凭名称合并。 |
| Application 边界 | 6 场景通过：只读保持；错误 ID/cwd/source、虚假完整性和读取期间转人工均拒绝。 |
| Handler | 既有鉴权/Origin/请求格式测试通过，新增 5 项 catalog 约束：无凭据、外部 Origin、路径覆盖、null、缺失会话。 |
| 浏览器 | 22 项通过。真实 Application 导出覆盖用户/项目/插件/内置数据；受管、未知和冲突提示使用明确的契约夹具。覆盖失败/切换清除、原 ID、别名、未知路径、无执行命令、空对象读取和 1440/390 布局。HTTP 是受控重放，不是实际 listener 验收。 |
| 回归/构建 | app/console 全包 race、Runtime 相关筛选 race、带 nativeclaude 标签的相关 vet、bridge tsc、`make dev-build` 通过。原生 Skill 改配 32 项、原选择 Node 6 项与资源包 Node 18 项回归通过。新增 Node 2 项确认描述只用于手动清单、来源查询失败不返回可用的部分元数据。 |

Go 全部使用 `GOCACHE=/tmp/netcap-w0206-auth-go-cache`。最终源码/构建/日志/截图与原始记录哈希见 `W02-07-skill-catalog-manifest.json`；切片差异同前缀 `-diff.patch`。旧 `bin/netcap` 与此前 942 个证据文件保持不变。浏览器使用 `/tmp/netcap-w0207-claude-app-706411316` 的真实数据；最终 Application 复跑单独归档，不混写目录。

初始 native 调用因未实现方法返回 unsupported，随后转绿；首次描述断言发现 CLI 会附加 `(user)`，已按实际元数据修正断言。真实 `built-in` 来源曾被误判为冲突，回归先红后绿（`builtin-red.log` 与最终契约记录），没有把误判保留为限制。所有自有执行句柄已结束，浏览器已关闭。

## 剩余要求与下一步

真实受管来源及策略优先级没有在本片构造；相关 UI/状态夹具不能替代其原生证据。当前查询也不等于完整目录来源列表，无法据缺席证明没有隐藏或历史 Skill。

仍未收到 `python3 tests/claude_skill_method_probe.py` 的完整正文/相对资料/真实历史改配结果；Codex 选择探针仍只有 metadata_only。此前请求持续有效，本片没有重复请求授权或重做受限的监听/Windows 探测。下一片继续 Skill 实际方法与完整来源，或推进前置独立的 W02.08 CLI 插件接入；保留 W02.07 的方法 Gate、Codex 选择及来源缺口。
