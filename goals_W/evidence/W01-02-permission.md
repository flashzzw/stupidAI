# W01.02 · 工作台 Codex 冷恢复的权限模式

日期：2026-10-01。承接[原执行观察片](W01-02-observe.md)指出的生产恢复缺口。现在工作台 Codex 原 ID 冷恢复显式携带已保存的沙箱模式和审批策略，并核对原生回执；缺失或不匹配时不发送新输入。**只核验模式与审批策略，完整权限继承、外部原生来源的控制接入和输入独占仍未完成。** W01.02 in_progress，累计 8/40 done，Goal active。

## 原始失败与真实对照

[生产 Runtime 红灯](W01-02-permission-runtime-red.txt)通过实际 `Execute(send)` 入口复现：已有 `Permission: workspace` 的 Intent，在原线程 notLoaded 时恢复并继续发送，但恢复 RPC 未携带 sandbox/approvalPolicy。受控后台使用只读、never 回退，旧代码仍发出新轮次。修复后[同一复现通过](W01-02-permission-runtime-green.txt)。这里的审批回退是 fixture 行为，不能当作已证明的原生审批漂移。

[真实负对照](W01-02-permission-native-before.json)只在恢复 RPC 中去掉这两个字段，保持其余隔离准备与固定 CLI 相同，结果如下：

| 原线程模式 | profile 默认模式 | 省略恢复权限字段 | 显式携带原字段 |
|---|---|---|---|
| workspaceWrite | read-only | 恢复为 readOnly，原模式断言失败 | 原模式及返回字段保留 |
| readOnly | workspace-write | 恢复为 workspaceWrite，原模式断言失败 | 原模式及返回字段保留 |

两项负对照都保留原 ID，却未保留原沙箱模式，整体退出 1。**只读原线程也可能被恢复成工作区写入。** 本次真实样本的审批都仍为 `on-request`；显式固定审批和验证缺失回执属于加固，不把 fixture 的审批回退说成真实 CLI 已复现问题。

[修复参数的真实对照](W01-02-permission-native.json)两种模式各 9 项、合计 18 项检查通过。workspace 样本包含额外可写目录、关闭网络、排除 TMPDIR 与 `/tmp` 默认可写范围；在配置未变时，恢复后的完整采样字段与创建回执一致。已加载后的无覆盖采样也保持这些字段。

## 生产实现

[应用](../../internal/app/application.go)在普通轮询与显式恢复控制时，从保存的 Session / Intent 传递权限；[Runtime Reconcile](../../internal/runtime/codex_recovery.go)同样取原 Intent。临时 `Scope.RecoveryPermission` 不进入 JSON 或持久身份，不创建另一份权限权威；Session.Permission 和不可变执行快照保留原值。

[权限模块](../../internal/runtime/codex_permission.go)集中处理两类情况：

- 原线程未加载：使用原 ID、原模型/供应商覆盖以及原 `read_only` / `workspace`；普通会话为 `on-request`，隔离 probe 保留 `never`。没有有效原权限时拒绝恢复。
- 原线程已加载且原生状态为空闲：通过只含 threadId / excludeTurns 的 resume 采样当前策略，不覆盖当前权限。回执身份/模型/运行状态须匹配，沙箱 type 与审批策略须符合原选择；缺失、未知或不一致即停止。重复查询与控制台重建不会用覆盖参数掩盖已加载线程的策略不一致。

新建也核对原生 start 回执。一般发送和异步问题回复都传递原权限；发送准入要求权限证据与原有空闲/执行范围证据一起成立。外部原生只读观察和已有只读预览继续使用 NoAutoResume，不进入此采样/恢复路径，也不套用新建权限或路由。

`View.permission_evidence` 是新增的观察字段，保存原生 sandbox / approvalPolicy、来源、时间、expected_mode、verified 和明确范围 `sandbox_mode_and_approval_policy`。验证器只判断原有两种模式与审批策略，不逐项证明 raw sandbox 中所有继承字段。不匹配时保留 Session 原身份、权限与快照，应用保持暂停；不会另建会话或清空历史。

## 新线程与证据失效

真实 CLI 的新线程可显示 idle，但尚无 rollout，resume 返回精确的 `no rollout found for thread id <ID>`。新原生对照分别核实该错误与初始 metadata。

仅对本控制台成功创建、同一协议连接、原 ID/项目/模型/供应商匹配且尚无输入的线程，允许使用已核对的 start 回执作为首次输入前的依据。证据保留原 `codex.thread/start` 来源和原时间，不伪装成本次 resume 读取。首次自有输入、看到外部活动/历史、成功恢复为可读 rollout、连接丢失或变化、关闭 Adapter 都会使它失效。错误类型或原身份不匹配时不走此路径；控制台重启不会凭历史记录制造一份新创建回执。

