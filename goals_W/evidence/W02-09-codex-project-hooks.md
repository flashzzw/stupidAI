# W02.09 · Codex 项目 hook 来源版本与 worktree

2026-10-06。完成来源保护切片，W02.09 保持 `in_progress`，总进度 9/40 done、Goal active。固定 Codex app-server 0.158.0 / CLI 0.157.1；隔离 profile、临时 Git 仓库/worktree、生产 Application/Runtime/SQLite/Handler。用户重复提供的 Claude `bp2rp6xu` 已验收，不重跑；五项既有 WSL 请求继续待结果。

## 用户可见结果

新普通 Codex 执行保存原生 `config/read(includeLayers:true)` 项目配置层所声明的 `.codex/hooks.json` 与 `hooks/list` 实际发现的项目文件的并集。记录内容摘要、权限、解析路径、存在状态、配置层版本/启用状态以及两类来源标志；快照、Scope、候选及改配指纹采用同一绑定，旧记录保持缺字段，不补造历史。

文件漂移或原生来源集合/配置层变化撤下观察的输入就绪，派发前拒绝输入、回复、改配和恢复。保留中止出口。原文件及来源集合恢复后，可明确恢复原 ID 和历史，插件候选不能顺带升级继承文件。新建前无法取得完整元数据、发现记录对应文件缺失、原生警告/错误、未知 hook 来源类别或格式异常均拒绝采用部分来源。

详情新增“Codex 项目 hook 文件版本”，区分配置层声明、原生发现、未启用层、缺文件和未发现来源。离线来源检查只核对已记录文件，不能发现此前缺席的新目录；原生派发/观察会再次复核集合。已被应用接收的异步请求可能最终为 `not_sent`，不等于消息已发送给模型。

## 原生根目录事实与原始失败

[原始目录及归档映射](W02-09-codex-project-hooks/original-directories.json)、[原始文件摘要](W02-09-codex-project-hooks/raw-sha256.json)保留探索及失败样本。早期 `roots-*` 探索输出没有断言，退出 0 不计作验收。最终入口有明确 `checks` 和退出码。

| 条件 | 固定版本实际报告 |
|---|---|
| 无 Git 根 | cwd 的 `.codex/hooks.json` |
| 普通 Git / 嵌套 Git | 相应 Git 根至 cwd；嵌套根截断外层 |
| 自定义 `project_root_markers` | 使用标记确定的范围 |
| 项目未信任 | 配置层仍报告，带 `disabledReason`；项目 hook 清单省略 |
| `.codex` 不存在 | 配置层缺席；目录出现后必须复核集合 |
| `.codex` 存在、hooks.json 缺失 | 配置层仍存在；同进程新增文件后清单更新 |
| Git worktree | 配置层指向工作树；hook 清单可指向主仓库根文件 |
| 工作树根 `.codex` 缺席 | 本样本即使信任主仓库，仍不发现主仓库 hook；恢复该目录后发现，反向查询顺序不改变结论 |

因此不按 cwd 祖先扫描推断完整来源，也不将两种 API 强行解释成同一路径。来源集合依据原生结果，工作树条件变化由下一次核对检测；此处只报告样本，不声称解释了全部原生实现。

原始项目文件红灯 `/tmp/netcap-w0209-codex-hook-sources-1648610567`：修改文件后 `ExecutionClear` 仍为 true。修复后原生入口通过。worktree 初次夹具缺根 `.codex`，实际清单为空；仅增信任仍为空，补齐根目录后发现。新增目录的早期测试误要求同步错误、等待错误终态 `failed`，实际已被 Runtime 拒绝为 `not_sent`；修正断言并核对原生仅有一轮种子输入，没有放宽生产规则。

## 验证

