# W02.08 · Claude 已安装插件的启动排除与 settings 组合

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 本片结论

已安装插件需要在原生初始化前按完整 `name@marketplace` 来源键排除。使用普通启动 settings 的正例成立；初始化后再 `applyFlagSettings` 并 reload，虽可清空目标清单，目标 `SessionStart` hook 已经执行。因此不能沿用“开放模型输入前核对清单”来证明启动副作用被排除。

**自定义 settings 文件与启动前覆盖的完整组合仍未通过。** SDK `settings: file` 搭配 `extraArgs.settings` 时只传出原文件参数；在公开 spawn 接缝直接追加第二个 `--settings` 则排除了目标，却丢掉原文件的设置。提前发送 `apply_flag_settings` 也被原生 CLI 拒绝：`--await-initialize` 要求第一条输入必须是 initialize。这些是本次实际尝试的限制，不推断其他所有实现方式都不可行。

本片仅新增可重跑原生探针、证据和说明，没有修改生产准入、产品 API、Web 或构建。现有同名安装来源拒绝保持；不能把上述局部正例当作已安装插件完整选配已交付。

## 实验与结果

通过真实 CLI 在隔离 profile 登记本地市场、安装 `w0208-selected@w0208-local` 和 `w0208-sibling@w0208-local`。两包各有 Skill、command、agent 和 `SessionStart` hook；hook 仅向自有临时目录写一行插件标记和测试环境哨兵。没有 MCP 服务、listener、真实供应商或模型输入。生产 `NativeHookChannel` 读取 hook 清单，SDK 公共接口读取其他组件；未访问 SDK 私有对象。

六次 SDK 查询使用同一会话 ID，每次均关闭前一自有进程后再打开。原生 hook 事件的 `session_id` 实际相同，不只比较请求参数。**没有模型轮次历史，因此只证明同 ID 查询重开，不是带真实对话/工具历史的恢复。**

| 场景 | 启动与来源的实际结果 |
|---|---|
| 原设置启用 | 两个插件、组件与准确 `@w0208-local` 来源存在；两个启动 hook 各执行一次。 |
| 启动 settings 排除目标 | 目标包、Skill/command/agent/hook 全部缺席，目标 hook 未执行；兄弟插件及其 hook 保持。 |
| 原 ID 再启用 | 目标原来源/版本与组件恢复，启动 hook 执行；兄弟插件保持。 |
| 自定义文件，初始化后停用 | 原文件哨兵生效。reload 后目标包消失，但它的启动 hook 已执行。 |
| 自定义文件 + SDK extraArgs.settings | 实际 argv 只有原文件的一个 `--settings`；额外覆盖未传出，目标包和启动 hook 均存在。原文件哨兵仍生效。 |
| 自定义文件 + spawn 追加第二个 --settings | 实际 argv 含两个参数。目标排除且 hook 未执行，但兄弟 hook 看不到原文件哨兵，原文件设置未保留。 |
| initialize 之前发送 apply_flag_settings | 独立 raw CLI 退出 1，无控制回执、无启动标记；stderr 明确要求 initialize 必须为第一行。 |

另核对安装登记表与实际加载路径：两包的 `installed_plugins.json.installPath` 指向 `profile/plugins/cache/.../1.0.0`，实际 `reloadPlugins().plugins[].path` 指向本地市场的源目录，两者不是同一路径。缓存目录真实存在且不是指向源目录的符号链接；不能只凭安装登记路径/版本构造已加载证据。市场、源文件和安装登记/缓存树在本轮查询前后摘要一致。

最终六个 SDK 子进程均退出 0，**10 次**启动 hook 都有唯一匹配的原生 `hook_response`、`exit_code=0` 和 `outcome=success`。另一个前置控制试验 CLI 按上述预期退出 1。全部请求都是控制请求，没有 user 输入、模型结果或工具审批；hook 的测试副作用是真实执行，不称为“零 hook”。

