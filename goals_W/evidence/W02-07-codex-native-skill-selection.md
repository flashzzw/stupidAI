# W02.07 · Codex 原生目录 Skill 选配与恢复

2026-10-05。普通用户/项目目录 Skill 已接通准确入口映射、完整资源版本、新建启停、原 ID 改配、原地升级、冷恢复及逐轮正文归属。最终生产原生链路 **66 项**、浏览器 **28 项**通过；原有 hook/共享 Skill 单文件/共享目录原生回归 **40/94/101 项**通过。方法使用、Gate、受管控制、Windows/proxy 和输入独占没有由这些结果证明。W02.07/W02.08 in_progress，整体 9/40 done，Goal active。

## 用户可见行为

能力资产新增可选 `codex_skill`，填写原生来源发现返回的绝对 `SKILL.md` 入口；`source` 仍登记该文件或包含它的完整目录。映射只作用于 Codex，资产仍可同时声明 Claude。未填写时保留已有共享 Skill 独立副本交付；已填写时使用原生目录，不复制为插件。映射必须指向登记的同一资源，不能用平台管理的导入副本冒充原生入口。

新建选择启用/禁用后自动记录完整版本，其他原生 `skills.config` 项保持继承。当前只接受原生清单中唯一的 user/repo 来源；同名、多路径别名、硬链接替代、插件/system/admin 来源、不完整清单和资源漂移均明确拒绝。单文件绑定只覆盖该文件；相对资料需登记目录。

原会话继续使用“预览 → 保存候选 → 准备并保持暂停 → 明确应用”。启停、撤去最后一个显式选择恢复继承、同路径 v1→v2 升级和后台冷恢复均按原 ID 执行。保存不改变当前快照；原地编辑会让旧绑定拒绝输入，不自动将其他会话提升至新版本。执行详情显示原始入口、资源类型、来源类别和“原生来源与本连接 Skill 配置已核对”。该范围与逐轮描述/正文记录分别展示。

“本条消息附带 Skill”可选已启用的原生目录 Skill。浏览器只提交资产/名称及当前快照；Runtime 从保存绑定解析入口和正文 SHA256。实际载入与原操作、原快照、原资源版本关联，升级后旧正文记录仍显示 v1。禁用不抹除历史，正文一致不证明模型采用方法或 Gate 通过。

## 实现与恢复依据

沿用 `SkillSelection`、`ExecutionSnapshot`、资源候选和持久操作；新 `codex_directory` 元数据保存完整文件清单和根权限。共享资源读取复用完整文件核对，不把原生目录转换为交付插件。`skills/list(forceReload)` 仅用于核对准确来源与完整清单，不能用其全局 enabled 值声称线程选择已恢复。

配置覆盖按精确原生路径合并 `skills.config` 数组，保留其他继承项；选择、原生路径和完整资源在输入前复核。新建、明确退订后改配及冷恢复重传选择，再保存仅当前连接有效的配置建立记录。该记录包括连接 ID、选择摘要及精确 JSON 数字的配置指纹；复用上一片 hook 的指纹计算，分别保存 Skill/hook 依据。候选预读不删除当前依据；没有本连接记录、配置漂移或来源漂移时不能继续输入。

原地升级必须能替换已经变化的旧目录文件。本片只在明确改配时允许撤去或替换这类旧引用：原执行身份、路由、配置、所有未改变的依赖和所有候选资源仍须核对。Runtime 另要求原选择仍有本连接建立依据，再只读核对原历史、队列、后台执行和未决请求；源码变化不能替代该依据。内部仅用于改配的标志不接受 JSON、不持久化，也不用于普通输入。未知的实际占用、连接或配置不会因升级被清除。

应用依赖本连接 `unsubscribed`/`notLoaded` 依据及完整空闲/历史复核，不要求全局卸载，不停止共享后台。恢复继承即使目标没有任何 Skill 行，也须取得当前连接的实际配置建立记录；空候选或丢回执不能推断已应用，核对不会重放改配。已加载、来源不明的会话仍无法从旧历史取得新输入资格。

## 实际验证

| 层次 | 结果与依据 |
|---|---|
| Application | 创建保留原生入口；10 类登记/映射场景；明确升级只豁免待替换旧目录，未改变依赖漂移仍拒绝；[日志](W02-07-codex-native-skill-selection/logs/app-final-shared.log) |
| Runtime | 16 类启停、候选预读、缺依据、配置/文件/权限漂移、来源缺失/同名/别名/受管/插件/不完整清单及未应用空候选场景；漂移后只读准备与普通输入分别核对；[日志](W02-07-codex-native-skill-selection/logs/runtime-final.log) |
| 最终原生生产链路 | Application → Runtime → Codex stdio → Handler，66 项通过；[摘要](W02-07-codex-native-skill-selection/native-final/summary.json)，完整 result/browser-responses gzip 无损保存 |
| 受影响原生回归 | hook 选配 40 项，共享 Skill 单文件 94 项、目录 101 项通过；[hook](W02-07-codex-native-skill-selection/regression-hooks/summary.json)、[文件](W02-07-codex-native-skill-selection/regression-shared-file/summary.json)、[目录](W02-07-codex-native-skill-selection/regression-shared-directory/summary.json) |
| 浏览器 | 最终构建 28 项通过；[结果](W02-07-codex-native-skill-selection/browser.json)、[新建 1440px](W02-07-codex-native-skill-selection/create-1440.png)、[历史详情 1440px](W02-07-codex-native-skill-selection/execution-1440.png)、[漂移详情 390px](W02-07-codex-native-skill-selection/drift-390.png) |
| 回归、静态与构建 | Application/Console 全包 race、相关 Runtime race、vet、make dev-build 通过；[日志](W02-07-codex-native-skill-selection/logs) |

