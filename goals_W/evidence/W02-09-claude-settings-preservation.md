# W02.08 / W02.09：原文件与派生文件的原生设置保留核对

日期：2026-10-09。状态：**progress**。插件设置预览现在会比较原文件与派生文件的其他有效设置、来源层、已采用参数和权限规则，缺少依据或出现额外差异时不再显示核对成功。**W02.07–09 仍 in_progress，整体 9/40 done，Goal active。**

## 完成的行为

此前预览只校验目标插件布尔值和来源文件稳定性，没有原文件的原生比较基线。新增原生 Application 断言得到四个缺失依据红灯；14 个受控传输场景也证实旧 worker 没有执行这项比较。原始失败保留在 [Application 红灯](W02-09-claude-settings-preservation/raw/application-red/result.json)与[传输红灯](W02-09-claude-settings-preservation/transport-red.log)。

现有校验器在相同 cwd、model、环境和固定 CLI 下，依次启动两个独立的 `--bare --no-session-persistence` 进程，分别读取原文件和派生文件的 `get_settings`、`list_permission_rules`，并核对 bare 模式与空 MCP 清单。比较规则如下：

| 对象 | 核对范围 |
|---|---|
| 其他有效设置 | 只排除本次选择的 `enabledPlugins` 键；其他插件开关、环境、权限等字段仍参与比较 |
| 来源层设置 | 只允许 `flagSettings` 中的目标键变化；继承层不排除该键，其他层、字段和顺序保留 |
| 已采用参数 | 比较原生返回的整个 `applied` 对象 |
| 权限规则 | 比较原生规则、来源、工作目录、`managedOnly`、未生效标记及诊断等整个权限状态 |

对象键顺序不会制造差异；去除目标键后形成的空 `enabledPlugins` 和空 flag 层作一致的归一化。数组顺序保留。设置或权限读取缺失、格式不符、cwd 不符、权限错误与设置成功矛盾时不会形成成功结果。

原始设置、规则和命令只在校验进程中处理，生产响应只返回分类、摘要、目标键与原文件路径。Go 独立验证基线路径、键集合、两次摘要、进程退出和差异结论；旧 worker 缺少该依据也会被拒绝。原有私有文件使用锁、结束回收、来源前后采样及启动冲突守卫继续适用。

界面分开展示目标插件值和设置保留核对；非目标插件、有效设置、来源、已采用参数、权限或格式差异有具体类别。旧响应缺字段显示待核对。此结果来自独立校验进程，**不证明正在运行会话的完整权限，也不授予已安装插件正式启停或原 ID 改配权限**。

## 实际验证

固定 **Claude CLI 2.1.280 / SDK 0.3.280**。原生测试使用自有 `bwrap` user/PID/network/mount namespace、自有 profile/项目、假凭据及本地供应商，自有策略目录映射到 `/etc/claude-code`；Windows 注册表为受控不存在。没有修改宿主策略、用户配置或共享后台，不把这些样本称为真实 Windows/企业策略验收。

| 验证层次 | 结果 |
|---|---|
| 真实 Application/Handler | **87 项通过**。原 ID、快照、人工接手、暂停及原三个启动 hook 保留；启用、排除、无 custom、目录别名、来源刷新和受管权限样本均核对，格式错误仍失败 |
| 原生读取原文 | **17 个元数据进程、16 次权限读取、8 组比较**。一组是格式无效诊断，其余保留核对相符。测试专用 preload 原样记录请求/回复，不替换原生响应或进程 |
| 受管权限 | 原生实际报告 `managedOnly=true`，两个非受管规则标为 `notInEffect`；原/派生文件观察一致。此处验证元数据和来源，没有执行受管权限下的 Read/Edit 工具 |
| 传输与差异 | **19 项通过**。覆盖对象键顺序、其他插件、有效值、来源层、已采用参数、权限规则/目录/受管标记，以及缺失/无效数据、原文件格式错误、两个阶段的故障和候选 fd 丢失 |
| Go 接收与预览 | 定向日志 **113 条 PASS**，含顶层和子用例；缺失/null、假摘要、错误路径/键、未退出、虚假一致结论等不能作为有效证据 |
| 原生 Runtime/创建回归 | **210 / 25 项通过**。已知冲突仍在原生启动前拒绝；普通创建保留原 profile/route，无额外模型输入 |
| 工具回归 | 当前开发二进制的 MCP **39 项**、插件 **33 项**通过，真实调用、账号绑定、逐次审批和原 ID 恢复保留 |
| 既有设置传输 | **18 项通过** |
| 浏览器 | **30 项通过**。真实 Handler 的正常/格式失败回复；额外差异、缺失依据、延迟、断连等为受控 UI 条件 |
| 静态与构建 | `internal/runtime`、`internal/app`、`internal/console` 全包 race 通过，分别 36.336s / 103.990s / 1.966s；tagged vet、gofmt、Node 语法、bridge、Web 类型检查/构建和开发构建通过 |
| 独立原始材料审计 | **174 项通过**。从原生协议重新计算全部比较摘要及差异，并核对 SQLite、hook 身份、供应商记录、旧证据及产物哈希 |

