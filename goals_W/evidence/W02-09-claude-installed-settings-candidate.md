# W02.08 / W02.09 Claude 已安装插件设置的持久候选

日期：2026-10-09。结论：已安装插件的启用/排除设置可明确保存为待应用方案，沿用现有版本、SQLite 回执和撤销流程；当前执行保持原配置。此候选仍不能准备或应用。W02.07–W02.09 保持 `in_progress`，全线 9/40 done，Goal active。

## 实现与范围

- `POST /api/sessions/{id}/plugins/settings-stage` 仅接受请求 ID、当前快照 ID、完整插件来源键、模式和登记版本。服务端重新执行原文件/派生文件的原生比较；客户端不能提交核对结论、文件路径或快照。未通过格式、目标值、其他设置/权限保留、来源稳定性或私有文件回收检查时不保存。
- 保存绑定安装登记与声明文件版本、声明插件目录的完整资源摘要，以及除采样时间外的完整启动来源观察。后者包含文件、来源层、路径身份、注册表结果和读取限制。资源树使用现有 2048 项 / 32 MiB 规则，拒绝嵌套链接；它仍不是原生实际加载目录的证明。
- 复用 `PendingExecutionChange` 的快照版本链、命令回执和 `execution.pending` 提交。新候选采用格式 2，原生执行快照仍为格式 1；普通表单不能静默替换该候选，准备、应用和 dispatch 重核均拒绝。撤销仍用 `discard_execution`。
- 保存不改变当前快照、Scope、Generation、控制方式、暂停状态或在途操作。相同请求并发重试共用核对，提交后和重启后直接返回原回执；不同请求竞争只保存一个方案。撤销后的旧请求重试不会重新创建候选。
- 界面展示保存时的来源依据及“尚未生效”，禁用准备/应用和普通替换，保留撤销。请求 ID 跨丢回执和页面刷新保存。原生 I/O 位于 Application 锁外，提交前复核原快照、输入归属和来源；关闭/取消仍清理自有文件。

## 实际验证

| 层次 | 实际结果 | 证据 |
|---|---|---|
| HTTP 边界与状态/竞争 | 定向 race 15 个顶层测试、56 条 PASS；覆盖保存、SQLite 重开、在途回执、取消/关闭、登记/资源/输入权变化、晚到的来源层/注册表/路径变化、两个真实并发候选、幂等与撤销 | [Go 日志](W02-09-claude-installed-settings-candidate/go-focused.log) |
| 固定 CLI + Application/Handler | 45 项通过；真实本地安装、存入 SQLite、关闭后重开、原回执重试及撤销。原始执行为已创建并转人工的空闲查询，保存前保持查询存活；重启明确关闭它 | [结果](W02-09-claude-installed-settings-candidate/raw/candidate/result.json) |
| 原有原生路径 | 设置预览 87、启动准入 210、普通创建 25 项通过；MCP 39、CLI 插件 33 项通过 | [命令与计数](W02-09-claude-installed-settings-candidate/command-results.json) |
| 浏览器 | 新候选 37 项、原预览 30 项通过；使用本轮真实 Handler 响应回放，丢回复、缺失依据和通知为受控界面场景 | [候选检查](W02-09-claude-installed-settings-candidate/candidate-browser.json)、[原预览检查](W02-09-claude-installed-settings-candidate/preview-browser.json) |
| 原始材料审计 | 129 项通过；由同一代理另写脚本，从 wire 回复、文件字节、SQLite 检查点及命令请求独立重算，不只读取 fixture 的通过标记 | [审计](W02-09-claude-installed-settings-candidate/audit.json) |
| 工程检查 | runtime/app/console 三包完整 race、含原生标签的 vet、Web 类型检查/构建、开发二进制及原生测试二进制构建通过 | [race](W02-09-claude-installed-settings-candidate/go-race.log)、[vet](W02-09-claude-installed-settings-candidate/go-vet.log)、[Web](W02-09-claude-installed-settings-candidate/web-build.log) |

原生保存样本共 3 个 metadata 进程：创建前的原文件校验，以及保存时的原文件/派生文件各一次；权限读取只有保存时的两次。幂等重试和撤销没有再启动 metadata 进程。原生 wire 的八个保留摘要、完整启动来源指纹、登记摘要、插件资源树和命令摘要均已重算。

