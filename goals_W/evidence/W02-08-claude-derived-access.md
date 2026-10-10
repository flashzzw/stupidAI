# W02.08 / W02.09 · 派生 settings 的实际文件权限与目录语义

日期：2026-10-08。整体 **9/40 done**，Goal active；W02.07/W02.08/W02.09 保持 **in_progress**。

## 结论与实现方向

**把 custom settings 完整复制到另一目录，再只改插件启停键，会改变部分真实文件权限。** `Read(/anchored/**)` 与 `Edit(/anchored/**)` 中的单个 `/` 以 `--settings` 文件所在目录为基准。复制后，原目录失去原生拒绝，新目录被错误拒绝。这不是仅有来源标签差异，已由真实 Read/Write 请求复现。

在原 settings 文件旁创建一份 0600 的派生文件，只合并所选安装键，能够保留这组目录语义、逐次审批、实际读写和原会话历史。该候选在当前固定 CLI 下 **172 项全部通过**；另目录候选的172项中仍有11项失败（含汇总项），没有改写成整体通过。

本片新增两个探针，没有修改生产 Application/Runtime/bridge/Web，也没有开放已安装插件选配。下一步的派生文件生命周期应绑定原路径目录和来源版本，不能继续假定任意私有目录等价。是否可在原目录安全创建、符号链接的定位、只读父目录、应用重启和失败清理仍需实现及验证。

## 原生执行范围

使用已有生产 `Session`、SDK **0.3.280**、Claude Code **2.1.280** 和自有本地脚本供应商。在临时 HOME/XDG/profile/cwd 安装唯一的 `w0208-selected@w0208-local`，插件只有自有启动标记和声明组件，不启用 MCP。custom 文件保留自己的相对启动 hook 和原插件覆盖键。

每个完整样本先用原文件完成12个工具轮次，关闭自有查询，再以同一原生 ID、另一份 settings 文件重新打开，并完成相同12个轮次。共48次本地模型协议请求，24个真实工具请求；同目录正例包含14个成功读写结果、10个原生权限拒绝。允许的写入只针对临时目录，审批仅接受与该步骤完全相符的路径/内容；拒绝分支要求原生规则直接阻止，不能把探针的拒绝回答当权限生效。

两个 Session journal 分开放在测试目录；第二个查询通过公开 SDK 的原 ID 恢复真正的原生历史。**这不是 Application 的配置应用事务**，不代表生产可越过 Scope/快照绑定改配。

| 实际操作 | 原文件与同目录派生文件 |
|---|---|
| `./allowed` 读取 | 返回同一完整内容，无审批 |
| custom `./secret` 与 project `./project-secret` 读取 | 原生直接拒绝，无探针权限回答，内容未到供应商 |
| 配置的 `../extra` 目录读取 | 无审批、实际内容返回；另两个未加入的目录各需一次批准 |
| cwd 内批准写入 | 原生要求一次批准，实际文件字节正确 |
| cwd 内拒绝创建 | `Edit(./blocked/**)` 原生拒绝，没有权限回答、没有新文件 |
| 原 settings 目录的 `/anchored` 读取和创建 | 原生直接拒绝，没有权限回答；新文件不存在 |
| 另一目录同名路径的读取和创建 | 各需一次批准，实际结果正确 |
| 启动与安装来源 | 原文件运行自定义/安装插件两个 hook；派生文件只运行自定义 hook。原安装键和实际市场源可核对，缓存路径未冒充加载目录 |
| 原 ID 与历史 | 两阶段 ID 相同，重开前历史逐项相同，原工具 ID/结果和提示继续保留；打开查询没有模型请求 |

对照的另目录派生文件使原 `/anchored` Read/Edit 拒绝消失，出现权限请求；探针拒绝了这些意外请求，没有实际读取或创建受保护文件。另一目录则被原生拒绝，原本允许的读取/创建失败。四项准入比较均失败，真实错误、请求与文件状态保留。