原生预览仍只有创建阶段的一次 `HEAD /api/hello` 探测，预览没有增加供应商请求。保留核对没有发送模型输入或新建执行候选。完整证据见 [原生结果](W02-09-claude-settings-preservation/raw/application/result.json)、[原生协议](W02-09-claude-settings-preservation/raw/application/native-settings-wire.jsonl)、[只读数据库投影](W02-09-claude-settings-preservation/raw/application/store-projection.json)、[审计](W02-09-claude-settings-preservation/audit.json)。

浏览器已目视核对[桌面视口](W02-09-claude-settings-preservation/preservation-1440.png)和[390 窄屏](W02-09-claude-settings-preservation/preview-390.png)。没有 JavaScript 错误，控制台唯一错误来自显式陈旧来源样本的 HTTP 409；页面重载后工具当前页计数为零，但全会话列表仍保留该错误，不用标题计数掩盖它。

## 保留的失败与修正

- 首次 TypeScript 编译未能从通用 record 的展开中推断 `source` 字段；明确保留该字段后构建通过。
- Runtime 回归最初两项失败来自旧夹具固定统计四行启动记录。新增原文件基线后，实际为两次版本查询、三次 bare 校验。已改为核对真实参数和“原文件 → 派生文件 → 独立创建校验的原文件”，没有放宽准入。
- 首张桌面元素截图包含滚动容器外的空白，已保留原图并补拍正常视口；没有为截图重跑原生任务。
- 首轮审计假定 hook marker 的顺序固定为 custom/project/plugin，实际为 project/custom/plugin。已改为每种 marker 及每个 hook ID 恰好一次，起止对应；未修改原始记录或生产代码。

详见[修正记录](W02-09-claude-settings-preservation/inspection-notes.json)、[初始 Runtime 回归](W02-09-claude-settings-preservation/runtime-regression.log)、[首轮审计](W02-09-claude-settings-preservation/audit-first.json)。全包 race 后只修正了 tagged Runtime 夹具的断言和截图方式；该夹具已重新编译、执行，tagged vet 随后通过，生产代码未再改变。

## 复跑与版本

在仓库根目录执行，使用已固定版本的 CLI/SDK 与可用 `bwrap`：

```sh
make claude-build web-build
go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
node tests/claude_settings_preservation_test.mjs
node tests/claude_settings_validation_test.mjs
python3 tests/claude_policy_helper_admission_probe.py --surface application
python3 tests/claude_policy_helper_admission_probe.py --surface runtime
python3 tests/claude_policy_helper_admission_probe.py --surface create
go test -race ./internal/runtime ./internal/app ./internal/console
go vet -tags nativeclaude,nativecodex ./cmd/... ./internal/... ./web
python3 tests/claude_mcp_selection_smoke.py --netcap .dev/bin/netcap
python3 tests/claude_plugin_execution_smoke.py --netcap .dev/bin/netcap
```

浏览器模板为 `tests/claude_plugin_settings_preview_browser.js`，已执行脚本、真实 Handler fixture 及补拍脚本均归档。原始目录在[目录索引](W02-09-claude-settings-preservation/original-directories.json)，[资料记录](W02-09-claude-settings-preservation/documentation.json)包含固定 SDK wire 类型和复用的官方受管权限文档；没有重复外部检索。原生 `get_settings` / `list_permission_rules` wire 请求没有冒称为公开 Query 方法。

开发二进制 SHA-256：`3ed0c8e357ee39909b4817ad636008ebf416da9cb0c2e8cce23991b7bb0eb94f`。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。源码/构建/测试二进制见 [manifest](W02-09-claude-settings-preservation/manifest.json)，变更见 [source.diff](W02-09-claude-settings-preservation/source.diff)，复制来源及完整证据哈希见 [archive-index](W02-09-claude-settings-preservation/archive-index.json)与[artifact-hashes](W02-09-claude-settings-preservation/artifact-hashes.json)。

**9,982 份旧 W 证据、31 份旧 goals 证据**以及硬要求文档未改。其他 bridge、依赖和已有启动来源读取器未改，SDK 来源观察无需重复执行。仅归档自有数据；完整数据库、账号文件及控制台访问 token 未复制，控制台访问设置行已脱敏。56 个已知原生/供应商/传输目录无自有存活进程或私有 settings 遗留，浏览器已关闭；未提交、推送或对外发布。

## 后续与硬要求

这片提供原文件与派生文件的**原生元数据比较依据**。完整有效权限的实际执行、受管/remote 来源真实性与新鲜度、helper 执行准入、读取后竞态和已运行查询的动态策略仍未完成。正式已安装插件持久候选、原 ID 应用事务与故障恢复、四配置联合、真实方法/Gate、独立前台、共享后台/输入独占、Windows 第二有效环境和全部首版条件保持。

下一片复用这些保留依据连接持久候选，并继续完整启动准入核对；不会以 metadata 相等代替权限执行验收或扩大正式选配范围。
