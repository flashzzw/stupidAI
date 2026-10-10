# W02.08 · Claude 项目与本地设置版本

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 结果与边界

新建的普通 Claude 执行在不可变快照中保存 `claude_local_settings`：版本格式 1，以及实际 cwd 的 project/local、支持的 Git 布局下主仓库 local 的文件字节 SHA-256、Go FileMode、解析路径和存在状态。沿用上一片真实来源根算法，不复制 SDK 的不完整 resolver。缺失 `.claude` 目录不会被创建；损坏的父目录链接不会被当作普通缺席。

来源读取按保存的 cwd/profile 重新计算当前路径集合，不能直接信任历史记录中任意路径。内容、权限、链接目标、存在状态及 Git/cwd 来源集合变化均报告漂移；新增来源路径单列。复用公共命令与持久意图派发前的检查阻止输入、审批和普通能力改配。普通能力改配候选继承旧本地版本，不顺便升级。老快照没有新字段时继续保留“未记录”，不迁移或补造历史。

执行详情及历史快照显示文件版本、缺失路径和权限。来源核对失败、切换快照或断线时清除旧结果；响应的快照 ID 不匹配时拒绝显示，断线前的迟到响应不能带回旧成功状态。

**本片只绑定并核对文件版本，不冻结原生文件。** 文件读取与 native 后续读取之间不是原子事务，也不证明原生实际加载、全部受管/helper/服务端策略或热更新状态。原 ID 显式采纳新本地设置、可靠受管预检和已安装插件事务尚未接通。恢复原文件可重新核对，但失败操作不会自动重放。Git 特殊布局和设置环境对定位的影响仍沿用上一片限制。

## 实际验证

| 层次 | 结果 |
|---|---|
| Application 原始失败 | [app-red.log](W02-08-claude-settings-versions/app-red.log)：合法本地配置 v1→v2 未被记录或报告漂移，发送仍被接受。 |
| 原生原始失败 | [native-red.log](W02-08-claude-settings-versions/native-red.log)与[结果](W02-08-claude-settings-versions/native-red.json)：SQLite 保存 create 意图后把本地文件改为 v2，旧实现仍创建 Claude 并实际执行 v2 SessionStart hook。 |
| 最终 Application/Runtime | [10 项](W02-08-claude-settings-versions/native-final.json)通过：上述持久化竞态在派发前拒绝、操作为 not_sent、错误指向具体文件、无 bridge/启动 hook；恢复 v1 后快照不变、可核对且不重放。这条绿色路径在 native 启动前终止。 |
| 已运行会话 | [12 项](W02-08-claude-settings-versions/application-final.json)通过：发送意图落盘后才修改 local 文件，持久拒绝、原 ID/Scope/快照保留、无模型输入。旧身份覆盖测试改为在落盘接缝注入，继续覆盖派发拒绝路径。 |
| 文件与候选边界 | 新增 4 个顶层测试，其中 7 个变化场景覆盖新增/删除/权限/文件及父目录链接/损坏内容/Git 来源图；另验来源恢复、普通改配不能升级、旧落盘记录不补造。全包 race 通过。初轮候选测试误用不存在的 `change_execution`，失败记录保留在 [app-boundaries.log](W02-08-claude-settings-versions/app-boundaries.log)；改为既有 `stage_execution` 后全包通过。 |
| 浏览器 | [27 项](W02-08-claude-settings-versions/browser.json)通过：采用真实 Application 响应回放 HTTP/SSE，合成历史/旧格式与连接变化仅验证界面。原始 UI 成功残留 [red](W02-08-claude-settings-versions/stale-read-red.json)→[green](W02-08-claude-settings-versions/stale-read-green.json)。[桌面](W02-08-claude-settings-versions/local-1440.png)与[窄屏](W02-08-claude-settings-versions/drift-390.png)已目视检查，未发现横向溢出。没有发送执行命令。 |
| 独立核对 | [24 项](W02-08-claude-settings-versions/audit.json)通过，从文件标记/hash/权限、只读 SQLite 投影与原生日志核对红/绿、持久 not_sent、原快照和零输入；不把生产测试布尔值当作独立依据。 |
| 原生回归 | [Skill 原 ID 改配 37 项](W02-08-claude-settings-versions/skill-regression.json)、[插件选择 9 项](W02-08-claude-settings-versions/plugin-regression.json)通过。真实会话启动/改配，供应商不可用且没有模型输入。 |
| 工程检查 | app/console 全包 race、Claude Runtime race、带 nativeclaude 标签的 vet、TypeScript typecheck、Vite/Go 开发构建通过；保留 [app/console](W02-08-claude-settings-versions/app-console-final.log)、[Runtime](W02-08-claude-settings-versions/runtime-final.log)、[vet](W02-08-claude-settings-versions/vet.log)、[构建](W02-08-claude-settings-versions/build.log)与[原生](W02-08-claude-settings-versions/native-final.log)日志。 |

最终竞态目录 `/tmp/netcap-w0208-settings-version-941976971`，已运行会话 `/tmp/netcap-w0208-local-settings-app-1915234767`，红轮 `/tmp/netcap-w0208-settings-version-1227511432`；回归目录见[原始目录](W02-08-claude-settings-versions/original-directories.json)。固定 CLI **2.1.280**、SDK **0.3.280**，隔离 HOME/XDG/profile、临时仓库、测试 hook 和不可用 loopback 供应商。红轮执行了一个自有启动 hook；本片最终启动拒绝路径没有 native 进程。其他自有查询均已关闭，保存日志没有模型 input/result；浏览器已关闭，无提交、推送、共享后台或新宿主操作。

用户再次提供的 `/tmp/netcap-console-bp2rp6xu` 与既有插件执行 33 项归档一致，不计为本片新原生执行。五项既有 WSL 请求继续分别待结果，不重复索取。

## 复跑与归档

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app \
  -run '^TestClaudeNative(SettingsVersionBeforeStartup|ApplicationLocalSettingsRejection)$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app \
  -run '^TestClaudeNativeApplication(SkillChanges|PluginSelection)$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 goals_W/evidence/W02-08-claude-settings-versions/audit.py
```

浏览器模板 [claude_settings_versions_browser.js](../../tests/claude_settings_versions_browser.js) 的 `__FIXTURE__` 替换为 [browser-responses.json](W02-08-claude-settings-versions/browser-responses.json)，`__ASSETS__` 为当前 `web/dist` URL→绝对路径映射；当前已执行脚本亦归档。HTTP/SSE 不需要监听，不代表生产 HTTP 或宿主验收。

[source.diff](W02-08-claude-settings-versions/source.diff) 相对本片 before 目录记录源码/使用说明/进度变化；[manifest.json](W02-08-claude-settings-versions/manifest.json)记录本片产物哈希和旧证据核对。没有复制配置正文、认证文件或完整 SQLite；独立核对只保存必要投影。

- 开发二进制：`3e9a0c8113d4622f1bdcdeb4686c1784d940a775059afe5a9bc54e339e0b7d7e`。
- 旧 bin：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，未改。
- **3435** 个既有 W 证据逐文件哈希不变。

下一片继续完整启动前来源/策略核对及安装插件事务所需的明确配置采纳；完整方法任务/Gate、原生丢确认恢复、输入独占、Windows/proxy 和其他 hook 条件继续保留，不据本片关闭 W02.07/W02.08。
