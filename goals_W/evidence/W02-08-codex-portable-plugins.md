# W02.08 · Codex 通用插件的 schema、来源与原 ID 选配

2026-10-05。带受支持 Agent Plugins schema 的根 `plugin.json` 已接通生产选配。根身份、内联 OpenAI 设置和旧 overlay 回退共用解析，执行快照记录实际身份/设置来源；新建启停、原 ID 升级/继承/重新添加、双 Skill 输入与冷恢复贯通。三种格式各 **116 项原生检查**、界面 **31/31/32 项**通过。W02.07/W02.08 仍为 **in_progress**，整体 **9/40 done**，Goal active。

## 实现与生效条件

`plugin_manifest.go` 将原有能力库解析提取为共享纯函数，能力诊断和执行选配使用同一份根身份、配置来源与覆盖规则。根 `name` / `version` 决定包身份；`extensions.com.openai` 存在时整体替代旧 `.codex-plugin/plugin.json`，空对象也算替代；缺省才采用旧 overlay。通用 Skill/MCP 始终来自根 `skills/`、`mcp.json`，不混入 overlay 中的旧组件位置。

固定 0.158.0 需要以下精确声明：

| 文件 | `$schema` |
|---|---|
| 根 `plugin.json` | `https://agent-plugins.org/schemas/1.0.0/plugin.schema.json` |
| 存在时的根 `mcp.json` | `https://agent-plugins.org/schemas/1.0.0/mcp.schema.json` |

缺少根 schema 时，原生可能忽略根身份、内联替代和根版本，继续按旧格式解释；缺少 MCP schema 时，该 MCP 清单不会加载。能力库保留来源登记/静态诊断能力，但明确显示 `native_schema_unverified` 警告；生产入口拒绝缺失或未支持的 schema，不能让静态可搬运与原生可加载混为一谈。

通用 stdio MCP 的 `command` 还必须是裸可执行名或包内 `./` 路径。绝对路径、没有 `./` 的路径和越界路径会被原生忽略，现已前移为明确诊断错误和选配拒绝。原目录不改写；原生正例同时覆盖裸 `python3` 与保留 0700 权限的包内 `./mcp-server.py`。

沿用完整目录/权限/摘要绑定和每会话独立缓存，根声明版本与资产版本各自保存；新增可选 `manifest_source` 记录身份文件、配置来源和格式，旧快照保持兼容。相等比较也包含这份元数据。界面展示根身份和实际配置来源，旧 overlay 的名称/版本不会替代它。生产不额外执行全局安装，观察与恢复也不修复缓存。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| Application | 根格式、非空内联、空内联、overlay 回退四场景；根身份/版本和实际来源、规范 MCP、Skill 名称保留。6 类 schema/命令反例在下发前拒绝，库诊断显示 schema 限制；被选中的未支持组件仍拒绝 |
| 原生根格式 | 116 项；包内相对 MCP 可执行文件、新建启停、原 ID 升级到根版本 2.0.0、继承/重新添加、双正文输入、冷恢复和真实丢回执同轮恢复不重发 |
| 原生内联 | 116 项；空内联对象排除旧 overlay 的 MCP、Skill 与 hook 声明，规范组件和根身份保留；相同生命周期及版本路径通过 |
| 原生回退 | 116 项；设置来源记录为旧 overlay，身份/版本及 Skill/MCP 仍按通用根规则；旧 overlay 的 MCP 路径不混入清单；相同生命周期通过 |
| 独立原生契约 | 14 场景、78 项；`plugin_sharing` 开/关、直接缓存与自有本地安装对照，分别核对无 schema、只有 manifest schema、完整 schema、无 schema 的内联、完整空/非空内联和完整回退。缺失条件的反例仍明确不具备通用 MCP 选择证明 |
| 原生回归 | 自定义 Skill 目录 116 项、外部共享 Skill 目录 101 项通过 |
| 浏览器 | 通用根 31、内联 31、回退 32 项；根身份/版本/设置来源、规范 MCP、旧身份不冒充、正文关联/历史版本、同 ID 重试/断连/移动布局；自定义目录 30、共享目录 38 项回归通过 |
| 检查 | app/console 全包 race、相关 Codex/异步/SkillContext race、最终 App 定向 race、nativecodex 标签 vet、`make dev-build`（含 web typecheck）、Python AST、归档与引用检查通过 |

