# W02.08 · 原 ID 明确采纳本地设置版本

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 生产行为

新建普通 Claude 执行的 `claude_local_settings.version=2` 将完整来源记录绑定到快照、Scope 与原生 Session 配置。内容、权限、解析路径和存在状态参与核对；旧格式 1 继续只保留原记录，不自动获得原生来源绑定。阻止机制版本仍为 1。

详情提供明确勾选的“采纳当前项目/本地设置版本”。`adopt_local_settings:true` 可与空能力选择一起预览，展示旧文件和候选文件；保存不生效。准备与应用只接收已保存快照/候选引用，复用原事务，要求原查询存活、阻止机制已核对注册且原执行空闲。仅采纳相同来源路径集合内的当前版本；Git/cwd 来源集合变化、旧格式、身份覆盖、profile/custom 漂移和其他能力依赖漂移均不会随之放行。

原生 prepare 核对具体候选来源；apply 将 `settings_previous/settings_target` 与操作、历史基线一并持久记录，关闭旧查询后按原 ID 打开候选。候选初始化与提交前复核文件、清单和历史；成功才晋升准确候选，Scope 同步，旧快照完整进入历史。仅修改保存 hash 不能满足原生核对。

关闭旧查询后的失败可能已经执行候选启动 hook，因此保留原快照和 `uncertain/applying`，不标成安全未发送，不自动回滚或重放。实际故障样例中的候选 v2 启动 hook 将文件改为 v3：初始化后检查失败，重启后依然待核对，候选启动只执行一次。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| 正常原 ID 应用 | [14 项](W02-08-claude-settings-adoption/normal/result.json)通过：准确候选、保存/准备保留原版本、历史、原生绑定、幂等与实际 v1→v2 hooks。 |
| 应用层丢回执 | [15 项](W02-08-claude-settings-adoption/lost/result.json)通过：真实 Runtime 成功后包装适配器丢弃结果，仅只读 reconcile 读取已持久原生结果；未模拟 CLI 进程内回执丢失。 |
| 正向冷恢复 | [17 项](W02-08-claude-settings-adoption/cold/result.json)通过；另有 [Session 冷恢复 5 项](W02-08-claude-settings-adoption/cold/cold-result.json)。App 关闭后，公开生产 `Session.restore().open()` 从持久来源重开，再重启 App 核对同一快照/绑定；没有真实模型历史输入。 |
| 意图落盘后、派发前漂移 | [9 项](W02-08-claude-settings-adoption/before-dispatch/result.json)通过：改为 v3 后记录 `not_sent`，零原生 apply，仅原 v1 hooks。 |
| 候选启动后漂移 | [11 项](W02-08-claude-settings-adoption/after-startup/result.json)通过：候选 hook 已执行，保留 `uncertain/applying`，重启不晋升、不重放。 |
| 浏览器 | [25 项](W02-08-claude-settings-adoption/browser.json)通过：明确选择、路径/hash/权限差异、失败预览清除、断线、保存、阻止机制不可用、准备/应用、旧历史和准确命令引用；桌面/窄屏截图已检查。HTTP/SSE 和三个命令回放真实 App 响应，不是生产 listener 验收。 |
| 独立核对 | [79 项](W02-08-claude-settings-adoption/audit.json)通过：重新比较 SQLite 只读投影、bridge metadata、journal、实际 hooks、磁盘文件 hash 和浏览器命令体；不以 producer 的 passed 字段为依据。 |
| 文件读取边界 | [Node 5 项](W02-08-claude-settings-adoption/source-boundaries.log)通过：内容/权限、链接、缺席/损坏父链接、类型/大小与协议边界。原阻止机制 [6 项](W02-08-claude-settings-adoption/guard-regression.log)通过。 |
| Application 候选边界 | [3 个顶层测试](W02-08-claude-settings-adoption/adoption-boundaries.log)通过，包含 profile/custom/Skill/身份/无变化 5 个子场景，以及格式 1 重启后拒绝采纳。候选再次变化拒绝准备、撤销不改原版本也已验证。 |
| 回归与工程检查 | 原生 Skill 改配 [37](W02-08-claude-settings-adoption/skill-regression.json)、插件选择 [9](W02-08-claude-settings-adoption/plugin-regression.json)、阻止机制 [10](W02-08-claude-settings-adoption/guard-regression.json)项通过；app/console 全包 race、Claude Runtime race、tagged vet、bridge 构建、Web typecheck/Vite 和开发 Go 构建通过。 |

五类主场景合计 **66 项**；重复运行不累加。浏览器初轮等待保存后的 UI 超时，原因是回放脚本漏发 SSE 变化通知，补齐后最终通过，[初轮记录](W02-08-claude-settings-adoption/browser-initial.log)保留。`initial-tests.log` 的 Runtime 正则未命中，后续 `runtime.log` 已真实运行 Claude 测试；不把首次空跑当作覆盖。

## 复跑与隔离

```bash
npm --prefix bridges/claude run build
node tests/claude_settings_sources_test.mjs
node tests/claude_settings_guard_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app -run '^TestClaudeNativeSettingsAdoption' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app -run '^TestClaudeNativeApplication(SkillChanges|PluginSelection|SettingsGuard)$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 goals_W/evidence/W02-08-claude-settings-adoption/audit.py
```

固定 Claude **2.1.280**、SDK **0.3.280**。独立项目/profile/状态目录、自有测试 hooks、不可用 loopback 供应商，没有模型输入或外部供应商调用。原目录见 [original-directories.json](W02-08-claude-settings-adoption/original-directories.json)。数据库只归档必要 JSON 投影，不复制完整数据库或凭据文件。

浏览器[模板](../../tests/claude_settings_adoption_browser.js)使用 [browser-responses.json](W02-08-claude-settings-adoption/browser-responses.json)与当前 `web/dist` 资源映射；[执行脚本](W02-08-claude-settings-adoption/browser.js)、[桌面](W02-08-claude-settings-adoption/preview-1440.png)、[窄屏](W02-08-claude-settings-adoption/preview-390.png)已归档。所有命令句柄与浏览器已收取终态。

- 开发二进制：`8c11137dcfa315928495989c3f3ea68582239c1facaebb227cfe472d18c8b31e`。
- 旧 bin 未改：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。
- **3738** 个旧 W 证据逐文件 hash 不变。
- [source.diff](W02-08-claude-settings-adoption/source.diff)、[manifest.json](W02-08-claude-settings-adoption/manifest.json)记录本片归档时点的源码/构建/证据；后加候选边界测试不改变被测生产构建。

## 仍未覆盖

此片不证明全部有效配置、受管文件/注册表/服务端策略、脚本资源、安装来源和权限合并已固定，也不提供原子读取或全局输入锁。原查询离线后不能直接采纳；旧格式 1 与 Git/cwd 来源集合变更仍拒绝。坏设置 schema 的原生检查已接入，尚无单独原生故障样例；真实受管限制、SDK/CLI 通道失联、在途模型任务及候选失败的后续人工解决路径仍需继续验证。

五项既有 WSL 请求分别待结果，旧 `/tmp/netcap-console-bp2rp6xu` 仅为已归档的插件执行证据。实际方法/Gate、原生进程内丢确认恢复、输入独占、Windows/proxy 与其他 hook 条件保持。没有提交、推送、部署或修改旧 bin。

下一片优先补候选设置解析错误、受限 hook 策略与混合资源的原生准入边界；根据新证据决定后续处理，不把本片局部成功提升为 W02.08 完成。
