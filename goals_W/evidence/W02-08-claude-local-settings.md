# W02.08 · Claude 项目与主仓库本地设置身份检查

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 修复结果

Claude 的创建、发送、审批回复、准备及应用改配，现在会在建立 bridge 或提交输入之前检查实际 cwd 的项目设置、本地设置，以及支持的 Git 布局下主仓库的本地设置。文件包含供应商、凭据或 HOME/XDG/Claude 状态目录覆盖时返回带具体路径的 `NotSent`；`apiKeyHelper` 同样拒绝。不会因下层同名值被其他设置覆盖就放过身份冲突，沿用原有 profile/custom 设置的约束。

原来的 `ValidateClaudeSettings(instance)` 只检查登记 profile 与 custom 文件。真实 Runtime 复现中，三类项目/本地来源的 `ANTHROPIC_BASE_URL` 均未被拦住，创建成功且测试启动 hook 已执行。修复后这三例连 bridge 都未创建；正常设置仍可启动。运行中增加覆盖时，Application 的发送操作持久记录为 `not_sent`，原生 ID、Scope 和执行快照不变；修复文件不会自动重放失败操作。

## 来源与读取边界

新增 `ValidateClaudeExecutionSettings(ctx, scope)`，复用 profile/custom 的身份字段验证，并按保存 cwd 和后端原始环境解析本地来源。正常仓库取主仓库 `.git` 对应的 local 文件；linked worktree 另核对工作树顶层、common Git dir 与实际 Git dir 的对应关系，不把 worktree 中间目录自动当作已加载来源。沿用固定 CLI 的 POSIX HOME/目录所有者边界；HOME 根和普通目录的行为有本片原生对照。

使用只读 Git `rev-parse`，3 秒期限、stdout/stderr 各 16 KiB 上限，诊断语言固定为 C。只有明确的“非仓库”诊断、退出 128 且无 stdout 才按 cwd 来源继续；一般失败、不可用、超限或未知布局均拒绝，不转发 Git 原始错误内容。裸仓库、外置 Git 元数据等未核对布局不作为已支持的主仓库来源放行。本片 Git 版本为 **2.43.0**。

profile/custom/项目/local 文件均限定为 2 MiB 内的普通 JSON 对象，支持 BOM 和可读取的文件链接；nonblocking 打开防止 FIFO 阻塞。读取前后比较文件身份、大小、时间和模式，无效对象、非字符串 env 值、损坏链接、超限及读取中变化返回具体文件错误，不返回解析输入或凭据值。

**这是逐次身份字段检查，不是完整来源绑定或配置冻结。** 未重写原配置、权限或快照，未改变 native 的设置合并。读取与后续原生使用之间不是原子事务；受管/helper/服务端策略、动态热更新、设置中的 Git 环境覆盖如何影响定位，以及其他特殊 Git/所有权布局仍待完整核对。它不能用于声称已安装插件控制或全部权限合并已完成。

## 实际验证

