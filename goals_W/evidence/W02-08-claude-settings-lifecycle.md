# W02.08 · Claude 设置热更新与关闭/重开边界

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 结论与下一步决定

不能仅放宽 Application 的来源漂移检查，就把现有 close/reopen 作为“旧设置保持到明确采纳”的完整实现。固定 Claude **2.1.280** 对 project、cwd local、主仓库 local 的外部修改会自动应用：在明确重开之前触发原有 `ConfigChange` hook，关闭时执行 v2 `SessionEnd`。较短等待得到的旧 hook 保留结果不能用作配置固定依据。自定义 `--settings` 文件在本次窗口内保留 v1，行为不同。

可行候选是公开 SDK **0.3.280** 的 `ConfigChange` callback 返回 `{decision:"block"}`。对三类普通来源，回调收到准确的来源、文件路径和原生 ID，原 hook 清单与 v1 关闭行为保持；源文件仍是 v2。原有用户 `ConfigChange` hook 仍按旧配置运行，没有被测试 callback 删除。自定义文件没有回调，只作为原生缓存行为的控制组。

**生产代码未改变，Application 仍拒绝来源漂移。** 本片只完成原生契约与候选验证。下一步应将普通来源的阻止决策与已保存文件版本绑定，记录变化与可观察原因，再验证明确采纳的准备/应用/回执丢失/冷恢复流程。不能把 standalone callback 的通过当作生产冻结或设置采纳已接通。

## 正反对照

所有案例只使用隔离 HOME/XDG/profile、临时 Git 仓库、测试脚本和不可用 loopback 供应商，没有用户输入或模型调用。每份配置含版本环境哨兵、只读 deny 规则元数据、SessionStart/SessionEnd 测试 hook。文件由 v1 改为 v2，原文件实际 hash 与独立标记保存。

| 对照 | 实际结果 |
|---|---|
| 直接原生控制，4 类来源 | [25/28](W02-08-claude-settings-lifecycle/native-final.json)，退出 1。project/local/primary 的原 hook 保留断言失败，关闭执行 v2；custom 保留 v1。`get_settings` 与 hook 清单在本轮立即读取时已返回 v2。后续约 0.2、1.2、3.2 秒读数保持，不据此推断精确的 watcher 阈值。 |
| 原生产 Session，250ms 等待 | [44/44](W02-08-claude-settings-lifecycle/session.json)，四类均按原 ID 关闭/重开，v1 end→v2 start。**后续较长窗口推翻了将此作为通用固定契约的解释**；原结果仍保留。 |
| Session 延长到 3500ms | [41/44](W02-08-claude-settings-lifecycle/session-final.json)，退出 1。三类本地来源均执行 v2 end→v2 start；custom 仍 v1 end→v2 start。ID、历史摘要和新连接成立，Session 的低层 restart 报告 verified，但它没有 Application 的设置版本事务；不能用于放行生产来源漂移。 |
| 排除只读请求的必要性 | [完全不再读取](W02-08-claude-settings-lifecycle/no-read.json)、[只读 hooks](W02-08-claude-settings-lifecycle/hooks-read.json)、[只读权限](W02-08-claude-settings-lifecycle/permissions-read.json)、[只读 settings](W02-08-claude-settings-lifecycle/settings-read.json)各 6/7、退出 1，local 均执行 v2 end。完全不读的 trace 只有改动前四个控制请求；所以后续 metadata 读取不是复现新版关闭行为的必要条件。 |
| SDK 关闭方式对照 | [延迟 Session local](W02-08-claude-settings-lifecycle/session-delayed.json)10/11、退出 1，重现 v2 end；差异不能仅归因于直接 helper 的 SIGTERM。 |
| 原有 ConfigChange hook | [Session local](W02-08-claude-settings-lifecycle/session-config-hook.json)10/12、退出 1：明确 apply 前已实际执行 v1 ConfigChange，关闭使用 v2 end。未执行模型输入。 |
| SDK callback 返回允许 | [5/6](W02-08-claude-settings-lifecycle/sdk-allow.json)，退出 1；准确收到 local payload，仍执行 v2 end，形成阻止候选的负对照。 |
| SDK callback 返回阻止 | [最终 24/24](W02-08-claude-settings-lifecycle/sdk-block-final.json)，退出 0；三来源 callback 与路径/ID 准确，清单不变，关闭执行 v1、文件仍 v2。custom 无 callback；不是该机制对 custom 生效的证明。 |
| SDK 与用户 hook 并存 | [6/6](W02-08-claude-settings-lifecycle/sdk-block-coexist.json)，退出 0；保留 v1 start→v1 ConfigChange→v1 end，准确 callback 仍返回阻止。 |
| 独立核对 | [60 项](W02-08-claude-settings-lifecycle/audit.json)通过，比较独立脚本标记、文件 hash、原生控制投影、Session journal 与 SDK payload，不把 producer 的通过位当作依据。 |

