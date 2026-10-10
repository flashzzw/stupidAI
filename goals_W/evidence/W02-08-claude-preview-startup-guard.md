# W02.08 / W02.09 · 设置预览的当前启动冲突守卫

日期：2026-10-09。W02.07/08/09 继续 **in_progress**，目标 **active、9/40 done**。

## 问题与交付

上片新增的已安装插件设置预览没有复用既有 `ValidateClaudeExecutionSettings`。原会话创建后，当前 user/project/local/custom 文件新增 `apiKeyHelper` 或覆盖路由/状态目录的 env，预览仍可能构造派生文件并启动原生校验进程。官方 `--bare` 说明明确允许通过 `--settings` 提供 `apiKeyHelper`，所以 bare 不能作为禁止认证 helper 的依据。

生产代码增加两处检查：预览开始读取/构造前检查当前已知设置来源；公共原生校验器启动前再检查它实际消费的 settings 文件和已知来源。第二处覆盖读取期间才加入冲突的派生候选。拒绝保持原 Scope、快照、输入归属与操作，已构造的候选按原生命周期回收。独立安装登记和原生只读认证入口保留各自准入规则。

只修改两个 Runtime 函数，共 11 行（含注释）。没有修改原生桥接 JS、Web 生产代码、依赖或正式插件启停规则。

## 验证与实际环境

- 当前工具权限为 workspace-write、受限网络、禁止提权。匿名 Unix socketpair send、Unix bind/listen、本地 TCP bind/listen 均实测 `EPERM`，见[前置](W02-08-claude-preview-startup-guard/prerequisites.json)。本轮没有运行需要这些条件的真实 CLI/SDK 调用或本地供应商验收，也没有重复索取用户代跑。
- Runtime 正式预览接口配合自有传输 worker，初始 **16 个场景全部失败**：四类来源 × `apiKeyHelper`、`ANTHROPIC_*` 路由覆盖、HOME 状态覆盖、后端选择覆盖，均留下实际启动 marker。修复后全部在启动 marker 产生前拒绝。
- 来源读取期间才加入 helper 的补测通过：初始检查后原文件变化，派生候选仍由公共校验器拒绝，临时文件实际回收。
- Application **八个场景**确认原会话创建后出现冲突时预览拒绝，安装登记仍能读取，Scope/快照/generation/原操作保持，helper/凭据值不传出。
- 相关守卫与创建校验共 **63 个叶场景通过**，包含上述新增场景及已有有效预览、错误结果、取消、清理隔离与校验进程取消检查。只读认证和来源版本回归 **49 个叶场景通过**。
- 未修改的 Web 使用实际 Application 拒绝结果回放，**10 项浏览器检查通过**：具体字段及原路径可见、无有效值或候选生成声明、仍能读取登记、不传出 helper/凭据值、没有执行命令。浏览器层是回放，不称现场原生验收。
- `nativeclaude` tagged vet、开发构建通过；本轮没有把上片真实原生/MCP/插件成功计作当前二进制重跑。独立[审计](W02-08-claude-preview-startup-guard/audit.json) **25 项通过**。

这里的红灯证明“当前冲突仍启动验证进程”，传输 worker 为受控实现；它不证明本轮已实际触发真实 CLI 的 helper。完整原生行为仍需可用环境验证。

## 版本和原始依据

- 开发二进制：`13d77c2b6839602f8e3d3d9a4fc73893b8b250a93061a501ad04225208cd3475`。
- 旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。
- 7,861 个旧 W 证据和 31 个旧 goals 证据未变，上片 249 项 manifest 原样通过；桥接与 Web 源码/构建、go.mod/go.sum 未变。
- Context7 请求的是官方 Claude Code `v2.1.278` 文档索引；返回的 bare 说明来自[官方 CHANGELOG](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md)，该链接指向 main，不能冒充固定 2.1.280 原始运行结果。查询记录见[文档摘录](W02-08-claude-preview-startup-guard/docs-context7.json)。本地固定 SDK `0.3.280` 的 `resolveSettings`、`policyHelper` 声明与文件 SHA 另见[SDK 契约](W02-08-claude-preview-startup-guard/sdk-contract.json)。

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime ./internal/app -run '^Test(ClaudePluginSettingsPreview|ClaudeCreateSettingsValidation|InstalledPluginSettingsPreview)' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime ./internal/app -run '^TestClaude(LocalSettings|LocalIdentity|NativeRead)' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./cmd/... ./internal/... ./web
GOCACHE=/tmp/netcap-w0206-auth-go-cache go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
```

设置 `NETCAP_ADMISSION_EVIDENCE_ROOT` 为自有工作目录可保存 Application 拒绝样本。浏览器脚本为 `tests/claude_plugin_settings_conflict_browser.js`，使用该样本和当前静态 assets。

工作目录 `.dev/w0208-startup-policy-admission-work`。归档包含[红灯](W02-08-claude-preview-startup-guard/runtime-red.log)、[守卫测试](W02-08-claude-preview-startup-guard/guard-green.log)、[读取回归](W02-08-claude-preview-startup-guard/read-regression.log)、[浏览器](W02-08-claude-preview-startup-guard/browser.json)、[源码差异](W02-08-claude-preview-startup-guard/source.diff)、[索引](W02-08-claude-preview-startup-guard/archive-index.json)及[manifest](W02-08-claude-preview-startup-guard/manifest.json)。不包含完整 profile、数据库或真实凭据。

## 剩余条件

此守卫只补齐已知的本地设置冲突检查。受管/远程策略的真实性与新鲜度、其他 helper 和动态账号来源、完整有效权限、正式已安装插件选配及原 ID 持久应用仍未完成。可用环境恢复后须按当前产物验证真实原生正常/冲突路径；不能由本轮 Go 传输检查或上片历史通过替代。

下一项继续完整启动准入的来源与执行边界；IPC 不可用期间处理其独立契约和必要检查。四配置、真实方法/Gate、严格前台、共享后台/输入独占与 Windows 全部硬要求保持。没有提交、推送或发布。
