# W01.02 · 持久状态与真实原生丢回执恢复

日期：2026-10-02。新增生产 Application、真实 SQLite 与 Codex Runtime 组合的故障验收：原生创建已经成功，分别在传输回执交付前、工作台回执落盘前注入失败，重开状态库与后台后核对原执行。8 个组合、152 项检查通过；W01.02 仍 in_progress，8/40 done，Goal active。

本片新增测试与 Makefile 入口，没有修改生产执行逻辑或 UI。测试使用固定 CLI 的 stdio 传输，未覆盖 Unix proxy/peer、HTTP 监听器或整个工作台进程被强制终止。

## 两种故障与两条恢复路径

1. **传输回执丢失**：真实 `thread/start` 已返回成功，测试传输在交给 `Codex.Execute` 前截住该回执；准备合成持久历史后关闭这条自有连接，向 Runtime 返回 `unexpected EOF`。工作台保存 uncertain 操作，没有 NativeID 回执。
2. **回执落盘失败**：Runtime 已成功返回原生 ID，`Persistence.Save` 在 create 成功状态提交前报错。此前的 dispatching 意图和人工接管已经写入真实 SQLite；内存权威状态也没有晋升 NativeID，新输入被拒绝。随后通过正常 Close/Open 重开状态库，未模拟断电或进程硬崩溃。

两种情况都在回执悬而未决时通过 `Application.Apply(takeover)` 转人工。重开前核对原生线程为原 ID、notLoaded；重开后确认工作台的 Session/Operation 仍无 NativeID，再执行以下路径：

- **原生列表可见**：夹具先通过原生 API 设置一个暂停的 Goal，线程可以被 `thread/list` 返回。生产轮询用原操作标记、原生日志头及绑定核对后自动关联；没有重发 create。
- **原生列表隐藏**：只有合成注入历史的样本不在列表中。工作台保持 unknown、阻止 send/resume；不存在的候选 ID 被拒绝，显式提供正确的原生 ID 后，通过生产 associate 流程核对同一创建标记并关联。

随后核对原 Session、原 create Operation、原生 ID、项目、状态命名空间、执行配置、路由与执行快照保持一致；人工接管和暂停保持。权限分别为 read_only/workspace，恢复得到原模式、on-request 审批及关闭的网络权限，尽管 profile 默认沙箱故意与原选择相反。再次重开 SQLite，已恢复绑定与命令幂等回执仍存在。

## 发现的原生枚举边界

[初始失败](W01-02-durable-unlisted-red.txt)与[原始状态/协议](W01-02-durable-unlisted-red.json)表明：仅注入历史的线程已持久化，可用原 ID `thread/read`，但自动查找持续返回空列表，因此没有自动关联。没有将这个失败改称自动恢复通过。

最小原生对照分别记录[无 Goal](W01-02-durable-visibility-no-goal.json)与[暂停 Goal](W01-02-durable-visibility-paused-goal.json)：

| 观察 | 无 Goal | 暂停 Goal |
|---|---|---|
| 原 ID 只读读取 | 成功 | 成功 |
| 带 cwd/provider/source 筛选的列表 | 0 条 | 1 条 |
| 不带这些筛选的列表 | 0 条 | 1 条 |
| 状态库中原线程行 | 存在 | 存在 |
| `has_user_event` / title / first_user_message | 0 / 空 / 空 | 0 / 空 / 空 |

这排除了本样本的 cwd/provider/source 筛选不匹配；也不能仅用 `has_user_event=0` 解释所有可见性，因为暂停 Goal 的样本仍是 0。这里只记录固定版本的实际行为，不推导未经验证的通用枚举规则。

诊断仅只读查看自有原生状态库，未改写 SQLite 行或 rollout；诊断代码已移出测试目录，归档为[无 Goal 源码](W01-02-durable-visibility-probe-no-goal.go.txt)和[暂停 Goal 源码](W01-02-durable-visibility-probe-paused-goal.go.txt)。固定版本的[ThreadListParams schema](W01-02-durable-ThreadListParams.json)没有 includeEmpty 选项。官方文档搜索与页面读取均因已配置代理不可连接而失败，见[来源记录](W01-02-durable-docs-check.json)；本片没有新取得的官方网页内容。

因此，“存在 rollout”也不足以保证**无回执自动查找**一定能枚举到候选。没有原 ID 且列表不返回时，仍保留 unknown；显式候选入口可补足定位，但必须通过原操作身份核对，不能凭路径或时间猜测。