初轮脚本直接 `json.load(stdin)`，因当前代理环境的 hook stdin 为空而失败；[初轮记录](W02-08-claude-settings-lifecycle/native.json)保留。随后脚本显式记录 stdin 字节数，独立进程 hook 的正文载荷仍为 0；只把实际文件标记和原生成功终态用于确认脚本作用，**没有声称 hook stdin 载荷问题已解决**。SDK callback 的 payload 走原生控制协议，本次取得准确 `ConfigChange` 输入，两者不能混称。

## 官方与固定版本契约

[官方 hooks 文档](https://code.claude.com/docs/en/hooks)说明 ConfigChange 可用退出 2 或 JSON `decision:block` 阻止设置应用。`policy_settings` 的阻止会被忽略；服务端受管设置、Windows 注册表/macOS 偏好与部分 WSL Windows 受管更新不触发同样的 hook。`reason` 不会展示给用户，原生只写 debug 日志。因此生产若采用此机制，仍需自己的可观察状态与严格的受管来源边界。

本轮 Context7 实际取得官方文档，保存在 [docs.json](W02-08-claude-settings-lifecycle/docs.json)。[固定 SDK 类型](W02-08-claude-settings-lifecycle/sdk-contract.txt)确认 `ConfigChangeHookInput`、公开 callback/`Options.hooks` 和 `SyncHookJSONOutput.decision`，实验通过公开 `query()` hooks 入口实现，没有使用 SDK 私有方法。原生只读 hook 清单仍使用现有 `NativeHookChannel` 的公开 spawn 接缝，没有生产新增通用控制接口。

尚未验证：hooks 全部关闭/受管 only 模式下 callback 是否可用、普通 user settings、删除/新增/链接切换、损坏文件与 callback 超时/失联、受管和服务端来源、所有设置字段、实际工具权限、在途模型任务、持久丢回执与重启。源文件关联脚本及其他资源也没有因此冻结。没有固定精确的 watcher 生效毫秒数，3500ms 仅为本轮复现窗口。

## 复跑与归档

```bash
# 原始保留旧配置的前置断言会在三类来源失败，预期记录退出 1。
python3 tests/claude_settings_lifecycle_probe.py
python3 tests/claude_settings_lifecycle_probe.py --session
python3 tests/claude_settings_lifecycle_probe.py --case local --reads none
python3 tests/claude_settings_lifecycle_probe.py --session --case local --config-hook
python3 tests/claude_settings_lifecycle_probe.py --case local --sdk-hook allow
# 可行候选与原用户 hook 共存；本轮退出 0。
python3 tests/claude_settings_lifecycle_probe.py --sdk-hook block
python3 tests/claude_settings_lifecycle_probe.py --case local --sdk-hook block --config-hook
python3 goals_W/evidence/W02-08-claude-settings-lifecycle/audit.py
```

[Python 入口](../../tests/claude_settings_lifecycle_probe.py)、[生产 Session 夹具](../../tests/claude_settings_lifecycle_session.mjs)、[SDK callback 夹具](../../tests/claude_settings_lifecycle_guard.mjs)均通过语法检查。原生产、依赖、Web 与构建未改，无需重跑 Go、浏览器或构建。本片不是新 HTTP/Windows/真实方法任务验收；五项既有 WSL 请求仍待结果。

最终原始目录：raw `/tmp/netcap-w0208-settings-lifecycle-3so4a5li`，Session `/tmp/netcap-w0208-settings-lifecycle-8s3k841i`，SDK 阻止 `/tmp/netcap-w0208-settings-lifecycle-2m1ujebx`；所有中间/对照目录见[清单](W02-08-claude-settings-lifecycle/original-directories.json)。归档按来源保存 `projection.json`、独立 `hooks.jsonl`、测试脚本和实际最终设置文件；projection 仅保留本次哨兵、规则、来源与生命周期信息，没有复制完整初始化账户信息、原生配置或数据库。

14 个测试运行、29 个案例对应 **39 个原生查询**均有终态。直接 helper 为 SIGTERM/143；SDK callback 查询正常退出 0，生产 Session journal 正常 closed。所有工具句柄已收取，没有存活任务、监听器、共享后台操作、项目提交或对外发布。

- **3470** 个旧 W 证据逐文件 hash 不变。
- 开发二进制未改：`3e9a0c8113d4622f1bdcdeb4686c1784d940a775059afe5a9bc54e339e0b7d7e`。
- 旧 bin 未改：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。
- [source.diff](W02-08-claude-settings-lifecycle/source.diff)及[manifest.json](W02-08-claude-settings-lifecycle/manifest.json)记录此片测试/文档与归档哈希。

保持 W02.07/W02.08 in_progress。下一片先建立普通来源热更新阻止与版本绑定的生产候选，验证失效条件，再接明确采纳；完整受管/安装控制、方法/Gate、原生丢确认恢复、输入独占、Windows/proxy 和其他 hook 硬要求不变。
