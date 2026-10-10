# W02.08 · 通用插件 hook 的固定版本发现限制

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 结论与产品边界

固定 Codex app-server **0.158.0** 能识别通用插件的身份与 Skill，但本片三种文件 hook 来源均没有进入 `hooks/list` 或 `plugin/read` 的 hook 清单：根目录默认、`extensions.com.openai.hooks` 指定文件、旧 overlay 回退指定文件。显式本地安装、开启或关闭 `plugin_sharing` 均未改变结果；相同进程中的旧格式控制包正常返回 hook。原生没有提供错误或警告，不能据此认定完整加载。

同一隔离缓存副本保留 hook 字节、插件 ID 与配置，补齐旧 manifest 后，仅删除根 `plugin.json`，三个包的旧格式 hook 均可发现；恢复根 manifest 后又消失。每步新开独立原生进程，避免将后台缓存当作格式差异。这将故障定位到固定版本的通用入口发现行为；没有原生源码依据，不推断其内部具体缺陷，也不外推所有版本都不支持。

生产继续拒绝通用格式 hook。实验性 schema 放宽和相关原生 flow 重构已完整撤回，生产、Web、bridge、既有测试和两个构建与本片基线一致。只新增公共 Application 拒绝边界回归及可复跑契约探针；未把原生空清单或 stub 通过解释为支持。已验收的旧格式文件 hook 和通用 Skill/MCP 行为保留。删除根 manifest 只用于自有实验副本的对照，未作为产品兼容方案。

## 验证与原始失败

| 层次 | 实际结果 |
|---|---|
| 原生完整选配实验 | root 两次、inline/fallback 各一次均在初始 inspection 失败：`原生未返回完整的已声明 hook；不能用空清单认定准备完成`。没有进入业务线程创建、信任写入、hook 或模型执行。 |
| 最小原生契约 | 两个 sharing 组各四种包，直接缓存、本地安装、补 overlay、移除/恢复根 manifest：58 项限制回归通过；`portable_hooks_verified=false`。要求支持的模式退出 1，默认限制回归模式退出 0，结论相同。 |
| Application 回归 | 三种合法 schema 文件来源均在原生读取前拒绝；伪造 review 的启用/禁用创建也不能调用执行器。新断言先在实验性放宽上三例失败，恢复原边界后通过。 |
| 相关检查 | App 全包 race 通过（88.558s）；Console race 使用已有缓存结果；最终 portable/inspection 定向 race 通过（2.561s）；`nativecodex` 标签 vet 通过。 |
| 独立核对 | 从归档协议、源文件字节、退出状态及原生失败结果重新核对 163 项，通过；不依赖探针报告的布尔值来判定清单。 |
| 浏览器与构建 | 未重跑：最终没有生产/Web/bridge 或原有 native flow 变化，两个二进制保持原摘要。 |

探针只执行 `initialize`、`config/read`、`hooks/list`、`plugin/read` 和本地 fixture 的 `plugin/install`；hook 保持 untrusted。所有 profile、缓存、市场及插件均在本次 `/tmp` 下。没有模型请求、本地供应商监听、用户配置写入、共享后台操作或对外动作。

关键目录：

- 最初探索：`/tmp/netcap-w0208-portable-hook-contract-gc7v5w_m`。该初版只采样、退出 0 不代表通过。
- 支持断言失败：`/tmp/netcap-w0208-portable-hook-contract-typ2ei8c`，退出 1。
- 最终限制回归：`/tmp/netcap-w0208-portable-hook-contract-n_6lq07q`，退出 0。
- 原生选配失败：`/tmp/netcap-w0208-hook-selection-3590628975`、`47565865`、`1108934310`、`249792111`（后三项沿用同一目录前缀）。

足以复核的协议、版本、源文件及失败结果已压缩留在 [证据目录](W02-08-codex-portable-hooks/manifest.json)，原始摘要见 [raw-sha256.json](W02-08-codex-portable-hooks/raw-sha256.json)。`serverName`、`installationId` 和非空 `environmentId` 已脱敏；插件源文件保持原字节。未复制认证文件、SQLite 或完整运行目录。

保留初始 App 拒绝、stub 正例、中间 Go 重构编译失败和全部真实失败日志；这些中间版本由 [实验差异](W02-08-codex-portable-hooks/experimental-source.diff)标明，不属于最终实现。当前差异见 [source.diff](W02-08-codex-portable-hooks/source.diff)。

## 复跑与版本绑定

```bash
# 固定版本的限制回归：期望退出 0，portable_hooks_verified 仍为 false。
python3 tests/codex_portable_hook_contract_probe.py
# 原始支持目标：当前期望退出 1；不能以默认模式的通过代替此断言。
python3 tests/codex_portable_hook_contract_probe.py --require-portable-hooks
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app -run '^(TestCodexPortable|TestCapabilityHookInspection)' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/app ./internal/runtime ./internal/console
python3 goals_W/evidence/W02-08-codex-portable-hooks/audit.py goals_W/evidence/W02-08-codex-portable-hooks
```

沿用[此前官方资料](W02-08-codex-portable-plugins/docs/plugin-docs.txt)中的 Agent Plugins 1.0.0 schema、OpenAI 设置来源与文件 hook 契约，没有重复不可达的外部请求。官方描述与此固定原生观测分别记录。脚本校验原生版本并保存原生二进制 SHA-256。

开发构建维持 `aea69423c9b72d27dc7ac978c45b889c78da4cdd6833b37a13f5b42c4cb09ba4`；旧 `bin/netcap` 维持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。4475 个旧证据文件逐一核对不变；所有测试句柄已收取，原生进程正常退出，未提交。详见[收尾](W02-08-codex-portable-hooks/cleanup.json)和[独立核对](W02-08-codex-portable-hooks/audit.json)。

用户再次提供的 `/tmp/netcap-console-bp2rp6xu` 与[既有 Claude 插件工具轮次证据](W02-08-plugin-execution.md)完全相同，33 项原生结果与归档逐字段一致；已有 53 项独立核对，本片没有冒称重新执行，也没有改写该证据。

## 剩余范围

通用 hook 选配、实际执行/排除、原 ID 改配和冷恢复仍未验收，本片只关闭发现差异的诊断问题。更换原生版本或取得新契约后，须重新运行支持断言及完整生产流，不能只修改允许字段。下一片转向前置已满足的原生组件行为与同步 hook 边界；真实模型/工具/Gate、成功 hook 生命周期、完整受管/安装来源、共享后台/生产 serve/proxy、输入独占、Windows 和五项既有 WSL 请求均保留。