[失效红灯](W01-02-permission-witness-red.txt)发现初版只消费自有输入，外部活动/历史后仍可能复用旧 start 依据；已修复并纳入最终 Runtime 回归。

[权限证据红灯](W01-02-permission-evidence-red.txt)发现应用核对失败后可能留着旧 `verified:true`。现在[失败证据处理](../../internal/app/recovery_permission.go)接收新失败采样，或将保留的旧字段明确标为未验证。轮询失败与显式交接失败都覆盖，旧时间/字段仅供追溯。

## 实际验证

| 层次 | 结果和范围 |
|---|---|
| Runtime | [最终 race](W01-02-permission-runtime-race.txt)50 个顶层、164 个子测试通过，明确排除既有 `^TestOpenCode` 监听测试。新测试包含原 ID 冷恢复后继续发送、12 个权限/状态场景、8 个新线程依据边界及缺少原权限拒绝；顶层/子测试不是独立场景数之和 |
| 应用与 HTTP 回归 | [最终 app/console race](W01-02-permission-app-console-race.txt)通过；[最终定向](W01-02-permission-app-targeted.txt)15 个顶层、25 个子测试通过，含两种权限在普通轮询、重启、显式交接与失败保持暂停中的传递、快照/身份保持及旧证据失效。使用受控 Runtime，无真实监听 |
| 原生 CLI | 固定 0.158.0 stdio 正向 18 项通过，负向两个模式均失败且保留失败结果；没有真实模型轮次或工具执行 |
| 静态/构建 | [全包 vet](W01-02-permission-vet.txt)、[开发构建](W01-02-permission-build.txt)及最后说明性注释后的 [Go 构建](W01-02-permission-go-build-final.txt)通过。Web 类型检查/构建通过；资源保持 `index-DyX1tA6S.js` / `index-orGpbq3d.css` |
| 未执行 | 生产 Unix proxy/peer 与真实后台完整集成、真实供应商/模型/工具、浏览器重跑、共享后台/独立前台、Windows。页面结构未改，未复用旧浏览器计数作为本片新通过 |

应用测试初轮在创建回执刚成为 idle 时即检查普通轮询计数，早于独立轮询发生；修正为同时等待真实轮询记录，再验证原权限。旧路由/异步问题 fixture 补齐有效权限与真实回执字段；没有降低生产校验来保留旧空响应。

## 隔离与重跑

原生实验全部使用独立 `/tmp` HOME/profile/项目、合成凭据与 `127.0.0.1:9` 供应商配置。只写标明合成内容的历史，没有 turn/start、模型供应商调用或共享后台操作。自有服务器未及时响应停止信号时由 fixture 终止并等待退出，记录 -9；不当作正常原生退出验收。

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -skip '^TestOpenCode' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app -run 'TestSavedPermission|TestResume|TestInterruptedHandoff|TestNativeBinding|TestBoundNativeObservation' -count=1 -v
python3 tests/codex_permission_resume_probe.py --output .dev/w0102-permission/native-final.json
python3 goals_W/evidence/W01-02-permission-native-before.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

[负对照脚本](W01-02-permission-native-before.py)会返回 1，并向 `.dev/w0102-permission/native-before.json` 写入不保留原模式的结果；它只用于本次回归证明。协议事实来自已核对的固定本地 schema 与实际原生 RPC；没有新获取的官方网页，也没有更换固定 CLI 版本。

[差异](W01-02-permission-diff.patch)覆盖本片开始时捕获的既有文件；新增模块、另两处异步回复/交接处理和测试以源码及 [manifest](W01-02-permission-manifest.json)为准。哈希描述此次归档时点，不是提交或发布。Go 1.26.2、Node 24.13.0，依赖未改。

[清理记录](W01-02-permission-cleanup.json)核对 486 个旧证据未变、所有本片自有原生进程退出。没有使用浏览器，没有提交、真实凭据写入或对外发布。

## 后续与限制

正常模式/审批恢复的本片问题已修复；没有完成 W01.02 或 W02.09 的全部要求。外部来源没有已确认的原权限选择，不能用本工作台默认值代替；上一片外部冷恢复无覆盖的失败证据仍有效。

完整原生权限配置、动态规则/额外路径、原供应商实际 endpoint/认证、在途工具/Goal 的全部接续副作用、独占输入与终端草稿仍需分别核验。创建回执只是同连接的初始观察，不能提供对另一个原生客户端配置改写的原子保证。继续推进原执行器接入与原配置来源证据，保留六入口、W02.06 EOF/认证、用户反馈和 Windows 条件。
