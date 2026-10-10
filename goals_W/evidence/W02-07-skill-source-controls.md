# W02.07 Skill 来源控制范围与保存选择

日期：2026-10-05。W02.07/W02.08 继续 in_progress，9/40 done，Goal active。

## 本片结果

Codex 的来源发现接口与界面现在区分普通目录入口、插件整包、暂无独立控制入口、歧义和待核对来源。已保存的原生目录选择单列资产、版本、快照、模式及路径；来源缺失不会清掉保存记录，同名其他路径不会替代原路径。删除了“尚未接通逐会话启停”的过时提示。

原生实测目录 `enabled=true` 与会话保存 `disabled/v1` 同时成立。资产库登记 v2 后保存版本仍是 v1；清单从同名、硬链接别名、不完整到修复、原路径移除时，控制提示与缺失说明相应变化。发现不修改配置、历史、输入归属或执行核对结果，来源漂移后的输入仍被拒绝。

## 契约与边界

- Application 返回 `SkillDiscovery`，保留原有 `SkillInventory` JSON 字段，增加 `snapshot_id`、`controls` 和 `saved_selections`。Runtime 的原生目录清单契约未变。
- 控制提示最多检查 2048 项，逐入口 `os.Stat` 后用文件身份识别别名；不为展示读取全部资源正文或完整资源树。目录清单、入口身份只是采样，并非原子文件系统快照。内容、权限和指向在查询后或采样过程中发生变化仍需登记、应用及输入检查，不宣称完整版本已核对。
- 普通 user/repo 来源在同名或别名时显示歧义；清单不完整或任一入口身份不可读时保留待核对。普通插件来源解释为整包控制；system/admin 限制优先于插件标识，不声称独立排除或覆盖原生策略。没有创建清单未返回的插件条目，也没有把普通清单提升成线程的实际插件清单。
- 保存选择只来自会话当前保存快照，不读取最新能力库来重标资产版本。查询期间更新资产库的测试证实旧版本仍保持；不存在“旧登记解释加新清单”的拼接。路径是否在清单中仅按准确路径判定，不证明相同内容或实际加载。
- 外部文件身份采样在 Application 状态锁之外；完成后仍复核 Generation、Scope、原执行绑定和配置实例。来源内容漂移仍允许发现，但不解除输入限制。
- 父组件已按快照 ID 和连接状态重建面板。本片没有额外增加请求生命周期状态；浏览器复验了旧保存选择不会从迟到请求进入另一会话，断连及刷新失败会清空旧结果。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| Application 控制解释 | 15 类场景通过：user/repo、plugin/system/admin/受管插件/未知类别、同名、软/硬链接、查询期间删除、不完整、矛盾 complete/error、非文件入口、超限。另有保存 v1/禁用与查询期间库更新 v2/目录启用分离、缺失路径不按同名替代测试。 |
| Application 既有边界 | 原来源漂移、状态不变、Generation/Scope/实例变化与错误来源测试通过。 |
| Application / HTTP | `go test -race ./internal/app ./internal/console -count=1` 通过，含既有发现接口 token/origin/路径覆盖/null/缺会话边界。 |
| 新原生链路 | `TestCodexNativeSkillSourceControls`，真实 0.158.0 stdio → Runtime → Application/SQLite → Handler，15 项通过。一个自有后台退出 0；两个自有会话，零 turn/model/tool/hook。 |
| 新浏览器链路 | 生产构建、原生 Handler 导出回放，23 项通过；桌面 1440 与窄屏 390 已查看，无横向溢出。HTTP 交付、绑定变化错误和连接时序受控，不是真实监听器验收。 |
| 既有发现回归 | `TestCodexNativeSkillDiscovery` 原生 12 项通过；对应浏览器 18 项通过，覆盖旧 JSON 消费方式、部分扫描/修复/删除/传输失败/切换/断线。两个自有后台均退出 0。 |
| 静态/构建 | app/console/runtime vet、nativecodex tagged vet、gofmt、`make dev-build`（含 TypeScript 与 Vite）通过。 |

