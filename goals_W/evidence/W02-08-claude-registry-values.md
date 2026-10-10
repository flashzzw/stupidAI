# W02.08 / W02.09 · 注册表设置正文的读取状态

日期：2026-10-09。W02.07/08/09 保持 **in_progress**，整体 **9/40 done、Goal active**。

## 问题与交付

独立启动来源读取原先只按 reg.exe 的退出码判断 Windows 注册表来源。退出 0 但正文为空、缺少 Settings 值或 JSON 损坏时，固定 SDK 会省略对应受管来源，工作台却仍报告 present 和 read_complete=true。先用生产 inspectStartupSettings、真实 SDK 0.3.280 与受控 OS 回复复现了空输出/损坏正文；扩展后的八类无效输入均出现同一误报。

现在按固定 SDK 的 Settings / REG_SZ / REG_EXPAND_SZ 格式提取正文，要求它是可解析的 JSON 对象。缺少有效值或类型不符记为 invalid_value，损坏 JSON、数组或 null 记为 invalid_json。只有 present/absent 可满足注册表读取完整条件，stderr 与正文均不进入报告。合法空对象仍表示读取成功，策略是否生效由其他契约决定。

界面分别显示“未读到有效的 Settings 值”和“设置 JSON 无效”，保留 HKLM/HKCU 归属。既有 Go 接口会接受这些部分结果、拒绝与 read_complete=true 矛盾的结果；普通创建和设置预览复用已有不完整来源守卫，在原生校验或派生候选前拒绝。生产 Go 代码无需修改。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| 原始复现 | 三例对照中合法正文通过，空输出与损坏正文失败；19 例完整红灯中恰有八类无效注册表输入失败。退出码均为 0，原始回复和 SDK 来源层可独立核对 |
| 固定 SDK / 生产读取器 | **19 例、186 项通过**。含 REG_SZ、大小写不同的多行 REG_EXPAND_SZ、空对象、HKCU 有效来源、八类无效输入及原有文件错误/拒绝/helper 七例 |
| Go 接口与入口守卫 | 相关 race 测试 **82 个叶场景通过**。新增两类元数据的部分结果/矛盾结果，以及创建和预览在原生启动 marker 前拒绝；Go 子进程为受控传输 |
| 浏览器 | **18 项通过**。使用本轮实际 SDK 报告与既有 Application 响应外壳回放，覆盖混合有效/无效 hive、刷新、断连、窄屏及正文不展示；本地文件绑定为空夹具，没有宣称本轮真实 Handler/CLI 验收 |
| 构建与静态检查 | bridge 和 Web 构建（含类型检查）、nativeclaude tagged vet、开发二进制构建通过 |
| 独立核对 | **52 项通过**：从 reg.exe 受控原始回复、SDK 来源层、Go 日志、浏览器请求与基线哈希核对，不只读取 passed 摘要 |

完整红灯包括：损坏 JSON、空成功输出、空 Settings 值、没有 Settings 字段、错误注册表值类型、数组、null，以及 HKLM 有效但 HKCU 损坏。最后一种仍有 SDK hklm 来源，不能用高优先级来源有效掩盖另一项读取失败。

初次浏览器检查已通过，但整块截图在滚动容器中未显示注册表行；收窄截图对象后重跑同一组检查，原图和说明保留。唯一控制台错误是测试明确返回的刷新 409，无 pageerror。最终[窄屏诊断](W02-08-claude-registry-values/registry-values-390.png)与[混合状态](W02-08-claude-registry-values/registry-values-1440.png)均能直接看到 hive 与诊断。

## 复验入口与证据

~~~bash
python3 tests/claude_startup_preflight_probe.py
python3 tests/claude_startup_preflight_probe.py --mode registry-empty-success --mode registry-malformed
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime ./internal/app -run '^Test(ClaudeStartupSettingsReader|ClaudePluginSettingsPreview|ClaudeCreateSettingsValidation|InstalledPluginSettingsPreview)' -count=1 -v
npm --prefix bridges/claude run build
npm --prefix web run build
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./cmd/... ./internal/... ./web
GOCACHE=/tmp/netcap-w0206-auth-go-cache go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap
~~~

探针显式选择受控 WSL 分支，Windows 和 Linux 系统策略文件映射到自有目录；reg.exe 的两个 query 均由夹具回复，没有调用实际 Windows 程序、策略 helper、Claude query 或供应商。继承[上片前置失败](W02-08-claude-preview-startup-guard/prerequisites.json)，没有重复监听检查或要求用户代跑。

- 开发二进制 SHA-256：61bede73660b4bb39eb24f6fc290984d60c86a3c6b9aba488412725d66527fd6。
- 新 settings_preflight.js SHA-256：94ecaec052385d9bcf380fc5696d32b245f8d5c481381875aa5f262235240363。
- 旧 bin/netcap 保持 e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf。
- **7,902** 项旧 W 证据和 **31** 项旧 goals 证据不变；其他 bridge 输出、Go 生产代码与依赖定义不变。自有探针进程退出，浏览器关闭。

工作目录为 .dev/w0208-registry-read-validation-work。持久归档包含[完整红灯](W02-08-claude-registry-values/full-red.log)、[SDK 绿灯](W02-08-claude-registry-values/full-green.log)、[入口测试](W02-08-claude-registry-values/guard-green.log)、[独立审计](W02-08-claude-registry-values/audit.json)、[固定 SDK 契约摘录](W02-08-claude-registry-values/sdk-contract.json)、[源码差异](W02-08-claude-registry-values/source.diff)、[原始文件索引](W02-08-claude-registry-values/archive-index.json)和[manifest](W02-08-claude-registry-values/manifest.json)。其中 registry-replies.json 是测试主动提供的合成正文，不是用户系统策略；未归档凭据、完整 profile 或数据库。

用户再次返回的 /tmp/netcap-console-bp2rp6xu 已对应[既有插件执行 33 项与独立核对 53 项](W02-08-plugin-execution.md)，本轮没有重复计入，也不把它当作当前二进制的原生复验。

## 剩余范围

read_complete 只表示这一轮观察到的来源读取状态；完整启动配置、策略真实性/新鲜度、helper 执行、权限合并及正式已安装插件选配仍未完成。当前权限下没有重跑真实 CLI、本地供应商或实际 Windows 注册表，本片不解除其原生验收缺口。

下一片继续受管来源读取的稳定性与完整启动准入边界，环境恢复后补当前产物原生验收。原 ID 持久事务、四配置、真实方法/Gate、独立前台、共享后台/输入独占及 Windows 全部硬要求继续保留。没有提交、推送或发布。
