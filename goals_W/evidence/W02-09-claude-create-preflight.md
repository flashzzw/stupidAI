# W02.08 / W02.09 · Claude 创建前原生设置校验

日期：2026-10-08。W02.07/W02.08/W02.09 仍 **in_progress**，整体 **9/40 done**，Goal active。

## 用户可见结果

普通 Claude 会话创建遇到无效的原生设置格式时，会在真实执行查询和启动 hook 运行前拒绝，持久操作为 `not_sent`，错误只包含文件和字段。例如合法 JSON 中的 `permissions.allow: 42` 不再被原生忽略后继续运行其他来源的 hook。正常配置仍由原查询加载原设置，预检不发送模型输入、不增加原生会话历史。

这次修复只接到普通 `create`。没有更改只读来源入口、探测、发送、恢复或原 ID 改配契约；特别是已记录的改配候选启动副作用限制仍存在。已安装插件选择本身尚未开放。

## 原始缺陷与原生边界

`internal/app/claude_create_preflight_native_test.go` 通过真实 Application/Runtime/固定 SDK 创建五份隔离配置。原始红灯中，user/project/custom 三类无效权限格式均创建成功，至少另一个来源的启动 hook 已执行；两个正常场景通过。接入预检后，三类错误均在执行查询前拒绝，两个正常场景的 user/project hook 各执行一次，五例共 **25 项**通过。

在此之前，直接原生进程对照了普通模式、环境变量 simple 模式、派生文件、坏 schema 以及设置覆盖环境变量的情况。原始两组共四例、六例，均已结束。关键记录：

| 启动方式 | 来源与格式读取 | 启动 hook / MCP |
|---|---|---|
| 普通模式 | user/project/local/flag 四层可读 | 五个来源 hook 均运行，安装插件 MCP 启动 |
| 仅 `CLAUDE_CODE_SIMPLE=1` | 四层可读 | 通常关闭；settings.env 将其设为 `0` 后重新启动 |
| 显式 `--bare`，同样的覆盖设置 | 四层及原权限/env 保留 | 没有所测启动 hook 和 MCP |
| bare + 无效 custom 权限格式 | 原生返回该文件的 `permissions.allow` 错误，跳过该层 | 没有启动 hook 和 MCP |

