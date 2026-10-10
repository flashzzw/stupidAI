# W01.02 · 重启后的创建来源核对

日期：2026-10-01。承接[真实 Runtime 协议片](W01-02-runtime-native.md)发现的 metadata 来源缺口，修复已持久化的原线程在 CLI 重启后无法关联创建操作的问题。原操作有 NativeID 回执和完全丢失回执两条路径均有真实 Runtime stdio 结果；W01.02 保持 in_progress，累计 8/40 done，Goal active。

## 原始失败与原因

[真实红灯](W01-02-create-recovery-native-red.txt)在两种权限下均复现：创建并持久化合成历史后重启 CLI，`thread/read.threadSource` 为 null；生产 Reconcile 即使拿到原 NativeID 也返回 unknown，无回执查找同样找不到原线程。原始记录为 [workspace](W01-02-create-recovery-red-workspace.json) / [read_only](W01-02-create-recovery-red-read_only.json)。这不是线程被删除或原 ID 改变。

读取同一自有测试文件的首行发现，`session_meta.payload.thread_source` 仍保留 `netcap-console:synthetic-create`，ID、session_id、cwd、model_provider、cli_version 也匹配。上一片原始 resume 回执同样保留这个标记。缺失发生在 read/list 的 metadata 投影，不能据此认定原生持久标记已经丢失。

另一个原因是查找过滤遗漏了实际 `vscode` 来源；固定 schema 允许该 sourceKinds 值，真实线程 metadata 确实返回它。`threadSource` 是原生定义的客户端 analytics 分类标记，不被提升成认证凭据。

## 实现

[创建来源模块](../../internal/runtime/codex_creation_source.go)保留现有非空 metadata 标记的精确匹配。只有标记缺失、候选目录/模型/供应商符合原 Intent 时，才只读查看该候选原生日志头：

- 文件必须位于绑定 profile 内，目录别名可解析；`os.OpenRoot` 约束实际打开过程，随后重核文件与目录身份，拒绝越界或替换。
- 只读取首行，最多 4 MiB，必须为完整的 session_meta JSON；非阻塞打开后拒绝 FIFO、目录等非普通文件。
- envelope 和 payload 拒绝重复字段；核对 ID、可选 session_id、cwd、model_provider 与固定 `cli_version=0.158.0`。仅提取创建标记，不输出指令正文或其他日志内容。
- 非空 metadata 标记若冲突，不用文件内容覆盖它。格式有效但没有标记的普通线程不能匹配本操作，也不会挡住另一个有准确标记的候选；文件不可读、格式未知等保留查询不完整。

[Reconcile / 查找](../../internal/runtime/codex_recovery.go)增加 vscode 来源，扫描已加载与磁盘候选后确认唯一匹配：同一 NativeID 去重，不同 ID 带同一原操作标记则拒绝，不因先命中已加载线程而跳过其他候选。候选通过来源核对后才走已有原权限恢复和执行观察。来源不匹配或不完整的候选不触发 resume、输入或新建。

读取创建候选被逻辑 RPC 拒绝时，复用现有 readFailed 行为，保留健康共享连接及 sibling 审批；传输故障仍会使旧请求待核对。[单行负对照](W01-02-create-recovery-read-rejection-red.txt)通过 Go overlay 恢复旧 `c.failed` 分支，确认它会错误关闭 sibling 连接。该负对照不替换实际生产文件。

## 验证

| 层次 | 结果与范围 |
|---|---|
| 受控 Runtime | [初始红灯](W01-02-create-recovery-runtime-red.txt)复现已知回执与自动查找无法恢复；[查找迭代红灯](W01-02-create-recovery-lookup-red.txt)发现重复投影和无标记线程影响查找，均已修复。新增最终 3 个顶层测试、25 个子测试覆盖 7 种查找、18 种拒绝边界及独立 sibling 请求保护 |
| 全 Runtime race | [59 个顶层、228 个子测试](W01-02-create-recovery-runtime-race.txt)通过；仍明确排除 `^TestOpenCode` 监听测试，不称整仓全测试通过 |
| 应用与 HTTP 边界 | [app/console race](W01-02-create-recovery-app-console-race.txt)通过，包括已有 unknown/人工候选关联/不重发的持久状态接缝；没有新增真实 HTTP 监听验收 |
| 真实 Runtime + stdio | [最终运行](W01-02-create-recovery-native-final.txt)两种权限各 21 项，共 **42 项检查**通过。原始 [workspace](W01-02-create-recovery-workspace.json) / [read_only](W01-02-create-recovery-read_only.json)包含文件头选定字段、RPC 和视图。先从完全无回执、原线程 notLoaded 的状态执行查找与恢复，再检查有回执路径；没有用先恢复过的线程冒充无回执冷查找 |
| 静态/构建 | [带 nativecodex 标签的全包 vet](W01-02-create-recovery-vet.txt)、[开发构建](W01-02-create-recovery-build.txt)通过；Go 1.26.2，固定 CLI 0.158.0。Web 类型检查/构建通过，资源仍为 index-DyX1tA6S.js / index-orGpbq3d.css |

42 项含上一片 38 项回归及每种权限的两个新创建关联检查。仍实际执行合成用户轮次和 active Goal 轮次；原输入中断、拒绝重复输入及权限保持的范围沿用上一片说明，不宣称零输入实验或真实模型/工具验收。

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -skip '^TestOpenCode' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
NETCAP_NATIVE_CODEX_EVIDENCE="$PWD/.dev/w0102-create-recovery/recheck" \
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
make codex-runtime-native \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

## 隔离与剩余条件

真实实验使用独立 `/tmp` HOME、CODEX_HOME、XDG 与项目、合成 key 和不可用 `127.0.0.1:9` 供应商；测试生成合成历史使原生线程持久化，没有修改任何原生日志以伪造创建标记。文件头内容仅按固定字段投影到证据，不归档指令正文。来源格式事实来自固定 native schema、原生文件与实际 RPC；本片没有新获取的官方网页。

[清理记录](W01-02-create-recovery-cleanup.json)核对 541 个旧证据未改、本片 12 个自有协议进程退出；最终四个进程 exit 0。只更新 `.dev/bin/netcap`，没有停止共享后台、提交或发布。[差异](W01-02-create-recovery-diff.patch)与 [manifest](W01-02-create-recovery-manifest.json)记录归档时点源码、构建和证据哈希。

**本片只解决已有持久化原生记录的创建关联。** 创建后从未生成 rollout 就退出的线程、日志已删除/损坏/超限、其他格式版本或无法唯一匹配时，继续保留 unknown；不会重放 create 或根据目录/时间猜测归属。读取来源本身是只读，随后恢复原已确认线程仍可能涉及原生 Goal 等行为，既有权限/占用门槛不能省略。

生产 Unix proxy/peer/凭据链、原执行器和环境来源展示、真实在途工具、外部输入权、六入口及完整权限仍需补齐。工作台持久状态与真实 Runtime 组合的丢回执端到端试验也未由本片 stdio 接缝覆盖。W02.06 EOF/认证、W03.02 用户反馈与 Windows 条件保留，整条目标未完成。