生产原生固定后台 **0.158.0**、proxy 配置声明 **0.157.1**，使用自有 stdio fixture。每组十轮只尝试不可用的 `127.0.0.1:9`，全部明确 interrupt；其中七轮附带双 Skill，共享目录回归为单 Skill。没有成功模型响应、工具或 hook 调用，五个最终组均无 `hook/started` 通知；MCP 只初始化/查询清单。五组的 15 个自有后台全部退出 0。独立契约另外使用两个隔离后台与本地安装，无模型轮次和工具调用，两者退出 0。没有重探 listener/Windows，也未声称 Runtime 全包通过。

界面使用最终原生 Handler 导出，构建资源为 `index-DE872Chw.js`；HTTP/SSE、HTTP 回执丢失、内容不符和上限选项为受控回放，不作为新增原生执行。当前/历史路径和版本来自原请求，真实原 ID 应用由原生流程覆盖。上下文桌面/移动截图已检查，浏览器已关闭。

## 失败证据与修正

最初四个 Application 红例复现旧格式限定。放开根格式后，首轮三种生产原生测试均停在未验证状态且没有模型轮次：根 MCP 缺失，内联预期排除的旧 MCP 反而出现。原生日志、完整失败结果保留，未用修改预期将其标成支持。

随后以独立缓存/本地安装、`plugin_sharing` 开/关对照排除开关和单一路径的解释。无 schema 的内联确实使用旧 overlay；旧名称不符还会使安装被拒绝。补根 schema 后，根版本、规范 Skill 与替代规则开始生效，但 MCP 仍未出现。

重新读取[官方插件文档](https://developers.openai.com/plugins/build/plugins)后补齐两个 schema。生产流程通过，独立探针仍失败；该探针使用绝对 `/usr/bin/python3`，与生产裸命令不同。读取自有诊断数据库取得明确原生错误：`Agent Plugins stdio command must be a bare executable name or a contained ./ path`。改用裸命令后，最终 78 项契约检查通过；Application 新增绝对/越界命令红例，再补前置校验通过。完整失败样本和原生诊断保留。

[早期 W02.05 发现证据](W02-05-formats.md)没有携带这些 schema，只证明当时三类目录能发现 Skill；不能据此证明根身份、MCP、内联替代或运行版本。旧证据未改写，本片补充其实际边界并接通满足条件的生产路径。能力库静态格式解析仍不等于原生加载。

## 复跑与附件

```bash
python3 tests/codex_portable_plugin_contract_probe.py

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^TestCodexNative(Portable.*|CustomSkillRoots|SharedSkillDirectory)$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

最终原生根/内联/回退目录分别为 `/tmp/netcap-w0208-portable-root-1160721041`、`/tmp/netcap-w0208-portable-inline-2466586491`、`/tmp/netcap-w0208-portable-fallback-3507642912`；契约目录 `/tmp/netcap-w0208-portable-contract-kyruidbe`。附件：[三种原生结果/协议/rollout](W02-08-codex-portable-plugins/native/)、[78 项契约与协议](W02-08-codex-portable-plugins/contracts/)、[原生诊断](W02-08-codex-portable-plugins/loader-diagnostics.json)、[失败记录](W02-08-codex-portable-plugins/failures/)、[界面根格式](W02-08-codex-portable-plugins/browser-root.json)、[内联](W02-08-codex-portable-plugins/browser-inline.json)、[回退](W02-08-codex-portable-plugins/browser-fallback.json)、[截图](W02-08-codex-portable-plugins/browser/)、[官方资料摘录](W02-08-codex-portable-plugins/docs/)、[日志](W02-08-codex-portable-plugins/logs/)、[差异](W02-08-codex-portable-plugins/changes.diff)、[审计](W02-08-codex-portable-plugins/audit.json)、[清单](W02-08-codex-portable-plugins/manifest.json)。installationId/宿主标识脱敏，未复制认证文件。

浏览器入口 [codex_skill_input_browser.js](../../tests/codex_skill_input_browser.js)，将 `__FIXTURE__` 替换为相应 `native/<root|inline|fallback>/browser-responses.json`，`__ASSETS__` 替换为当前 `web/dist` 绝对路径映射；截图写入 `.dev/w0208-portable-<root|inline|fallback>/`。

开发构建 SHA256：`0e9c018873c5bcd8e79bec51d1b1f480b5e3ddb75a8843d3c011b1e5c8b65e82`。2096 个旧证据文件及旧 `bin/netcap` 不变，前置基线 `/tmp/netcap-w0208-portable-work/before`。未提交、推送或发布。

本片只接通已支持的本地 Skill/MCP 通用包；选中的 hooks、apps 和其他未知字段仍拒绝。下一片继续 hook 实际成功/失败归属与组件选配。普通原生目录恢复、完整方法/Gate、相对资料实际使用、hooks 异步、受管/安装来源控制、Windows/proxy、跨客户端输入独占及既有三个 WSL 待返回结果均保留。