最终原生目录 `/tmp/netcap-w0207-native-skill-213851013`。A 用户启用/项目禁用，B/C 用户禁用/项目启用；用户资产为含相对资料的目录，项目资产为单文件。十一个明确中止的轮次分别核对描述、未选择项的保留、显式正文与原操作/版本。A 原 ID 启停；B 分别撤去两个显式选择，确认恢复原生默认；后台重启后原 ID/选择仍保留。A 原地更新并明确升级 v2，再冷恢复；C 仍绑定 v1 并拒绝漂移输入。A 的历史 v1 正文归属保持。原 profile 文件不变，四个自有后台退出 0。

原生版本为 app-server 0.158.0，使用既有隔离 HOME/profile/workdir、合成认证及自有 stdio fixture。供应商只尝试不可用本地地址，观察到连接错误后 interrupt；没有成功模型响应、工具/hook 调用或方法 Gate。没有创建 listener、修改用户原生配置或重启共享后台。已验证的 Unix stdio 不代表生产 Unix proxy/peer 或 Windows 链路。

浏览器使用真实 Application 初始快照、原生上下文记录、改配预览及最终 Handler 导出。HTTP/SSE 和 command ACK 为受控传输；浏览器创建、发送与保存测试只检查实际 UI 请求契约，不称浏览器真实原生执行。最后一次构建仅改变共享资产的后端声明准入，Web JS/CSS 与已测浏览器字节一致。

## 失败及修复记录

- 初始 Application 创建把明确原生映射转换成私有插件；[失败](W02-07-codex-native-skill-selection/logs/app-red.log)由独立原生目录绑定修复。
- 首次原生检查在状态已 idle、来源观察尚未写回时断言，两个来源核对断言失败，其他流程通过；改为等待真实 SkillEvidence。[结果](W02-07-codex-native-skill-selection/failed-early-observation/summary.json)保留，不声称该轮全通过。随后基础链路 47 项通过。
- 原地升级首个夹具修改了与配置共享的资产 slice，误让 v2 成为当前登记版本；复制配置 slice 后再验证。该次 [46 项后中断](W02-07-codex-native-skill-selection/failed-upgrade-fixture/summary.json)属于夹具错误。
- 真实原地升级被旧执行来源检查阻断，47 项后中断；[原失败](W02-07-codex-native-skill-selection/failed-upgrade-source/summary.json)、[日志](W02-07-codex-native-skill-selection/logs/native-upgrade-source-red.log)。修复严格限定于明确替换/继承的原生目录资源，先有 54 项通过，再扩展为最终 66 项；常规输入、未改变依赖和未知连接仍有拒绝测试。
- 浏览器第一轮使用了不存在的“取消”按钮，所有前序动作已执行，但脚本未完成；改用实际“关闭能力管理”，最终 28 项通过。[失败日志](W02-07-codex-native-skill-selection/logs/browser-first.log)保留。

中断的运行即使已记录 checks 全 true，也仍为整体失败；仅采用各摘要的 passed 与实际日志共同判断。完整 Runtime 包的既有 OpenCode listener 环境失败未纳入通过声明，也没有重复环境探测。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^(TestCodexNativeDirectorySkillSelection|TestCodexNativeHookSelectionAndTrustChanges|TestCodexNativeSharedSkillFile|TestCodexNativeSharedSkillDirectory)$' -count=1 -v
```

[浏览器模板](../../tests/codex_directory_skill_browser.js)中的 `__FIXTURE__` 使用最终原生 browser-responses.json，`__ASSETS__` 映射 web/dist；经 Playwright MCP 运行。[prepare_browser.py](W02-07-codex-native-skill-selection/prepare_browser.py)记录本次具体路径。全包回归后只增加定向用例、放开该映射对同时声明 Claude 的共享资产的准入，并重新运行对应 Application、Runtime 定向检查、vet 和最终构建；未无理由重复全部原生轮次。

旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。最终 `.dev/bin/netcap`：`b825dd6b9cd2030ec003ebf975c132dedc73efdc0e70226d10a4057a4c5b62ff`；Web 为 `index-Dv170Vq0.js` / `index-DSkOCaGd.css`。2657 个旧 W 证据未改。源码/构建/本片归档哈希见 [manifest](W02-07-codex-native-skill-selection/manifest.json)，变更边界见 [audit](W02-07-codex-native-skill-selection/audit.json)与 [diff](W02-07-codex-native-skill-selection/changes.diff)。未提交、推送或发布。

下一片核对普通目录 Skill 与插件/hook 的组合改配、实际回执丢失恢复及来源解释，继续关闭 W02.07/08 的完整选择缺口。真实方法/Gate、受管/完整来源、成功与异步 hook、通用 hook、Windows/proxy、输入独占及四个既有 WSL 请求继续保留；本片不提升整项完成状态。
