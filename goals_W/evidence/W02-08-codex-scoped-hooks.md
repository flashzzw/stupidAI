# W02.08 · Codex 逐线程 hook 信任、排除与原 ID 恢复

2026-10-05，固定 app-server 0.158.0。**109 项原生契约检查通过**：五个线程、14 个输入轮次、19 次明确超时的自有 hook 调用。已验证独立包元数据怎样绑定到实际来源 key、逐线程信任隔离、整包排除、原 ID 改配和显式冷恢复。该片新增可重跑探针，未接通产品 hook 会话选配；成功执行和恢复前逐项当前信任核对仍未收口。W02.07/W02.08 in_progress，9/40 done，Goal active。

## 原生结果

[探针](../../tests/codex_scoped_hook_selection_probe.py)建立三个独立插件包：继承包、选择 v1、选择 v2。三个包具有相同原始 handler 定义；各自脚本写入标明版本的自有输入/PID 记录，然后执行 sleep 2，原生期限为 1 秒。使用 UserPromptSubmit 使每轮都能验证配置；所有轮次指向不可用的本地供应商并明确中断。

包元数据先在独立 profile 读取；该环境使用 `@inspection-…` 来源身份。执行 profile 中的对应副本使用 `@netcap-…`，只在 profile 全局启用并信任继承包，选择 v1/v2 始终通过线程覆盖启用。原生目录清单一直只返回继承包，原配置字节和全部缓存/源文件前后不变。

| 场景 | 实际调用 |
|---|---|
| A：启用 v1，并信任其实际来源 key + 原生 hash | 继承包、v1 |
| B：相同 v1 来源，未给该线程信任 | 仅继承包；A 的信任未泄漏 |
| C：有 v1 信任但禁用整包 | 仅继承包 |
| D：启用 v2，却只给 v1 key 信任 | 仅继承包；相同 handler hash 不替代完整 key |
| E：启用 v1，却直接使用 inspection key 授信 | 仅继承包；包读取身份不能直接用作执行信任 |
| A：热线程通过 disabledPluginIds 禁用，再启用 | 分别仅继承包、继承包 + v1 |
| A：未退订就热 resume，并传入撤销 v1 信任的配置 | 仍调用 v1；新覆盖没有替代已加载信任 |
| A：退订后原 ID 禁用，再退订后升级到 v2 并给新 key 信任 | 分别仅继承包、继承包 + v2；v1 不再调用 |
| B：A 改配后再次输入 | 仍仅继承包 |
| 自有后台重启，A 原 ID 恢复时省略配置 | 仅继承包；原 v2 选择丢失 |
| A 退订后原 ID 明确重传 v2 选择及信任，B 原 ID 明确重传未信任选择 | 分别继承包 + v2、仅继承包 |

每轮都核对实际 stdin 中的 session_id/prompt、标记版本、原生 sourcePath、failed 终态、至少 900ms 耗时及 `hook timed out after 1s`，之后确认不可用本地供应商连接错误和中断终态。没有把超时调用或无模型输出当作成功 hook/模型结果；继承包和选择包的失败记录可区分。五个线程在同一后台保持独立配置；该片没有验证两个 hook 同时在途的竞争行为。

原始元数据 key 的来源前缀替换成对应执行 pluginId，保留原生后缀和 hash 后，实际调用成立；错误 namespace 的 E 控制不会调用。v1/v2 完整包内容及实际 key 不同，而原生 handler hash 相同。这是固定样本的原生绑定契约，不是通用 hash 算法，也不允许把未经版本复核的用户输入 key 直接交给执行器。

## 输入前回读的剩余边界

每次 turn/start 之前均保存 thread/read；另检查实际 start/resume 响应、目录 config/read、rollout 的 turn_context/thread_settings_applied/world_state，以及自有 SQLite 表结构，汇总见[读取面](W02-08-codex-scoped-hooks/readback-surface.json)。

- config/read 可读取目录层的 hooks.state，因而可在组合覆盖时保留继承 key；它不是该线程当前信任表。
- hooks/list 只有 cwds 参数，始终返回原 profile 的继承包。start/resume、ThreadSettings 和已检查的持久字段没有逐项 hook 当前信任或已加载配置摘要；ThreadExtra 为空。
- 重新从固定二进制生成的 [ClientRequest](W02-08-codex-scoped-hooks/schema/ClientRequest.ts)、[HooksListParams](W02-08-codex-scoped-hooks/schema/HooksListParams.ts)、[ConfigReadParams](W02-08-codex-scoped-hooks/schema/ConfigReadParams.ts)、[ThreadSettings](W02-08-codex-scoped-hooks/schema/ThreadSettings.ts)与实际响应相符。server/diagnostics 返回进程及 gauges，不提供线程 hook 配置。
- ThreadSettings 的生成注释仍写 disabledPluginIds 尚不筛选能力，但本片 UserPromptSubmit 的实际排除正反例成立；不根据这条注释否定实测，也不据此宣称所有组件/事件已验证。

因此，不能靠目录清单、历史调用记录、resume 返回同一个 ID 或一次请求成功，把任意已加载会话标为“当前 hook 信任已核对”。生产接入需区分本连接明确建立的配置与来源不明的已加载配置：绑定完整资源版本及本次原生定义；保留继承状态；只在明确建立配置的路径上记录准备依据，连接代次/加载身份不明时撤销该依据。冷恢复要重新应用原绑定；热 resume 不能作为改配完成证据。完整当前信任回读或等价的有效配置核对仍需在实现中解决，不以本片调用成功触发过一次来代替。

## 证据、复现与状态

```bash
cd /home/zzw/codex-netcap
python3 tests/codex_scoped_hook_selection_probe.py
```

最终原始目录 `/tmp/netcap-w0208-scoped-hooks-etpu0app`。归档包含[109 项结果](W02-08-codex-scoped-hooks/native/result.json)、[计数](W02-08-codex-scoped-hooks/counts.json)、三个后台协议、标准错误、自有源码、stdin 标记及各会话 rollout。metadata/initial/cold 三个后台均退出 0，没有 config 写入 RPC、显式 shell/MCP 工具请求或成功模型结果。仅建立和更改隔离夹具，没有监听或连接用户后台。

最初 74 项、扩展排除/恢复后的 100 项、加入身份变换后的首次 109 项分别保存在 earlier-* 目录；最终又增加输入前原生读取及明确超时正文/耗时核对。各轮都通过，计数不累加。固定版本 schema 生成和 Python AST 检查通过。生产 Go/Web 与两个二进制未改，未重复 race、构建或浏览器；本片也没有运行既有已失败的成功 hook/通用格式矩阵。

[审计](W02-08-codex-scoped-hooks/audit.json)、[差异](W02-08-codex-scoped-hooks/changes.diff)和[归档时点哈希](W02-08-codex-scoped-hooks/manifest.json)记录本片范围、旧证据及源码/二进制。OpenAI Docs 对 key/hash 信任和“启用不自动授信”的要求沿用[已核对摘录](W02-08-codex-hook-trust/docs.txt)，本片新结论来自固定版本协议与实际调用。

下一片将已验证契约接入生产会话的 hook 选择/信任准备与完整资源绑定，同时为来源不明的热加载/恢复保留未知和输入拦截。普通目录 Skill 恢复、方法/Gate、通用 hooks、实际成功/异步、受管来源、Windows/proxy 和输入独占硬要求继续保留。四个既有 WSL 脚本结果仍待返回，不重复请求；未提交、未发布。