SQLite 关闭检查点确实包含候选，重开后的候选内容与 ID 一致；最终 SQLite 已撤销候选且保留原快照和保存回执。只有原来的一个创建操作、零模型消息。供应商记录只有创建阶段的一个 HEAD；三个原始 SessionStart hook 各执行一次，journal 只在重启时追加原查询的关闭记录，没有新执行查询。数据库原文件留在自有目录，归档保存脱敏投影与哈希：[保存检查点](W02-09-claude-installed-settings-candidate/raw/candidate/saved-store-projection.json)、[撤销后](W02-09-claude-installed-settings-candidate/raw/candidate/store-projection.json)。

界面截图已实际查看：[桌面](W02-09-claude-installed-settings-candidate/candidate-1440.png)、[390 px 窄窗](W02-09-claude-installed-settings-candidate/candidate-390.png)。保存重试和撤销分别使用真实回执结构；没有从浏览器再发送原生模型任务。

## 版本、隔离与复现

固定 Claude CLI **2.1.280**、SDK **0.3.280**。自有 profile、状态目录、Git 项目、本地目录市场和假凭据；受管读取在 `bwrap` 独立 user/PID/network/mount namespace 中，将自有目录映射到 `/etc/claude-code`。Windows 注册表是受控缺席，不是实际 Windows 或企业策略验收。普通 Go 临时目录和 MCP/插件回归根位于仓库外。

本轮工作目录为 `.dev/w0209-installed-settings-candidate-work/`。主要复现入口：

```bash
go test -race ./internal/runtime ./internal/app ./internal/console
go vet -tags nativeclaude,nativecodex ./cmd/... ./internal/... ./web
go test -c -tags nativeclaude -o .dev/w0209-installed-settings-candidate-work/runtime.test ./internal/runtime
TMPDIR="$PWD/.dev/w0209-installed-settings-candidate-work/owned" python3 tests/claude_policy_helper_admission_probe.py --surface candidate --binary "$PWD/.dev/w0209-installed-settings-candidate-work/runtime.test"
python3 tests/claude_mcp_selection_smoke.py --netcap "$PWD/.dev/bin/netcap"
python3 tests/claude_plugin_execution_smoke.py --netcap "$PWD/.dev/bin/netcap"
npm --prefix web run build
go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
```

同一原生 wrapper 的 `application`、`runtime`、`create` surface 对应其余原生检查。浏览器模板为 `tests/claude_plugin_settings_candidate_browser.js` 与既有预览模板；本轮已填充输入的脚本一并归档。审计从原工作目录运行 `audit.py`，需要保留的自有 SQLite 检查点；归档副本用于复核内容，不应直接执行它写入旧证据。

源码、原源码、最终差异与构建哈希见 [manifest](W02-09-claude-installed-settings-candidate/manifest.json)、[diff](W02-09-claude-installed-settings-candidate/source.diff)、[附件哈希](W02-09-claude-installed-settings-candidate/artifact-hashes.json)。所有 bridge、依赖、需求文件和旧 `bin/netcap` 均未改变；没有提交、推送或修改用户配置。10,597 份旧 W 证据及 31 份旧 goals 证据逐文件保持；自有进程、私有 settings 文件及浏览器已收尾。

## 失败记录与限制

首次 HTTP 测试遗漏 JSON 类型头，得到 415；修正后在基线 Handler 上实际复现五个 404，再由新入口通过。首次原生测试误把既有撤销接口的 202 当作 200，并要求主动关闭后的 journal 字节不变；按原命令契约和唯一关闭记录修正。浏览器夹具的 `URL` 构造器不可用及撤销后未发通知也已修正；原日志与首轮截图保留，见 [问题记录](W02-09-claude-installed-settings-candidate/inspection-notes.json)。完整 evidence 清点补上了 `rg` 忽略的 24 个既有文件；没有改写旧证据。

保存完成不等于插件生效。本片没有实现完整权限执行准入、受管/remote/helper 真实性与新鲜度、动态策略、文件读取后的全部竞态、原 ID 应用/恢复事务或真实加载目录绑定。候选不持有可直接应用的派生文件；未来应用必须重新核对来源并通过完整准入。四配置联合、真实方法/Gate、独立前台、共享后台与输入独占、Windows 第二环境及全部首版硬要求继续保留。下一片沿已保存候选完成启动准入与实际加载来源核对，再接原 ID 应用事务。
