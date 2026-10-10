# W01.02 · 已加载权限采样与执行占用

日期：2026-10-01。承接[权限模式恢复](W01-02-permission.md)，修复已加载线程仅凭原生 `idle` 就调用 `thread/resume` 的顺序问题，以及异步回答新开轮次缺少完整准入的问题。工作台现在先核对执行和请求，再采样权限，采样后重新只读核对。W01.02 保持 in_progress，累计 8/40 done，Goal active。

## 失败与修复

[首轮红灯](W01-02-resume-state-runtime-red.txt)通过生产 `Observe` / `Execute(send)` 接口复现：12 类已有占用或查询失败仍触发 resume；连接身份变化、采样后再次卸载的两个场景仍发送了新输入。其余正向/变化对照在旧实现中部分已通过，不把全部测试都称为原始失败。

[Runtime](../../internal/runtime/codex.go)现在先读取历史、Goal、队列、后台终端、子会话和未决请求；已加载且完整空闲、MCP 选择核对一致时，才调用无覆盖的 resume。未加载线程仍沿用上一片的原权限冷恢复路径，没有把完整已加载门槛错误套到尚无运行句柄的线程。

[权限采样](../../internal/runtime/codex_permission.go)在 resume 后以 `NoAutoResume=true` 再观察一次，使用新的状态、历史、请求和执行范围；期间不会递归恢复第二次。连接变化、查询失败、新增占用或线程再次卸载都会使采样证据未验证，阻止新输入。新创建依据还须在后读中保持有效；原生元数据变化、历史、活动、执行占用和未决请求均使它失效，不能把已消费的创建回执重新合并为 verified。

[异步回答红灯](W01-02-resume-state-async-red.txt)是在首轮观察修复后、回答路径修复前取得：原回答路径仍可在 Goal/队列/终端/子会话、其他问题或未知查询期间直接发新轮次。[回答实现](../../internal/runtime/codex_async.go)现在仅对本次显式回答的单一 `message_input` 问题放宽等待状态；采样前后仍需原问题有效、无其他占用且权限已核对。原问题属于当前运行轮次时保留带 `expectedTurnId` 的 `turn/steer`，不会借回答另起第二轮。

[迟到请求红灯](W01-02-resume-state-late-red.txt)进一步发现执行查询期间的请求可能未被投影，以及采样中的元数据变化未消费创建依据。现在在最后一次执行/MCP RPC 后收集请求与解决通知，再决定采样和发送。[活动依据红灯](W01-02-resume-state-activity-red.txt)补齐了 Goal/请求占用后创建缓存未消费的问题。以上两份是迭代过程的失败记录，最终测试已通过。

## 真实 stdio：通过与失败分开

固定可执行文件为 `/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex`，报告版本 `codex-cli 0.158.0`。使用独立 `/tmp` HOME、CODEX_HOME 和项目，合成凭据、合成历史、供应商配置指向不可用的 `http://127.0.0.1:9/v1`。未使用真实供应商或共享后台。

| 实验 | 实际结果 | 能证明的范围 |
|---|---|---|
| [暂停 Goal、无队列输入](W01-02-resume-state-native-goal.json) | 21 项通过，连续 3 次无覆盖 resume | Goal 全字段、空队列、空轮次、空后台终端、原 ID、idle 状态及采样权限字段保持；未观察到启动轮次 |
| [加入队列的负对照](W01-02-resume-state-queue-negative.json) | 前置断言失败，退出 1 | `thread/queue/add` 后队列已自动出队，线程 active，出现一个 inProgress 轮次和 `turn/started`；Goal 仍 paused。实验在任何 resume 之前中止 |

**负对照确实启动了一个原生轮次，不能称为零输入或零轮次实验。** 它说明暂停原生 Goal 不阻止队列用户输入自动执行，不能证明 resume 对非空队列的影响。正对照仅覆盖暂停 Goal、空队列、无真实工具的样本，不能外推到 active Goal、真实在途工具、非空队列或生产 Unix proxy。

这两次协议结果在实现本轮修复之前已取得，本片复用并归档，没有无故重跑。它们验证固定 CLI 的行为边界，不冒充新生产 Adapter 的真实端到端验收。[负对照原始脚本](W01-02-resume-state-queue-negative.py)保留了当时被推翻的“队列保持未启动”假设；当前[可重跑脚本](../../tests/codex_resume_state_probe.py)明确区分默认正对照与 `--with-queue` 负对照。

## 验证

| 层次 | 结果 |
|---|---|
| 新增定向场景 | [6 个顶层、39 个子测试](W01-02-resume-state-targeted.txt)通过，另一个无子测试的顶层为暂停 Goal 正例；覆盖占用/未知查询、后读变化、异步回答、迟到请求、创建依据失效和暂停 Goal |
| Runtime 最终 race | [56 个顶层、203 个子测试](W01-02-resume-state-runtime-race.txt)通过，明确排除 `^TestOpenCode`；沿用前片已确认的监听权限限制，没有宣称整仓全测试通过 |
| 应用/HTTP 边界回归 | [app/console race](W01-02-resume-state-app-console-race.txt)通过，使用已有受控接缝；未启动生产 HTTP 监听 |
| 静态与构建 | [全包 vet](W01-02-resume-state-vet.txt)、[开发构建](W01-02-resume-state-build.txt)通过，包含 Web 类型检查/构建；Go 1.26.2，Node 24.13.0 |
| 未执行 | 新生产代码的真实 stdio Adapter/Unix proxy 集成、active Goal/真实工具占用恢复、真实供应商、浏览器、独立前台、Windows |

构建仅写 `.dev/bin/netcap`，Web 资源仍为 `index-DyX1tA6S.js` / `index-orGpbq3d.css`。页面未改，未把历史浏览器通过计入本片。最终移除了已由采样后检查接管的旧创建依据分支，随后重跑 Runtime race、vet 和构建。

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -skip '^TestOpenCode' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 tests/codex_resume_state_probe.py --output .dev/w0102-resume-state/recheck-goal.json
# 负对照会向隔离原生线程加入输入，可启动轮次；不应改用真实供应商。
python3 tests/codex_resume_state_probe.py --with-queue --output .dev/w0102-resume-state/recheck-queue.json
```

[差异](W01-02-resume-state-diff.patch)包括四处 Runtime 修改、新测试、新协议脚本和使用说明；[manifest](W01-02-resume-state-manifest.json)记录归档时点的源码、构建与证据哈希，不是提交或发布。[清理核对](W01-02-resume-state-cleanup.json)确认 506 个旧证据未改，两次自有原生进程都已退出；它们由 fixture 终止并等待，exit -9 不代表正常原生退出验收。未提交、发布或停止共享后台。

## 剩余范围

本片阻止了可观测占用期间的已加载权限采样和新回答轮次；多次 RPC 观察仍有时间窗口，不构成跨客户端原子输入锁。冷恢复能否激活已有原生 Goal、完整原权限规则与供应商实际配置、原执行器接入和输入独占还需分别验证。

继续保留 W01.02 原执行器/配置来源展示与控制、六入口、W02.06 EOF/认证、W03.02 用户反馈和 Windows 的原有缺口。只读来源观察始终 NoAutoResume，既未开放外部控制，也未据此关闭 W01.02。
