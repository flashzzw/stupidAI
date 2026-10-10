# W01.02 · Application 进程 SIGKILL 与提交边界

日期：2026-10-02。承接[持久丢回执片](W01-02-durable.md)，在承载生产 Application、SQLite 和 Codex Runtime 的独立测试进程上实施 SIGKILL。两种权限、三个提交边界共 6 个组合 / 108 项 race 检查通过。W01.02 仍 in_progress，8/40 done，Goal active。

本片新增 opt-in 测试和 Makefile 入口，没有修改生产执行逻辑。被杀的是独立测试进程中的生产 Application，不是运行 HTTP/Unix proxy 的 `netcap serve` 二进制；真实原生协议使用固定后台 stdio。这个范围不能提升为生产完整连接链路或断电验收。

## 实际故障点与结果

| 故障点 | SIGKILL 前的真实 SQLite | 重启后的结果 |
|---|---|---|
| 创建回执提交前 | 原 dispatching 意图与人工接管已提交，NativeID 回执尚未提交；真实原生创建已成功 | 原操作转 uncertain，unknown 阶段拒绝新输入；后台重新读取创建来源，关联原线程与原操作 |
| 创建回执提交后、Save 尚未返回 | 成功操作和原生 ID 已提交，调用方尚未取得结果 | 已提交成功事实保持，原命令 ID 返回原回执，不再创建执行 |
| 恢复自动化核对成功、授权提交前 | 原 create 已完成，resume 仍为 dispatching；原生核对已成功，但拟写入的 managed/非暂停状态没有提交 | 原 resume 最终为 `not_sent`，人工接管与暂停保持；重复该命令 ID 不重新执行授权 |

每个故障点都在工作台创建操作悬而未决时先通过 `Apply(takeover)` 保存人工接管。恢复授权场景随后显式调用 `Apply(resume)`，在生产 `finishHandoff` 即将提交成功状态处停住。

存储包装器在指定边界读取**实际 Persistence.Load 结果**和拟提交状态，原子写出测试检查点，然后阻塞，不返回合成错误。父测试进程确认自有原生 PID/birth、故障点与数据库状态后，使用已持有的 `exec.Cmd.Process` 句柄 SIGKILL 子进程，并检查实际 WaitStatus。没有调用 Application.Close 代替故障；子进程的正常测试清理标记也未出现。

父测试作为 Linux subreaper 等待这棵自有进程树。旧 stdio 后台因输入管道关闭退出，六个组合均 exit 0，没有使用超时强制清理。确认退出后，才用原 profile/项目/SQLite 启动新后台；fixture 重开入口不重建 config.toml。

## 验证

| 权限 | 创建提交前 | 创建提交后 | 恢复授权提交前 |
|---|---|---|---|
| read_only | [18 项](W01-02-crash-read_only-before_commit.json) | [17 项](W01-02-crash-read_only-after_commit.json) | [19 项](W01-02-crash-read_only-resume_before_commit.json) |
| workspace | [18 项](W01-02-crash-workspace-before_commit.json) | [17 项](W01-02-crash-workspace-after_commit.json) | [19 项](W01-02-crash-workspace-resume_before_commit.json) |

[最终 race 输出](W01-02-crash-native.txt)六个组合全部通过。每份原始 JSON 包含故障前拟写入/真实持久状态、冻结的原生协议记录、实际 SIGKILL 结果、旧后台退出结果和恢复后的快照。

核对内容包括：

- 原 Session ID、原生线程 ID、创建操作和命令幂等回执保持；原配置/执行快照与人工所有权保留。
- 原线程重启后为 notLoaded，按原 ID 加载后得到原 read_only/workspace 沙箱模式及 on-request 审批。没有把 profile 中相反的默认沙箱当作原权限。
- 恢复授权的未提交结果不晋升；原 resume 操作被标为 not_sent，重复原命令不会重放。原线程的正常 `thread/resume` 加载/采样 RPC 仍会发生，它与工作台恢复自动化授权是不同对象。
- 每个组合只有一次 thread/start，零 thread/fork、零 turn/start 请求、零 turn/started 事件；所有 thread/resume 都指向原生原 ID。
- 自有项目中的 `retained.txt` 内容保持，停止和重启没有清理这个夹具产物。它是预置验证文件，不代表真实模型产物已验收。