| 层次 | 结果 |
|---|---|
| 公共 Runtime 原始失败 | [runtime-red.log](W02-08-claude-local-settings/runtime-red.log)：项目、cwd local、primary local 三来源 × create/send/respond/prepare/apply 五类入口未按来源拒绝，已尝试准备 bridge。 |
| 真实原始失败 | [native-red.json](W02-08-claude-local-settings/native-red.json)：三例均返回新原生 ID并写出启动标记；预期拒绝、无启动、无 bridge 均失败，仅 Close 三项通过，3/12。没有模型输入。 |
| 文件与 Git 边界 | 最终 [topology-final.log](W02-08-claude-local-settings/topology-final.log)通过：16 类文件边界、五种位置及 HOME 来源路径、Git 失败、io.Copy 输出上限、bare/外置 `.git` 拒绝，以及上述三来源五入口。 |
| 真实 Runtime | [最终 44 项](W02-08-claude-local-settings/native-final.json)通过：三类覆盖在启动前拒绝；正常设置、忽略中间 worktree、普通目录、HOME 边界四例保留启动；运行中新覆盖阻止输入，修复后仍为原 ID；来源路径、零模型 input、Close 均核对。 |
| Application/SQLite | [最终 12 项](W02-08-claude-local-settings/application-final.json)通过：同 ID 会话增加 root local 凭据覆盖后，发送持久记录 not_sent、错误指向原文件且不含凭据值，Scope/快照不变；修复后人工接管正常，无模型消息/输入。 |
| 独立审计 | [28 项](W02-08-claude-local-settings/audit.json)通过：从实际标记文件、bridge/journal 状态、原生 hook 终态、只读 SQLite 和调用方结果核对，不把测试布尔值当作独立依据。 |
| 回归 | 自然 Skill 与插件原生选择各 **9 项**通过。首次回归正则中插件测试名称不匹配，只有 Skill 实际运行；之后以精确名称单独运行插件测试，两个日志分别保留。 |
| 工程检查 | Claude Runtime 相关 race、app/console 全包 race、最终带 nativeclaude 标签的定向 Runtime/App race 与 vet、gofmt、开发构建通过。Web 源码与构建内容不变，无浏览器重测；实际拒绝数据已导出，未称为浏览器验收。 |

中间修复没有隐藏：`boundaries.log` / `native-green.log` 记录了普通目录被无效 `.git` 标记误拒的问题；改为核对 Git 的明确非仓库结果后通过。`layout-red.log` 记录了外置元数据也叫 `.git` 时被误接受的问题；补齐 Git dir/worktree 对应检查后通过。输出缓冲使用具名字段，避免嵌入 bytes.Buffer 的 ReaderFrom 绕过 io.Copy 限额；定向测试覆盖该调用方式。

最终原始 Runtime 目录 `/tmp/netcap-w0208-local-admission-2727102109`，Application 目录 `/tmp/netcap-w0208-local-settings-app-3325718867`；原始红轮 `/tmp/netcap-w0208-local-admission-2389436742`。其他执行位置见 [目录记录](W02-08-claude-local-settings/original-directories.json)及各日志。CLI **2.1.280**、SDK **0.3.280**，隔离 HOME/XDG/profile、临时仓库/worktree、测试凭据与不可用本地供应商。自有测试空提交仅用于建立 worktree；没有项目提交、推送或发布。

所有命令和自有查询已结束。红轮三次、绿轮四次测试启动 hook 有记录；禁止分支没有启动 hook，Application 只有 initialized/closed。模型输入和结果均为零，没有新宿主探针、监听器或共享后台操作。原有五项 WSL 请求仍待结果。

## 复跑与归档

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaudeLocal' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/runtime ./internal/app \
  -run '^TestClaude(Local|NativeLocal|NativeApplicationLocal)' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^TestClaude' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 goals_W/evidence/W02-08-claude-local-settings/audit.py
```

[差异](W02-08-claude-local-settings/source.diff)、[清单](W02-08-claude-local-settings/manifest.json)、[文件摘要](W02-08-claude-local-settings/filesystem-summary.json)、[原生日志摘要](W02-08-claude-local-settings/journal-summary.json)及 [SQLite 摘要](W02-08-claude-local-settings/sqlite-summary.json)保留本片依据。未复制认证文件、完整 SQLite 或原生设置正文。

- 开发二进制：`3894564bf3bf0e100dea56c95a1a9f7b4673e2941be5d85180099a263f4d6617`。
- 旧 bin 未改：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。
- **3396** 个既有 W 证据逐文件哈希不变；Web 内容不变。

继续完整启动前来源/策略检查、源文件身份与安装插件事务；上一片 SDK/native 来源不一致仍然存在，本片没有修改 SDK。方法任务/Gate、丢确认恢复、输入独占、宿主/proxy 与其他 hook 硬要求继续保留，不据本片关闭 W02.07/W02.08。
