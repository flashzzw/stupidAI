# W02.07 · Claude 外部 Skill 资源加载

2026-10-02；W02.07 保持 in_progress，整体 9/40 done。此片贯通新建时的外部资源选择、保存绑定、原生加载、清单核对和界面解释；不关闭方法 Gate。

## 实现与原生依据

- 对已有唯一用户/项目入口继续使用原生目录键 override；没有自然入口的已登记目录或 SKILL.md 单文件，改用每工作台会话、每资产独立的本地 Skill 包。绑定记录资源种类、路径、内容戳、原生命名空间与加载入口；全部字段保持标量，原 ID 比较和恢复不依赖指针身份。
- capture/preview 只保存计划。Session.open 才在自己的 Runtime 状态目录生成包，固定 manifest 只声明名称、版本和说明，包根只容纳 `.claude-plugin` 与 `skills`。目录资产链接整个已登记目录；单文件资产只链接 SKILL.md，不引入其父目录其他文件。资产根的 hooks/MCP/agents 不被当作插件组件；Skill 正文本身声明的原生行为仍属于所选方法，不能将包装解释为权限沙箱。
- 启用通过固定 SDK 的 `plugins` 和 `pluginDelivery: initialize` 加载，并要求 `initializationResult().plugins_applied === true`。禁用省略该包，核对生成命名空间（包括命令别名）未出现。已有不同内容、目录链接、额外组件、链接变化或缺失文件均拒绝，不覆盖修复未知包。
- 原生命令保留 aliases，描述保留 pluginName 归属；匹配需要名称、来源、包归属和别名唯一。自然来源 override 前后比较其他继承项；外部加载的继承保持由本片 A/B/C 原生对照验证，不声称启动前后已经取得同一查询的无插件基线。输入及权限回复前复核；新自然入口和资源内容戳漂移阻止发送。
- 详情区显示 Skill 名称、原生键、资源来源及加载/省略说明。快照仍保存完整原生名称；单纯清单匹配不显示正文加载、方法使用或 Gate 通过。

接口依据为固定 Claude Code 2.1.280 / Agent SDK 0.3.280 的本地类型与六场景原生实验，辅以官方 [SDK plugins](https://code.claude.com/docs/en/agent-sdk/plugins)。目录链接、文件链接和 initialize 均暴露插件 Skill；按目录键或显示名设置 off 均未隐藏插件 Skill；省略包后目标消失、项目 Skill 保留。原始实验 `/tmp/netcap-w0207-skill-delivery-zku29d8o`，结果归档为 `W02-07-skill-resources-probe.json`；不将两个 off 观测包装成禁用成功。

## 实际验证

隔离 profile、项目、状态目录；真实 CLI 经自有 stdio Session 运行，供应商地址为不可用的本地端口，零模型输入/结果，无真实供应商或共享后台切换。

| 层次 | 入口及结果 |
| --- | --- |
| 生产 Session/SDK/CLI | `python3 tests/claude_skill_resources_native.py`：18 项通过，目录启用/禁用、单文件启用、其他继承保持、原 ID 重开、真实别名冲突、新自然入口拦截、无正文写入元数据。最终目录 `/tmp/netcap-w0207-skill-resources-i899ogl0`。 |
| Application/SQLite/Runtime/CLI | `NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags=nativeclaude ./internal/app -run '^TestClaudeNativeApplicationSkill(Selection\|Directory\|File)$' -count=1 -v`：自然来源 9 项、目录/文件各 13 项通过。资源版本漂移必须返回执行快照来源错误，native journal 无 input。最终目录分别为 `/tmp/netcap-w0207-claude-app-547006671`、`-2733074194`、`-3472012727`。 |
| 应用/Runtime 契约 | `go test -race ./internal/app ./internal/runtime -run 'TestClaude.*Skill' -count=1 -v`：Application 10 场景、Runtime 19 场景通过。包含仅捕获不物化、共享独立命名空间、插件归属/别名/额外组件、禁用命名空间别名和原绑定保护。 |
| Node 边界 | `node tests/claude_skill_resources_test.mjs` 18 项；`node tests/claude_skill_selection_test.mjs` 6 项通过。包含目录/文件隔离、禁用不创建、包篡改/逃逸路径/不完整包、同源新增入口与别名冲突。 |
| 浏览器 | `tests/claude_skill_selection_browser.js` 重放真实 Application 导出：目录 17 项、文件 17 项、自然来源回归 14 项。1440/390 截图，长路径不横向溢出，创建请求只传 asset ID/mode。HTTP、EventSource 和创建回执是受控传输，未验真实 listener。 |
| 回归与构建 | app/console 全包 race、Runtime Claude/Execution/NativeObservation/Skill 筛选 race、带 nativeclaude 标签的 app/runtime/console vet、MCP 认证契约与 Session 恢复 10 项、bridge tsc、`make dev-build` 均通过。所有 Go 命令使用 `GOCACHE=/tmp/netcap-w0206-auth-go-cache`。 |

最终开发构建 SHA256 和源码/日志/截图哈希见 `W02-07-skill-resources-manifest.json`。仅更新 `.dev/bin/netcap`，旧 `bin/netcap` 与此前 882 个证据文件保持不变。完整切片差异保存于同前缀 `-diff.patch`。浏览器原始数据来自首次原生应用三组目录 `-2001783165`、`-1903334851`、`-294715855`；最终原生重跑与之区分记录。首次浏览器回放缺少 favicon 映射产生 404，补齐后外部资源最终回放通过；不是产品请求失败。Playwright 曾拒读 `/tmp` 生成脚本，随后复制到允许的工作区 `.dev/w0207-resources` 执行。

## 未完成与下一片

本次没有模型 turn；原 ID 重开只证明保存身份/绑定与选择重应用，不是带实际方法历史的 resume 验收。目录资源的正文/相对脚本按需读取、真实方法任务及 Gate、Skill 原 ID 改配、共享与专属方法组合、受管/其他插件来源单项排除仍待验证。直接终端 resume 不等于重放 SDK 参数。包装和来源戳不冻结未登记动态依赖，也不替代文件权限或抹除历史。

完整 Codex 选择探针仍只有 metadata_only 结果；用户已收到 `python3 tests/codex_skill_selection_probe.py` 请求，本片未重复要求授权或重做环境探测。下一片优先准备 Claude 正文/辅助资源按需读取与方法任务验收，并接通同会话 Skill 改配；继续保留 Codex 完整请求证据缺口。W02.07 与 Goal 保持进行中。