## 验证结果与入口

| 组合 | 回执故障 | 检查数 / 原始记录 |
|---|---|---|
| read_only，列表可见 | 落盘失败 | [17 项](W01-02-durable-application-read_only-listed-persistence.json) |
| read_only，列表可见 | 传输丢失 | [17 项](W01-02-durable-application-read_only-listed-transport.json) |
| read_only，列表隐藏 | 落盘失败 | [21 项](W01-02-durable-application-read_only-hidden-persistence.json) |
| read_only，列表隐藏 | 传输丢失 | [21 项](W01-02-durable-application-read_only-hidden-transport.json) |
| workspace，列表可见 | 落盘失败 | [17 项](W01-02-durable-application-workspace-listed-persistence.json) |
| workspace，列表可见 | 传输丢失 | [17 项](W01-02-durable-application-workspace-listed-transport.json) |
| workspace，列表隐藏 | 落盘失败 | [21 项](W01-02-durable-application-workspace-hidden-persistence.json) |
| workspace，列表隐藏 | 传输丢失 | [21 项](W01-02-durable-application-workspace-hidden-transport.json) |

[最终 race 输出](W01-02-durable-native.txt)8 个组合全部通过。[原始协议审计](W01-02-durable-validation.json)逐项核对每个组合恰好一次 thread/start、所有 thread/resume 都指向原 ID、零 thread/fork、零 turn/start 请求及零 turn/started 事件。列表可见样本的 Goal 仍为原目标与 paused。

[既有 Runtime stdio 回归](W01-02-durable-runtime-regression.txt)42 项通过，原始数据：[workspace](W01-02-durable-runtime-workspace.json) / [read_only](W01-02-durable-runtime-read_only.json)。这项回归会执行合成模型轮次与 active Goal 占用对照；“零模型轮次”只适用于上表新建的 8 个组合，不能外推到这 42 项回归。

[带 nativecodex 标签全包 vet](W01-02-durable-vet.txt)通过。生产源文件、Web 资源与开发二进制未改，已与上一片 manifest 核对哈希；本轮未重跑全 Runtime/app/console 测试、开发构建或浏览器。新增文件均为 `linux && nativecodex` 的 `_test.go`，不会进入生产二进制。外部测试包通过现有 Apply/Snapshot/Persistence 接口验证，复用上一片 stdio 读写器，没有新增生产连接绕过接口。

```bash
NETCAP_NATIVE_CODEX_EVIDENCE="$PWD/.dev/w0102-durable/recheck" \
GOCACHE=/tmp/netcap-w0206-auth-go-cache make codex-application-native \
  NETCAP_CODEX=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache make codex-runtime-native \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./...
```

固定版本：代理 0.157.1、后台 0.158.0、Go 1.26.2。[最初版本准入失败](W01-02-durable-version-initial.txt)来自夹具将后台路径误填为代理路径；改用现有固定代理，未放宽生产版本门槛。[中途代次断言失败](W01-02-durable-generation-initial.txt)来自错误地要求两次显式 associate 后代次仍不变；最终核对已接受命令后的代次不被迟到结果回退，人工所有权始终保持。没有为这些夹具问题修改生产逻辑。

## 隔离、收尾与剩余项

每个组合独立 HOME、CODEX_HOME、XDG、项目和 SQLite，只使用合成历史、合成 key 与不可用的 `127.0.0.1:9` 供应商。代理本轮只执行版本查询；真实协议经固定后台 stdio，不是生产 socket 链路。暂停 Goal 仅用于构造可见原生记录，没有推进真实任务。

[清理记录](W01-02-durable-cleanup.json)确认最终 24 个自有原生进程均经测试等待退出，exit 0；584 个旧证据哈希不变。没有停止共享后台、迁移用户状态、提交或对外操作。[差异](W01-02-durable-diff.patch)与 [manifest](W01-02-durable-manifest.json)记录本片归档时点。

本片补齐两类回执故障下的持久状态组合证据。整个工作台进程被杀、断电/WAL 故障、生产 Unix proxy/peer/认证链与环境采样端到端仍未覆盖。无 rollout、不可枚举且没有原 ID、损坏/不唯一候选继续保留 unknown；完整权限规则、原执行输入独占、在途工具、六入口及 Windows 等硬要求继续保留。
