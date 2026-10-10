# W02.09 · 配置来源版本绑定与漂移准入

日期：2026-10-06。完成 Codex 磁盘合并来源记录从只读清单进入执行快照/Scope/原生操作的纵切。W02.09 仍 in_progress，W 系列 9/40 done；完整有效配置、受管权限和四配置联合验收没有整体完成。

## 实现与边界

新普通 Codex 执行在原生创建前保存 `codex_configuration_sources`，记录 profile/cwd、磁盘合并值与字段来源摘要、来源层完整摘要和顺序、各层原生版本及文件字节/权限/存在状态/解析路径。来源正文和任意配置键不进入记录。快照、Scope、准备/应用 Intent、晋升及恢复使用同一来源版本；历史快照原样保留，旧记录缺字段不会补造。

本地 `ReadExecution` 核对保存的文件。Runtime 在发送、回复、准备/应用和恢复前核对来源文件与原生合并值/来源层/字段来源，并在观察时撤下漂移后的就绪状态。原生自动恢复也必须先通过检查；漂移不会自动升级快照。原文件还原后可以按原 ID 重新核对与恢复。原执行中止不因来源漂移失效。

hook 文件采纳会因 `.codex` 目录出现或消失而增加/移除空配置层。此类变化只有在合并值、字段来源、其他配置层及其相对顺序都不变，增减层的配置确为空且配置文件不存在，并能关联到本次 hook 项目来源时才允许附带采纳。逐层完整摘要同时保留不展示的元数据，不能借新增空层采纳其他层变化。普通配置文件/合并来源的新版本采纳尚未接通。

`configRequirements/read` 的权限要求沿用原生权限与 hook 收紧/撤去契约，没有被本轮固定为文件版本；来源采样失败仍拒绝不完整结果。该区分保留策略收紧后明确撤去本地 hook 的合法路径，不把来源一致当作完整权限或线程配置证明。

界面新增保存的配置来源版本与历史展示，当前磁盘来源可对照原快照；文件漂移时保留原文件权限与版本，显示具体变化路径。目录来源一致不代替实际线程加载、输入权、方法使用或 Gate 结果。

## 复现与验证

首先在真实 `TestCodexNativeConfigurationCatalog` 中加入项目文件排版/权限漂移检查：旧实现返回未变化，原生红灯已保留；实现后同入口 **25 项通过**。新的冷重开测试 **12 项通过**：一次明确取消的不可用本地供应商输入建立真实历史，项目配置权限变化后本地检查与输入拒绝，真实重启后原线程未加载，恢复原文件权限后仍按原 ID、原快照和历史加载。仅此测试的一次输入不代表成功模型任务。

| 验证 | 实际结果 |
|---|---|
| Runtime 新准入与采纳边界 | 20 个场景通过：文件内容/权限/缺失、合并值、新层、坏版本/目录、旧记录，以及空层增减、顺序和无关变化；策略收紧继续交由原权限流程。 |
| Application 持久状态 | 4 场景通过：新快照重开、旧记录缺字段、Scope 不匹配和无效版本。 |
| 原生 hook 采纳回归 | 用户/项目/新增项目目录/丢回执恢复共 43 项通过；worktree 12 项通过。 |
| 原生混合与冷恢复 | 准备后重启 26 项、最终加载丢回执后的原配置恢复 46 项通过。 |
| 原生受管策略收紧 | 41 项通过。仍为真实 stdio + 测试边界注入更严格 policy 响应，不代表真实 MDM 宿主策略下发。 |
| 浏览器 | 来源绑定/漂移/冷重开/还原 29 项，当前清单与快照对照 26 项通过；390px 无溢出，漂移后发送按钮禁用。 |
| 回归与构建 | app/console 全包 race、相关 Runtime race（排除已知 OpenCode 监听条件）、新场景、最终 tagged 产品包 vet、TS/Vite 与开发构建通过。 |

受管策略回归第一次在三条撤去路径完成后触及原有 60 秒 fixture 期限，后续请求报管道已关闭；原始 43 MiB 轨迹保留。计数断言反复编码完整配置/历史轨迹，改为在相同锁保护下只读取匹配请求与参数后，保持原期限、断言及完整归档轨迹，最终约 41 秒通过。移除旧计数代码后的首次编译还发现一个未使用 import，已移除并保留失败日志。

## 复现入口

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime \
  -run '^Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings|MCP)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/... ./cmd/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^TestCodexNativeConfiguration(Catalog|BindingColdDrift)$|^TestCodexNativeHookSourceAdoption$|^TestCodexNativeHookSourceAdoptionWorktree$|^TestCodexNativeHookSourceAdoptionMixedColdPrepared$|^TestCodexNativeColdPolicyRecovery$/final|^TestCodexNativeManagedHookDeactivation$' -count=1 -v
```

浏览器入口：[configuration_binding_browser.js](../../tests/configuration_binding_browser.js) 与 [configuration_catalog_browser.js](../../tests/configuration_catalog_browser.js)。分别填入绑定测试/清单测试的真实 Handler 结果与当前 `web/dist` URL 到文件映射，通过已配置 Playwright MCP 回放；截图目录 `.dev/w0209-config-binding`。没有新增监听服务。

工作目录 `/tmp/netcap-w0209-config-binding-work`；主要绑定目录 `/tmp/netcap-w0209-config-binding-2383127367`、红灯 `/tmp/netcap-w0209-config-catalog-1553674263`、修复清单 `/tmp/netcap-w0209-config-catalog-4003430736`。全部原始目录见[索引](W02-09-config-binding/original-directories.json)。[源码差异](W02-09-config-binding/source.diff)、[浏览器绑定结果](W02-09-config-binding/browser-binding.json)、[快照对照结果](W02-09-config-binding/browser-catalog.json)、[窄屏截图](W02-09-config-binding/reopened-390.png)、[独立产物核对](W02-09-config-binding/audit.json)与[最终哈希](W02-09-config-binding/manifest.json)随片归档。

5585 个旧证据逐文件哈希不变；旧 `bin/netcap` 与 Claude bridge 源码不变。44 个自有原生进程已退出，其中一个因上述原期限结束，其他正常退出；浏览器已关闭。没有提交、推送或调用真实供应商。

## 后续

继续核对一般配置来源新版本的明确采纳及原 ID 生效边界，再完成更多来源/权限冲突与四配置联合。实际 MDM/企业下发、完整受管权限、来源外脚本和运行依赖、动态账号能力、真实模型方法/Gate、成功/通用/异步 hook、W02.07/08、存活共享后台/输入独占、Windows 和五项待返回 WSL 结果仍保留。