[原始协议审计](W01-02-crash-validation.json)独立统计上述创建/恢复/轮次/分叉请求与事件。这里只说恢复流程没有新增模型输入，**暂停自动化并不等于禁止用户主动发送**；成功核对后的人工发送仍沿用原公共契约。

初次运行的[失败输出](W01-02-crash-initial.txt)与[原始数据](W01-02-crash-initial.json)保留：进程故障、原线程恢复和人工状态均通过，最后的测试断言错误地要求人工暂停后所有 send 都被拒绝。最终改为核对 unknown 阶段拒绝输入、恢复本身无新输入，没有为此改变生产发送语义。

| 回归 / 静态验证 | 结果 |
|---|---|
| 既有 Application/SQLite 丢回执矩阵 | [8 组合 / 152 项 race](W01-02-crash-durable-regression.txt)通过；对应原始 JSON 使用 `W01-02-crash-regression-application-*` 前缀 |
| 既有真实 Runtime stdio | [42 项 race](W01-02-crash-runtime-regression.txt)通过；原始 [workspace](W01-02-crash-runtime-workspace.json) / [read_only](W01-02-crash-runtime-read_only.json) |
| 带 nativecodex 标签全包 vet | [通过](W01-02-crash-vet.txt) |
| 全 Runtime/app/console 测试、构建、浏览器 | 本轮未重跑；生产源文件、Web 和开发二进制未变，归档时核对前片哈希 |

42 项旧 Runtime 回归包含合成模型轮次和 active Goal 对照；零模型轮次结论只适用于本片 6 个新组合，不能套用到全部回归。

## 入口、版本与隔离

```bash
NETCAP_NATIVE_CODEX_EVIDENCE="$PWD/.dev/w0102-crash/recheck" \
GOCACHE=/tmp/netcap-w0206-auth-go-cache make codex-application-crash \
  NETCAP_CODEX=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache make codex-application-native \
  NETCAP_CODEX=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache make codex-runtime-native \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./...
```

固定代理 0.157.1、后台 0.158.0、Go 1.26.2。新增与扩展的代码均在 `linux && nativecodex` 的 `_test.go` 中，不进入生产构建。继续使用 TDD 工作流的既有 Apply/Snapshot/Persistence 接缝；原生 API 沿用已有固定版本实测，没有新模型、SDK 或原生接口假设。

每个组合使用父测试分配的独立临时目录、自有子进程、隔离 HOME/CODEX_HOME/XDG、合成 key 和不可用的 `127.0.0.1:9` 供应商。合成历史及暂停 Goal 用来构造可持久化、可枚举的原线程；没有激活真实 Goal 或调用真实供应商。代理仅用于版本查询，协议直连测试专用 stdio。

[清理记录](W01-02-crash-cleanup.json)保存 6 个故障组合的旧原生进程与恢复进程退出结果；6 个 Application 子进程均实际被 SIGKILL，6 个旧后台被等待回收，6 个新后台正常关闭。612 个旧证据哈希不变，没有停止共享后台、提交或对外操作。[差异](W01-02-crash-diff.patch)与 [manifest](W01-02-crash-manifest.json)记录本片归档时点。

## 剩余条件

本片覆盖 Application 独立进程的三个提交边界，尚未覆盖生产 serve 的 HTTP/Unix proxy/peer/凭据与环境采样链路、后台继续存活的共享连接故障、断电/文件系统损坏、真实在途工具和输入独占。无 rollout、隐藏且无候选 ID、损坏或不唯一来源仍保留 unknown。六入口、完整配置/权限、Claude、Windows 及其他 W 系列硬要求继续按 PROGRESS 推进。