因此实现使用显式参数。`--bare` 不是文件权限隔离；本例没有发送用户输入，收到权限请求、模型结果、hook 事件或非空 MCP 清单时均拒绝。官方参数依据复用已取得的 [CLI reference](https://code.claude.com/docs/en/cli-reference)，归档其本次使用的原文；实际兼容性以 **Claude Code 2.1.280 / SDK 0.3.280** 的上述实测为准。

## 实现边界

- `internal/runtime/claude.go` 在普通创建接通真实 bridge/query 前调用新校验。先验证既有凭据引用；保持原有 Runtime 锁与关闭同步关系。
- `claude_settings_validation.go` 复用独立来源读取，拒绝读取不完整及已声明的 policy helper；启动单独 worker，核对固定版本、cwd/settings 绑定、结果结构及结束确认，再读取一次来源，忽略读取时间比较证据稳定性。整体期限 40 秒，返回输出限制 1 MiB，取消时终止自有进程组。
- `bridges/claude/src/settings_validation.ts` 使用显式 `--bare --no-session-persistence`，发送 `initialize → get_settings → get_hooks_listing → mcp_status` 四个控制请求。原生单行限制 16 MiB，控制阶段整体期限 12 秒，退出时先 TERM、必要时 KILL 并等待结束。只返回 schema 错误文件/字段与摘要，原生诊断文案和设置值不传出。
- `tests/claude_plugin_contract_probe.py` 仅新增可选原生参数，供 bare 对照使用。没有 Web 源码变更；创建失败沿已有操作记录与 Handler 呈现。

来源读取的 `complete` 仍为 false。两次读取稳定不是原子冻结，schema 合法也不代表有效权限完整或所有受管/远端来源已准入。此片不能作为已安装插件排除、原 ID 改配或动态策略控制的成功证明。

## 实际验证

| 层次 | 结果 |
|---|---|
| 真实创建 | 五例25项；无效配置无原生 ID、查询 journal、启动 hook 和 transcript；正常创建只有原查询及两个成功 hook |
| Node 传输 | 11项通过；覆盖错误 schema、非 bare、意外 MCP/权限/hook/模型结果、超限、版本错误及忽略 TERM 后强制退出；包含独立 `hook_progress` 事件 |
| Runtime 边界 | 14类返回/来源边界及自有进程组取消通过；取消用受控 worker/子进程，不冒充真实 CLI 故障验收 |
| 既有来源读取 | 11类通过 |
| 新构建的生产 MCP | 39项通过：A/B 并发、绑定与真实结果回到各自供应商、逐次审批、禁用、来源漂移和原 ID 恢复 |
| 新构建的生产插件 | 33项通过：真实 MCP、Pre/Post hook 载荷、失败 hook 阻止工具、排除及原 ID 恢复 |
| 独立原始记录核对 | 新审计72项；复用插件审计53项；均从原生 wire/journal、标记文件、供应商请求、Handler 和只读 SQLite 取证 |
| 常规检查 | runtime/app/console 全包 race、nativeclaude+nativecodex 标签的产品包 vet、bridge 构建、`make dev-build`（含 Web typecheck/build）通过 |
| 浏览器 / 真实供应商 | 本片未执行；无界面改动，使用真实 Handler 验证错误可见；模型协议只用本地模拟供应商 |

初始编译失败是测试包导入环，修正为 `app_test` 后才取得有效红灯。首次绿尝试误要求无输入查询一定留下 transcript，调整为只允许原执行 ID 的历史后通过。Node 初次 6/7 通过，非 bare 已拒绝但测试过度约束错误文案；改为验证拒绝且不泄露值，补全清理边界后10项通过。最终检查另复现独立 `hook_progress` 事件被忽略，先取得11项中的一个真实红灯，再补充拒绝条件，最终11项通过。变更后的 worker 重新通过真实创建25项、MCP39项和插件33项；先前 worker 的源码、hash与结果另行保留。

首次独立审计有三个断言失败：启动 hook 的 `operation` 本来为空，应按原生 `session_id` 与成对 `hook_id` 核对；MCP A 只恢复控制/历史，没有再次发送，因此只有一次真实查询初始化。保留原失败记录；没有通过修改产品或放宽无额外 hook/历史条件来消除失败。最终审计确认 B/disabled 在恢复后发送才启动第二个原查询。

## 产物与复核

归档：[审计](W02-09-claude-create-preflight/audit.json)、[插件审计](W02-09-claude-create-preflight/plugin-audit/native-audit.json)、[源码差异](W02-09-claude-create-preflight/source.diff)、[命令结果](W02-09-claude-create-preflight/command-results.json)、[归档索引](W02-09-claude-create-preflight/archive-index.json)、[manifest](W02-09-claude-create-preflight/manifest.json)。原始 `/tmp` 的必要材料按索引压缩保存，未复制完整 profile、凭据文件或 SQLite；原命令和初始失败均保留。

本次开发二进制 SHA-256：`ad915e94e311896dd2f69157550e0dd353f8daf202b75b06fe45e520bf7268b3`。该标识在两项生产回归启动前采集；最终 bridge worker SHA-256：`e93bad5aa6848fa3fc8502b725ccf1ada4143bbf7006a31fbd0e3c287864b350`。旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。基线7767个文件中6946个旧证据全部不变，生产修改限创建接入及新 worker；自有进程与本地服务均已结束，未提交或推送。

```bash
npm --prefix bridges/claude run build
node --test tests/claude_settings_validation_test.mjs
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags nativeclaude ./internal/app -run '^TestClaudeNativeCreateRejectsInvalidSettingsBeforeStartup$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude,nativecodex ./cmd/netcap ./internal/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 tests/claude_mcp_selection_smoke.py --netcap /home/zzw/codex-netcap/.dev/bin/netcap
python3 tests/claude_plugin_execution_smoke.py --netcap /home/zzw/codex-netcap/.dev/bin/netcap
```

用户此前提供的 `/tmp/netcap-console-bp2rp6xu` 已不存在；其33项原生和53项独立核对已在 [W02.08 插件执行](W02-08-plugin-execution.md) 持久归档。本片另以新构建复验，没有伪称重新读取旧目录。

下一片连接完整已安装键、实际来源版本与私有派生 settings 生命周期，复用已有原 ID 事务；完整受管/remote/helper、动态权限、四配置联合、真实方法/Gate、独立前台/输入独占、Windows 第二环境及全部未满足硬要求保留。