`sandbox.enabled=false`，本片验证 CLI 文件工具规则，没有声称 OS 沙箱或任意 Bash/间接子进程访问受同等限制。完整有效权限、受管/remote/helper 与在途动态变化仍未验收。

## 诊断与证据

- 第一轮使用了 `Write(path)` 拒绝规则。官方文档经 Context7 返回的[权限说明](https://code.claude.com/docs/en/permissions)明确：文件路径规则使用 `Read` / `Edit`；`Write(path)` 虽被接受却不用于文件路径检查。原有裸工具名 `Write` 规则有效，本片没有更改生产只读模式。改正探针后，普通 `./` 路径的中间样本121项通过。
- 第一轮还误要求原生 journal 与供应商请求中的工具结果对象完全相同。真实 CLI 会追加上下文提示、缓存字段并裁去尾部空白。后续按同一工具 ID、错误状态和完整结果正文核对这些传输变化，未丢弃工具的实际内容。
- 首个 `/anchored` 样本171项失败，写原目录的既有文件还触发了“需要先读取文件”的另一检查。最终改用创建新文件，将权限拒绝与该前置分开；另目录172项仍失败，同目录172项通过。原始所有失败与中间脚本版本均保留。
- [独立审计](W02-08-claude-derived-access/audit.json) **99 项通过**，从原始 journal、供应商请求、启动标记、文件、安装登记与市场元数据重新核对；审计通过不提升另目录方案，`separate_directory_parity=false`、`production_installed_selection_added=false` 保持。
- [文档记录](W02-08-claude-derived-access/docs/context7.json)保留解析和查询结果。直接请求 `permissions.md` 返回 HTTP 错误，未声称取得该 URL 的全文；采用 Context7 返回的官方页面片段并以固定 CLI 实测交叉验证。

原生加载路径是 `marketplace/selected`，登记缓存为 profile 下的 `plugins/cache/.../1.0.0`，两者确实不同。原文件、自有项目设置、登记、市场源和派生文件的内容在调用前后均保持；允许写入只改变列明输出文件。两次自定义 hook 和原安装 hook 都有成对的原生成功终态。

## 复验与归档

```bash
# 已知另目录方案仍失败；查看四项 anchored 准入差异，退出 1。
python3 tests/claude_derived_settings_access_probe.py --anchors
# 同目录候选，最终172项通过，退出 0。
python3 tests/claude_derived_settings_access_probe.py --anchors --placement beside-original
node --check tests/claude_derived_settings_access_probe.mjs
```

探针 [Python](../../tests/claude_derived_settings_access_probe.py) / [Node](../../tests/claude_derived_settings_access_probe.mjs)，[命令结果](W02-08-claude-derived-access/command-results.json)、[差异](W02-08-claude-derived-access/source.diff)、[归档索引](W02-08-claude-derived-access/archive-index.json)、[清理](W02-08-claude-derived-access/cleanup.json)、[最终检查](W02-08-claude-derived-access/final-checks.json)与[manifest](W02-08-claude-derived-access/manifest.json)。必要原始材料已压缩保留，不复制完整 profile、访问令牌或数据库。

源码和构建沿用前片：开发二进制 `ad915e94e311896dd2f69157550e0dd353f8daf202b75b06fe45e520bf7268b3`，settings worker `e93bad5aa6848fa3fc8502b725ccf1ada4143bbf7006a31fbd0e3c287864b350`；旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。本片没有新的 Go/Web/bridge 修改，因此不重复构建、Go 回归和浏览器验收；Python AST 与 Node 语法检查通过。

基线9935个文件中的7223个既有 W 证据逐文件不变；全部测试句柄已收取，自有进程和本地供应商已退出，没有提交/推送。下一片将同目录候选与完整安装键、原来源版本及私有文件的创建/保留/恢复/清理连接。已安装生产选配、权限完整性、四配置、真实方法/Gate、独立前台/输入独占和 Windows 等硬条件仍未完成。