受管插件优先级在最终核对中补了先失败后通过的回归测试：本片最初将 `scope=admin` 且带 `plugin_id` 的条目归为普通插件，修正为受管限制优先。完整 app/console race 后新增该边界，最终又执行全部 `TestSessionSkill*` 的 race、Application vet、原生 15 项与开发构建，均通过。该受管条目为 Application fixture，不能替代真实受管来源验收。

初次原生测试的 command ID 含空格，被公共校验拒绝；修正测试 ID 后通过，失败日志及原生结果保留。初次浏览器定位器匹配了新增的上下文/hook 面板，发生 strict-mode 错误；修正两个来源发现脚本的定位器后通过，失败记录保留。两个失败均在测试夹具，未修改生产规则以迁就测试。

最终浏览器复跑曾在重连时超时：脚本打开了 React 尚未替换的旧面板，随后连接状态更新按既有 key 重建面板。增加等待可见“控制台已连接”后再展开的同步条件，新增 23 项和既有 18 项均再次通过；超时记录保留。没有修改生产重连行为。

浏览器的 `pageerror` 为空；故障测试刻意返回 409/503，因此网络控制台出现对应失败请求，不称网络全成功。plugin/admin 分类使用 Application fixture，未取得它们的完整原生控制或排除验收。

## 复跑与产物

工作目录：`/tmp/netcap-w0207-source-controls-work`。最终新原生：`/tmp/netcap-w0207-source-controls-3727316543`；受管优先级修正前的通过记录：`/tmp/netcap-w0207-source-controls-3135814862`；初次失败：`/tmp/netcap-w0207-source-controls-2602926011`；原生回归：`/tmp/netcap-w0207-native-skills-170962207`。最终和初步新流程各一个自有后台，回归两个自有后台，均退出 0。

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^(TestCodexNativeSkillSourceControls|TestCodexNativeSkillDiscovery)$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app -run '^TestSessionSkill' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./internal/app ./internal/console ./internal/runtime
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/runtime
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器脚本：[新增来源控制](../../tests/codex_skill_source_controls_browser.js)、[既有发现回归](../../tests/skill_discovery_browser.js)。将 `__FIXTURE__` 替换为对应 `browser-responses.json`，`__ASSETS__` 替换为当前 web/dist URL→文件路径映射，写入项目 `.dev/w0207-source-controls/` 后使用现有 Playwright MCP 的 `browser_run_code_unsafe` 执行。原生连接使用已存在的测试 stdio 接缝，未改变生产 transport 或绕过当前 listener/IPC 限制。

归档目录：[结果、源码、日志、截图](W02-07-skill-source-controls/)。[审计](W02-07-skill-source-controls/audit.json)确认 2807 个旧 W 证据未变，旧 `bin/netcap` 未变；仅构建开发产物。原生调用只使用临时 HOME/profile/project/SQLite 和合成供应商配置，没有模型请求、真实凭据调用、用户后台修改、提交或推送。

- `.dev/bin/netcap`：`6fe7e0aece01a8b3a1dddccb1d6793617f36360ceedce39305909ee51efa40e2`
- `index-CO0YbBKa.js`：`692b97f3ac1d41930622e634af4e8c9cc9a68773214770ced8a2de9717e95f7b`
- `index-DSkOCaGd.css`：`3781875c9ca317492b8773f0b67767aa507fe40eca497aedb876f6190b6c845a`
- 旧 `bin/netcap`：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`

## 保留缺口与下一步

本片补齐来源解释，没有完成受管/系统/安装来源的独立控制、完整策略排除或方法/Gate。继续处理这些来源的真实准入与故障解释，并收取已有 WSL 探针结果；不重复已发出的四项请求。Native→Runtime 丢回执后的安全恢复、仍存活共享后台、多客户端输入独占、Unix proxy/peer、Windows、成功/异步/通用 hook 等硬要求仍保留，不能以保持未知或目录可发现代替通过。