## 验证状态与失败记录

- [最终完整结果](W02-08-claude-installed-startup/final/result.json)：65 项中 62 项通过；两项组合要求失败（SDK 覆盖未排除、重复 CLI 参数未保留自定义文件），另有 SDK 测试执行器因断言失败退出 1 的汇总项。**完整 probe 仍退出 1，未改成仅跑正例来宣称通过。**
- [独立审计 38 项](W02-08-claude-installed-startup/audit.json)通过：从实际 argv、控制收发、原生事件、启动标记和加载路径复核正反例。审计通过表示这些结论可复核，`integration_gate_passed` 仍为 false。
- [最初四控制场景](W02-08-claude-installed-startup/first-controls/result.json)30 项通过，仅用于初步确认时序；最终补充了真实事件 ID、逐 hook 完整终态、启动参数、custom 哨兵和两个组合分支，不与初轮计数累加。
- [SDK 组合初次失败](W02-08-claude-installed-startup/sdk-combination-failure/result.json)、[独立 preinitialize 失败](W02-08-claude-installed-startup/preinitialize-failure/wire.json)和[stderr](W02-08-claude-installed-startup/preinitialize-failure/stderr.log)、[中间完整控制](W02-08-claude-installed-startup/expanded-controls/result.json)均保留。它们是原生契约实验，不是已经修复的产品 bug。
- Python AST 与 Node syntax 检查通过。生产代码/依赖/界面/二进制未改，不重复 Go race、浏览器或构建。

最终原始目录为 `/tmp/netcap-w0208-installed-session-dhwqkb8w`；完整目录列表见 [original-directories.json](W02-08-claude-installed-startup/original-directories.json)。所有试验句柄均已收取；没有待运行进程需要重启或继续等待。

## 固定版本与复验

```bash
# 完整组合预期目前失败，退出 1；查看 result.json 中的两个实际契约失败。
python3 tests/claude_installed_plugin_session_probe.py
# 离线审计只确认已归档实验的结论，不能提升为生产集成通过。
python3 goals_W/evidence/W02-08-claude-installed-startup/audit.py
node --check tests/claude_installed_plugin_session_probe.mjs
```

固定 Claude Code **2.1.280**、SDK **0.3.280**。使用 `find-docs` 尝试 Context7 库解析，20 秒超时退出 124、无资料返回，见[查询记录](W02-08-claude-installed-startup/docs-result.json)。没有据此声称在线官方文档已核对；依据为[已安装 SDK 类型片段](W02-08-claude-installed-startup/sdk-contract-excerpts.txt)、既有已归档查询协议和本轮真实 argv/wire/事件。SDK 的 settings 是文件路径或对象这一联合类型，本身不构成支持两者合并的保证。

[差异](W02-08-claude-installed-startup/source.diff)、[产物 manifest](W02-08-claude-installed-startup/manifest.json)及[完整性核对](W02-08-claude-installed-startup/integrity.json)记录归档时点。3128 个旧 W 证据未改。开发二进制仍为 `7dadabec610c43296d7c0f0cf0bfcf800871bbb5403a718c64afe8b4ead4471c`；旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。只复制白名单测试结果，不归档 profile 凭据或原生数据库。没有提交、推送、共享后台或 Windows 操作。

## 后续条件

生产接入前需建立保留自定义文件语义的启动前设置组合，并把完整安装来源键、适用 scope、市场来源、实际加载路径和内容版本绑定起来。停用必须在可能执行启动 hook/MCP 等副作用之前成立；原生重载后的空清单不能替代这一条件。带模型历史的原 ID 恢复仍要另验。

完整来源/受管控制、实际方法/Gate、原生丢确认安全恢复、输入独占、成功/async/通用 hook 的其余范围、Windows/proxy/peer 和四个既有 WSL 请求仍保留。本片给出新的实现约束，没有降低 W02.07/W02.08 或整条 W 系列完成标准。