| 层次 | 实际结果 |
|---|---|
| 原生根目录/信任/工作树/目录缺失探针 | 10 阶段、61 项通过；仅元数据，无线程/输入/hook 执行 |
| 生产来源漂移及原 ID 冷恢复 | 用户 20、普通项目 23、worktree 24、新增目录 23，共 90 项通过 |
| 既有混合资源及丢回执回归 | 90 项通过 |
| 已确认恢复跨重启回归 | 108 项通过 |
| 无插件普通创建与丢回执恢复 | 8 组合 / 152 项通过，未发送模型轮次 |
| 既有插件改配 | 63 项通过 |
| App/Console race | 通过；覆盖持久化、缺失、旧记录、绑定损坏、候选保留、reader 失败 |
| Runtime 相关 race | 通过；覆盖两类路径并集、不完整元数据、读期间变化、文件/集合/层变化与派发拒绝 |
| 带 nativecodex 标签的 internal/cmd vet | 通过 |
| Web 类型检查、构建、开发 Go 构建 | `make dev-build` 通过 |
| 浏览器 | 19 项通过；真实 Handler 回放及明确标注的旧记录/缺失/禁用/空集合展示夹具，GET only |
| 独立证据核对 | 133 项通过 |

四类来源测试均只发送一轮指向不可用 loopback 供应商的种子输入并明确中止，以形成真实可恢复历史。漂移时真实线程未加载；恢复来源后明确 resume，核对原生 loaded list 和原 ID/历史。没有成功模型、工具或 hook 执行声明。浏览器回放使用该片 worktree 成功样本；[桌面截图](W02-09-codex-project-hooks/initial-1440.png)、[390px 详情截图](W02-09-codex-project-hooks/drift-390.png)。早期桌面截图未滚到来源区域，保留为 first 样本，最终已重新定位。

扩大检查的两项限制单独保留：全 Runtime 测试进入 OpenCode `httptest` listener 时因 EPERM 失败，App/Console 当次通过；仓库 `go vet ./...` 会编译历史 evidence 内不完整源码片段而失败，实际产品包 `./internal/... ./cmd/...` 通过。未修改旧证据来消除这些失败。旧原生创建/插件改配测试包装器最初未转发新增的来源 reader，创建持久化故障样本报接口缺失；直接 runtime 路径通过。补齐测试包装器的真实 reader 转发，重跑普通创建与插件改配通过，生产准入未放宽。首次独立审计把混合资源入口误当作 108 项持久恢复入口，保留失败并分别验证两个入口。

可重跑命令（本地临时资源；不提交用户仓库）：

```bash
python3 tests/codex_project_hook_sources_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run 'Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/... ./cmd/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags nativecodex ./internal/runtime -run '^TestCodexNative(Inherited|Project|Worktree|NewProject)HookSourceDrift$' -count=1 -v
```

混合资源回归同环境使用 `^TestCodexNativeCombinedResourcesAndReceiptLoss$`，持久恢复使用 `^TestCodexNativeOriginalConfigurationRecoveryAcknowledgedRestart$`。浏览器入口是 `tests/codex_project_hooks_browser.js`，填入真实 `browser-responses.json` 与开发构建路径，经配置的 Playwright MCP 回放；没有监听生产端口或发送模型请求。

## 产物与限制

最终开发二进制 SHA-256：`5ed0b78f9e5766b876d3223dc5bba4cf4c9ef126d1a8402bdd2c6d2443494216`。旧 `bin/netcap`：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。最终源码/构建/归档摘要见 [manifest](W02-09-codex-project-hooks/manifest.json)，差异见 [source.diff](W02-09-codex-project-hooks/source.diff)，检查结果见 [command-results](W02-09-codex-project-hooks/command-results.json)。保持 4686 个旧证据和 Claude bridge，不提交/推送；只关闭自有执行器与浏览器。

仍未覆盖全部有效配置、受管 hook 文件、引用脚本、显式采纳新来源版本、文件冻结、实际权限冲突和四配置联合验收。模型/方法/Gate、成功/通用/异步 hook、完整安装来源、共享后台/生产 serve/proxy、输入独占、Windows 与五项既有 WSL 请求继续保留。下一片沿原事务核对明确采纳新版本的原生条件，先复现原生加载/恢复行为；不只改摘要来宣称新配置生效。
