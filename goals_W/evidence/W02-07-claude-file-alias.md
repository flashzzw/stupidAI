# W02.07 · Claude Skill 文件别名准入与输入检查

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 用户可见行为与原因

同一个 `SKILL.md` 通过其他原生目录键的符号链接或硬链接出现时，工作台拒绝独立选配。外部 Skill 出现任何同文件原生入口时也拒绝，包括本次禁用。运行中的来源变化会撤下已核对状态并阻止输入；原 ID、快照、资源版本和启停模式保持。移除冲突后重新核对，仍使用原执行；关闭查询后也能按原 ID 重新打开。

旧 Application 只比较符号链接解析路径，硬链接因路径不同而漏检；bridge 对原生选择只检查同目录键，外部选择只比较 realpath。实际原生红轮中，natural/external × enabled/disabled 四例均在新增硬链接后保持库存可用，并错误接受一次输入。均指向不可用本地供应商，没有成功模型结果。

Application 新增 `os.Stat` / `os.SameFile` 文件身份检查；同文件但不同真实路径的入口不包装为外部资源，也不自动替换相对资料所在目录。bridge 每次来源核对扫描既有 roots，以 bigint `dev/ino` 比较文件身份，同时保留目录键、准确路径和资源包核对。原生选择要求唯一入口，外部选择要求没有同文件原生入口；读取异常返回稳定的未发送错误。打开、清单、准备改配、发送与权限回复沿用已有调用链。

内容相同的独立副本不属于文件别名；新增正控制确认来源分类不变，原生命令名称冲突仍独立拒绝。文件身份检查不按内容摘要合并资产，也不扩大文件权限。

## 实际验证

| 层次 | 实际结果 |
|---|---|
| 创建准入红→绿 | [原失败](W02-07-claude-file-alias/app-red.log)的 native-hardlink/external-hardlink 均误准入；修复后[定向 race](W02-07-claude-file-alias/app-targeted.log)通过。普通副本两种来源对照随后随 app 全包 race 通过。 |
| bridge 契约 | [选择 9 项](W02-07-claude-file-alias/bridge-selection-final.log)、[资源 22 项](W02-07-claude-file-alias/bridge-resources-control.log)、[改配 6 项](W02-07-claude-file-alias/bridge-change.log)、[插件 17 项](W02-07-claude-file-alias/bridge-plugin-final.log)通过；包含不同键 symlink/hardlink、外部启停及独立副本。 |
| 真实 Session | [四例各 10 项](W02-07-claude-file-alias/native-final/result.json)通过：初始就绪、新增别名后撤下清单/阻止 prepare/send、配置和 ID 不变、移除后恢复；别名存在时原 ID reopen 拒绝，修复后同 ID 可打开。最终绿色流程零模型输入/结果。 |
| 真实 Application | [14 项](W02-07-claude-file-alias/application-final/result.json)通过：两 profile、自然选择启用/禁用、unknown 与 SkillEvidence 撤下、公共 send 拒绝、原 ID/快照/模式保留，修复后 idle；SQLite/Application/Runtime/真实 Session 串联。API 数据从实际 Application 读取，没有真实 HTTP listener。 |
| 原生回归 | 自然选择 [9 项](W02-07-claude-file-alias/selection-regression/result.json)、改配 [37 项](W02-07-claude-file-alias/change-regression/result.json)、目录发现 [26 项](W02-07-claude-file-alias/catalog-regression/result.json)通过。 |
| 浏览器 | [33 项](W02-07-claude-file-alias/browser.json)通过；当前构建回放真实 Application 三阶段数据，核对两会话原 ID/入口/模式、核对状态与发送按钮、未冒称方法/Gate。没有提交命令；[窄屏](W02-07-claude-file-alias/blocked-390.png)与[修复后桌面](W02-07-claude-file-alias/repaired-1440.png)已查看。 |
| 独立审计 | [27 项](W02-07-claude-file-alias/audit.json)从归档快照和 journal 重新计算，未依赖生产脚本的 pass 字段。确认四个旧实现案例各新增一个 input，最终绿色数据无 input/result，两个 Application journal 以 closed 结束。 |
| 工程检查 | app/console 全包 race、Claude/Execution/NativeObservation/Skill Runtime 定向 race、nativeclaude tagged vet、Python 语法、bridge TypeScript 构建、Web 类型检查与开发构建通过。未重复已知受监听限制的 OpenCode 全包测试。 |

