# W01.02 · 生产 Runtime 的真实 stdio 协议验证

日期：2026-10-01。将[上一片采样与占用实现](W01-02-resume-state.md)接到固定 CLI 的真实 stdio 回执，补齐创建、已加载采样、原 ID 冷恢复、在途冲突及显式中断的 Runtime 层证据。**生产 Runtime 使用测试专用 stdio 传输；Unix proxy、peer 身份与凭据认证链未覆盖。** W01.02 仍 in_progress，累计 8/40 done，Goal active。

## 可重跑入口与实际范围

新增 [nativecodex 测试](../../internal/runtime/codex_native_stdio_test.go)和 [Makefile 目标](../../Makefile)。测试文件只在 `linux && nativecodex` 标签下编译，常规 `go test` 不会自动调用真实 CLI。可执行文件须由绝对路径指定且报告 `codex-cli 0.158.0`。

测试把真实 stdio 连接注入 Runtime 已有 `codexConnection` 接缝，随后调用生产 `Execute(create/send/interrupt)`、`Observe` 和 `Reconcile`。测试传输仅负责 JSON-RPC 收发、事件与请求集合；没有用 fixture 回答 native RPC，没有向生产代码添加绕过 socket 核对的入口。

本片没有修改生产 Runtime、应用或页面代码。所用路由为独立 profile 的 `fixture` 供应商，显式标注 `profile:fixture`，不声称验证了 `env:` 凭据来源或真实认证。`initialize.codexHome` 必须匹配本测试临时目录；这项 stdio 检查不替代生产 Unix peer 核对。

```bash
NETCAP_NATIVE_CODEX_EVIDENCE="$PWD/.dev/w0102-runtime-native/recheck" \
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
make codex-runtime-native \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex

GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/runtime
```

## 最终验证

[最终 race 输出](W01-02-runtime-native-final.txt)通过。两种权限各 19 项、合计 **38 项检查**，原始回执分别在 [workspace](W01-02-runtime-native-workspace.json) 与 [read_only](W01-02-runtime-native-read_only.json)。测试对原生 sandbox type 和 approvalPolicy 有独立预期，不只信任生产 `verified` 字段。

| 路径 | 实际依据 |
|---|---|
| 创建与核对 | 生产 create 返回原生 ID，Reconcile 完成创建；无 rollout 时使用同连接 start 回执，原生模式分别为 workspaceWrite / readOnly，审批为 on-request |
| 已加载观察 | 注入明确合成历史并设置 paused Goal 后，生产 Observe 完成无覆盖采样和后读；原 ID、权限、完整 Goal 字段保持 |
| 冷恢复 | 退出自有 CLI 后用同一 profile 启动新进程，先确认原 ID 为 notLoaded；profile 默认沙箱故意与原权限相反，生产 Observe 仍恢复原模式与审批，样本 sandbox 字段和 paused Goal 保持 |
| 原 ID 发送与关联 | 实际向不可用本地供应商发送合成输入；生产 Reconcile 等待原操作 ID 的用户消息出现后关联到原轮次，没有重放输入 |
| 在途冲突 | 第二次 send 被拒绝；该次观察与准入期间没有 thread/resume 或 turn/start |
| 显式中断 | 生产 interrupt 核对自有 input 标记后中断原轮次，随后原 send 被 Reconcile 为 interrupted；此阶段仍只有那个原轮次，Goal 保持 paused |
| active Goal | 在中断验收之后，测试显式激活自己的合成 Goal；CLI 自动开启新的 Goal 轮次。生产 Observe 显示 native_goal 占用，冲突输入被拒，期间没有 resume 或 turn/start；最后记录 Goal 暂停并关闭自有进程 |

**每个模式在 active Goal 阶段会额外启动一个原生轮次。** “只有原轮次”的断言限定于显式输入/中断阶段，不声称整个实验只有一次 native turn。真实 active Goal 样本的线程状态也为 active，未证明 `idle + active Goal` 的瞬时间隙；上一片对应受控 RPC 断言仍是那一边界的证据。

[带 nativecodex 标签的 vet](W01-02-runtime-native-vet.txt)通过。没有改生产源码或界面，因此未重跑 app/console、普通全 Runtime、浏览器或开发构建；`.dev/bin/netcap` 和 Web 资源哈希与上一片一致。Go 1.26.2，固定 CLI 0.158.0，未更换依赖。

## 两项被真实回执纠正的测试假设

第一版预期不可用端点会快速产生 failed 轮次，但[初始运行](W01-02-runtime-native-initial.txt)两个模式各等待 60 秒后超时失败。原始完整记录压缩保存为 [workspace](W01-02-runtime-native-initial-workspace.json.gz) / [read_only](W01-02-runtime-native-initial-read_only.json.gz)，[摘要](W01-02-runtime-native-initial-summary.json)保留最后状态、错误事件和失败检查。`request_max_retries=0`、`stream_max_retries=0` 下仍观察到 `Reconnecting... waiting for network`、`willRetry:true` 和同一 inProgress 轮次。**这不是模型失败已完成，也不是权限恢复失败。** 最终测试改为验证真实在途状态与显式中断，不把超时翻成成功。

第二版在 turn/start 回执后立即要求关联成功，[时序检查](W01-02-runtime-native-timing.txt)失败。当时已经存在 inProgress 轮次，但其 userMessage 尚未进入历史；Runtime 保持运行占用且关联结果为 unknown。最终测试只读等待该原输入标记出现，不发送补偿输入，也没有放宽生产关联条件。保留 [workspace](W01-02-runtime-native-timing-workspace.json) / [read_only](W01-02-runtime-native-timing-read_only.json) 原始记录；它们是测试时序前提失败，不被描述为生产输入重放 bug。

这些失败推动了验证方法的修正，没有修改产品目标或把未满足要求改成通过。[初始测试源码](W01-02-runtime-native-initial-test.go.txt)和[时序测试源码](W01-02-runtime-native-timing-test.go.txt)记录当时具体断言；最终源码以当前测试和 manifest 为准。

## 隔离、产物与剩余要求

所有实验均在测试自建 `/tmp` HOME、CODEX_HOME、XDG 目录和项目中运行，环境不继承用户凭据、代理或配置。供应商固定为不可用 `http://127.0.0.1:9/v1`，合成 key 仅用于本地测试。存在真实原生轮次和本地失败连接尝试，没有真实供应商成功调用、模型输出或工具执行。直接 `thread/inject_items` / `thread/goal/set` 仅建立测试自有数据，不是生产外部控制功能。

测试通过自己持有的 `exec.Cmd` 进程句柄停止并等待退出，记录每份 CLI 的退出结果；最终四个进程 exit 0，不将初始超时进程的退出混作正常关闭验收。[清理记录](W01-02-runtime-native-cleanup.json)核对本片全部实验的自有进程退出、524 个旧证据未变、生产源码与既有构建保持。[差异](W01-02-runtime-native-diff.patch)及 [manifest](W01-02-runtime-native-manifest.json)记录归档时点，不是提交或发布。

原始回执还显示：本样本的 `threadSource` 创建标记在重启后的磁盘 metadata 中为 null。本测试在重启前已核对并完成创建；**丢失创建回执后的冷关联没有因此得到验证**，仍需单独核对，不能只凭相同 ID 推断来源。

后续继续生产 Unix proxy/peer 与原执行器接入、完整原配置/权限与认证来源、真实在途工具、外部输入权及六入口。active Goal 的本片结果只验证占用检测与拒绝冲突输入，不代表所有 Goal 冷恢复副作用或自动推进都已验收。W02.06 EOF/认证、W03.02 用户反馈和 Windows 条件继续保留。
