# W02.08 · Claude 启动前 settings 派生文件候选

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 结果与范围

在独立配置中，将原 settings 对象完整写入私有派生文件，只合并目标 `enabledPlugins` 键，能够在插件启动 hook 之前排除该安装来源，同时保留本次测试的原设置、继承设置、权限规则元数据和自定义 hook。实际有效设置、逐来源设置和已应用 model/effort 的差异都只涉及目标插件键；原有另一个插件覆盖键没有丢失。

这是上一片[启动排除与 settings 组合](W02-08-claude-installed-startup.md)失败后取得的可行候选。没有重写其失败记录：SDK 额外 settings 参数、重复 CLI 参数、preinitialize 控制三条路线的原结论仍有效。**本片未接通 Application 的已安装插件来源绑定、漂移检查或版本事务；生产准入仍拒绝同名安装来源。**

## 原生对照

原文件位于 `custom/location/original.json`，工作目录为 `work/`，派生文件位于独立 `state/settings/`。三处各有不同标记的同名相对 hook 脚本及 `extra/scratch/secret` 目录。固定 CLI 依次启动原文件、相同内联对象、不同目录的同内容文件、合并排除的内联对象、合并排除的派生文件；每次先初始化，再读取 `get_settings`、`list_permission_rules`、插件和 hook 清单。

| 验证对象 | 实际结果 |
|---|---|
| 同内容的文件/对象/复制文件 | 原生返回的 effective、sources、applied 全部相同，权限规则元数据相同。 |
| 合并目标排除 | 相比原文件，只有目标插件有效值由 true 变 false，flagSettings 多出该覆盖；其他字段逐项相等。 |
| 原有覆盖与继承 | 原文件的 `w0208-preserved@w0208-local=false` 保留；项目 env 和 `Read(./project-secret/**)` deny 规则保持原来源。 |
| 相对 hook 与环境 | 五种方式均执行工作目录的 `./local-hook.py`，原 cwd 和自定义环境哨兵一致；没有改用设置文件所在目录的同名脚本。 |
| 安装插件的启动作用 | 原文件/相同对象/复制文件均产生安装插件启动标记；两种合并候选均无该标记，目标包清单缺席。 |
| 私有文件与原资源 | 派生目录 0700、派生文件 0600；原自定义文件、原项目和插件源内容摘要不变。 |

`get_settings` 对本次样本未报告设置文件位置差异。这不证明任意设置的文件位置语义都相同：本片 `sandbox.enabled=false`，相对 allowWrite/denyRead 仅回读配置；`additionalDirectories` 虽出现在有效设置中，`workspaceDirectories` 仍为空。因此没有宣称沙箱访问范围、实际额外目录授权或工具权限执行通过，也不据此抹除其后续验收。

## 生产 Session 与验证状态

随后把原文件和派生文件分别交给**已有**生产 Session 的 `settings_file` 启动入口，使用独立 journal、相同原生 ID。原生 hook 事件 ID 实际匹配，原文件的两个 hook 和派生文件的一个 hook 均收到唯一成功终态。候选保留自定义 hook，目标安装插件的启动标记、Skill 描述和 hook 清单均缺席；没有 user input 或模型 result。

这两次启动没有经过 Application 的原 ID 改配事务，派生文件也是测试夹具生成的。没有模型轮次历史；不把它称为生产已安装插件改配或带工具历史恢复。

- [最终原生对照 43 项](W02-08-claude-startup-settings/native-with-session/result.json)通过，其中含 Session 结果的汇总项；[Session 自身 16 项](W02-08-claude-startup-settings/native-with-session/session-result.json)通过，两者不相加计算独立断言数。
- [独立审计 36 项](W02-08-claude-startup-settings/audit.json)通过：重新比较原生设置、权限来源、真实启动标记、控制请求和关闭后的 journal，不依赖生产脚本的 pass 字段。
- `Native` 测试 helper 只新增原样传递字符串 settings 的支持，旧对象调用保留 JSON 序列化。[原生回归](W02-08-claude-startup-settings/helper-regression/result.json)的 enabled 18 项、installed-disabled 15 项通过。
- Python AST 与 Node syntax 检查通过。生产源码、Web、依赖和构建未改，因此没有重复 Go/浏览器/构建检查。

最终原始目录 `/tmp/netcap-w0208-startup-settings-gz12y0xp`。初步采样、41 项对照、42 项嵌套键/权限增强和最终 Session 验证分别保留，见[原始目录列表](W02-08-claude-startup-settings/original-directories.json)，不累加统计。五个 raw CLI 完成读取后由 helper 正常发出 SIGTERM 收尾，记录退出码 143；两个生产 Session 正常 Close，journal 以 closed 结束。最终流程共 11 次测试启动脚本标记，其中 Session 的 3 次另外有完整原生成功终态证据。所有测试句柄已结束。

## 复验与产物

```bash
python3 tests/claude_startup_settings_probe.py
python3 tests/claude_plugin_contract_probe.py --case enabled --case installed-disabled
node --check tests/claude_startup_settings_session.mjs
python3 goals_W/evidence/W02-08-claude-startup-settings/audit.py
```

固定 Claude Code 2.1.280、SDK 0.3.280。`get_settings` 和 `list_permission_rules` 是已安装固定 SDK 声明的 wire 请求，本片直接用自有 CLI 读取；没有冒称它们是公开 Query 方法，也没有访问 SDK 私有对象。接口类型片段见[固定契约](W02-08-claude-startup-settings/native-contracts.txt)。沿用上一片 Context7 超时的事实，没有无新条件重试在线调研。

[差异](W02-08-claude-startup-settings/source.diff)、[manifest](W02-08-claude-startup-settings/manifest.json)与[完整性核对](W02-08-claude-startup-settings/integrity.json)记录本片产物。3191 个既有 W 证据未改。开发二进制仍为 `7dadabec610c43296d7c0f0cf0bfcf800871bbb5403a718c64afe8b4ead4471c`，旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。仅归档自有测试结果，不复制 profile 凭据或数据库。没有模型/MCP 调用、listener、共享后台、Windows、提交或对外发布。

## 下一步

继续把原文件身份/内容版本、私有派生文件生命周期与完整安装来源绑定纳入生产事务，并在输入及恢复前复核。需要保持原文件的来源解释，不把派生文件误称用户原配置；还需核对实际市场源与登记缓存、同名补位、受管来源、热更新和权限访问效果。完整方法/Gate、原生丢确认恢复、输入独占、Windows/proxy/peer、其他 hook 范围和四个既有 WSL 请求继续保留。