## 失败与夹具修正

- [bridge 原选择失败](W02-07-claude-file-alias/bridge-selection-red.log)直接捕获新增不同键 symlink/hardlink 未拒绝。早期 `node --test` 聚合入口只有泛化失败，单独保留 [bridge-red.log](W02-07-claude-file-alias/bridge-red.log)，不将其解释为 IPC 根因。
- [外部资源原失败](W02-07-claude-file-alias/bridge-resources-red.log)中 disabled 是真实漏检；enabled 最初漏做包准备，不能将其 ENOENT 当作产品别名证据。补齐 `prepareSkillResources` 后最终通过；[夹具失败](W02-07-claude-file-alias/resource-fixture-not-prepared.log)保留。
- [初步真实运行](W02-07-claude-file-alias/native-partial-red/result.json)的 natural 子进程 profile 绑定不正确，external 两例已复现。修正隔离环境变量后，[完整原生红轮](W02-07-claude-file-alias/native-red/result.json)四例均先就绪再暴露漏检；它是本片真实复现依据。
- 插件回归首次命令漏 `--experimental-test-module-mocks`，报 `mock.module is not a function`，见 [入口错误](W02-07-claude-file-alias/bridge-plugin.log)；按现有项目命令重跑 17 项通过，没有为此修改生产实现。

## 复验与版本

```bash
make claude-build
node tests/claude_skill_selection_test.mjs
node tests/claude_skill_resources_test.mjs
node tests/claude_skill_change_test.mjs
node --experimental-test-module-mocks tests/claude_plugin_selection_test.mjs
python3 tests/claude_skill_file_alias_native.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -tags=nativeclaude -race ./internal/app -run '^TestClaudeNativeApplicationSkill(FileAlias|Selection|Changes|Catalog)$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestClaude|TestExecution|TestNativeObservation|TestSkill)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativeclaude ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 goals_W/evidence/W02-07-claude-file-alias/audit.py
```

固定 Claude Code **2.1.280**、SDK **0.3.280**、Node **24.13.0**。复用已验证的 Session/SDK 协议，没有新增 SDK 方法。浏览器脚本 [claude_skill_file_alias_browser.js](../../tests/claude_skill_file_alias_browser.js)以最终 `file-alias-browser-responses.json` 替换 `__FIXTURE__`，当前 Web 文件映射替换 `__ASSETS__`，生成 `.dev/w0207-claude-file-alias/run-browser.js` 供 Playwright MCP 执行。

- 最终 Session 原始目录：`/tmp/netcap-w0207-file-alias-wnfwe8kp`。
- 最终 Application 原始目录：`/tmp/netcap-w0207-claude-app-3299283738`；其他原始目录见[清单](W02-07-claude-file-alias/original-directories.json)。
- 开发 `.dev/bin/netcap`：`7dadabec610c43296d7c0f0cf0bfcf800871bbb5403a718c64afe8b4ead4471c`。
- bridge `dist/skills.js`：`9b2e0b585bb1b3d9cd4f1109395f0ae30b416aa9dbad6d1c39e15d2d646642c2`。
- 旧 `bin/netcap` 未改：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`；Web 内容未改。

[源码差异](W02-07-claude-file-alias/source.diff)、[产物清单](W02-07-claude-file-alias/manifest.json)与[完整性核对](W02-07-claude-file-alias/integrity.json)记录收尾时点。3045 个既有 W 证据未改；只归档白名单结果与测试日志，没有复制 profile 凭据或原生数据库。所有执行命令已结束、查询正常关闭、浏览器已关闭。没有停止用户后台、提交、推送、发布或操作 Windows。

## 范围与下一步

扫描范围仍是保存 profile 的 `skills/` 和项目祖先 `.claude/skills/`，最多 64 层、2048 个条目；这是输入前的有界采样，不是文件树的原子冻结。受管、已安装插件、旧 commands 或额外动态 roots 未因此成为完整可控来源。界面显示待核对；具体别名拒绝由准备/打开/输入边界返回，本片未新增完整诊断面板。

继续来源准入及实际方法任务/Gate。真正丢失原生配置确认后的安全恢复、共享后台/输入独占、完整受管/插件来源、成功/async/通用 hook、Windows/proxy/peer 及四个既有 WSL 请求仍未完成。用户重复提供的插件执行目录已在前片核对为原有 33 项，不重复算新增验收；本片不关闭 W02.07/W02.08 或整条 Goal。
