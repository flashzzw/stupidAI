# W02.07 · Claude 用户与项目 Skill 目录发现

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 用户可见行为

“执行配置快照 → Claude Skill 来源清单”读取后，可展开“用户与项目目录入口”，查看当前保存配置下的入口路径、解析路径、声明名称、描述和原生目录键。同名、同目录键或指向同一文件的入口提示歧义；损坏链接、无效元数据和读取上限显示具体问题。原生清单与目录采样分别展示，不按同名自动关联，也不把磁盘存在解释为启用、加载、使用或方法 Gate 通过。

原生条目的 `bound_path` 仍只来自已保存选择。另有已保存原生目录选择时，目录采样携带原快照的版本、模式和入口是否发现；目录缺失不会删掉选择，也不会从最新能力库替换旧版本。保存版本/缺失路径投影已做状态测试，本片不把它当成真实方法历史验收。

## 实现与范围

复用 `POST /api/sessions/{id}/skills/catalog` 的空对象请求、鉴权、当前已开启查询与保存绑定。Runtime 的原生响应仍为 `claude.sdk.current_query`、`complete=false`，不增加文件路径推断。Application 返回附加的 `directories`，其来源固定为 `claude.bound_directories`；目录 I/O 在状态锁外执行，返回前重新核对会话 generation、Scope、登记实例及状态归属，不写快照或输入授权。

目录枚举与已有 Claude 原生 Skill 绑定共用 roots：保存 profile 的 `skills/`，以及工作目录向上各层 `.claude/skills/`，最多 64 层。最多检查 2048 个目录条目，入口限制为普通文件、256 KiB，整次入口读取按 4 MiB 预算控制。只输出元数据，正文和解析输入不进入结果；打开前后检查文件身份、大小、时间、模式及路径解析变化。目录链接保留原入口与解析路径，读取中无法确认时返回不完整，不尝试修复来源。

`directories.complete` 仅表示支持范围内此次目录采样完成，不是原生全部来源完整，也不是整个目录树的原子快照或完整资源验证。受管目录、已安装插件、旧 `commands/` 与其他动态入口不在本片扫描范围；它们仍按原生清单实际返回情况展示。当前原生查询/bridge 不可用时，此 API 仍报错，不为补齐目录面板启动新查询。

## 实际验证

| 层次 | 结果 |
|---|---|
| 公共入口反馈 | [初始失败](W02-07-claude-directory-discovery/red.log)：原 API 没有独立的目录入口结果；实现后同一调用通过，不改变 Application 状态或增加模型输入。 |
| 边界与保存版本 | [定向 race](W02-07-claude-directory-discovery/targeted.log)通过。新增 9 类覆盖元数据/目录键、同键、文件别名、损坏链接、无效 YAML、入口大小、条目上限、取消、原快照版本与缺失路径；并回归原生绑定和六类清单身份/输入归属变化。 |
| 真实 Claude | [最终 26 项](W02-07-claude-directory-discovery/native-final/result.json)通过：固定 Claude Code 2.1.280 / SDK 0.3.280，生产 Application/SQLite/Runtime/Session；A/B profile 各自用户路径、共享项目路径、名称与目录键差异、原生与磁盘来源分开、损坏元数据不丢原生清单、别名歧义及身份/操作不变。 |
| 原生回归 | [自然 Skill 选择 9 项](W02-07-claude-directory-discovery/native-selection-regression/result.json)通过，验证共用 roots 后的启停、状态命名空间、零模型消息及新来源冲突仍阻止输入。初步发现 22 项另存，不与最终 26 项累加。 |
| 浏览器 | [32 项](W02-07-claude-directory-discovery/browser.json)通过：当前构建与实际 API 数据回放、两 profile 路径隔离、部分错误/别名、未推断启用、无正文、失败/会话切换/断连清空、空请求与无执行命令。受管/未知类别仍是原有契约夹具。桌面与窄屏截图已查看。 |
| 独立审计 | [14 项](W02-07-claude-directory-discovery/audit.json)从实际响应复核 ID/Cwd、原生非完整覆盖、独立目录来源、准确路径/目录键、不返回 enabled/verified 声明和正文；另核对两个 bridge journal 均无 input。 |
| 工程检查 | app/console 全包 race、`go vet -tags=nativeclaude ./internal/app ./internal/runtime ./internal/console`、gofmt、TypeScript 检查与开发构建通过。Runtime 生产代码和 bridge 未修改，不重复无关原生矩阵。 |

最终原始目录 `/tmp/netcap-w0207-claude-app-4096846514`；初步 `/tmp/netcap-w0207-claude-app-3218049388`；自然选择回归 `/tmp/netcap-w0207-claude-app-4293547614`。所有测试命令已结束，Application 正常 Close，浏览器已关闭。使用隔离 profile、项目和本地不可用供应商，无模型输入、工具/hook 执行、共享后台切换、listener 或 Windows 操作。没有重复等待四项尚无新结果的 WSL 请求。

## 复验与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app -run '^(TestSessionClaudeSkillDirectories|TestClaudeSkillDirectoryBoundaries|TestSessionSkillCatalog|TestClaude.*Skill)' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -tags=nativeclaude -race ./internal/app -run '^TestClaudeNativeApplicationSkill(Catalog|Selection)$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

[浏览器脚本](../../tests/claude_skill_directories_browser.js)使用最终 `catalog-browser-responses.json` 替换 `__FIXTURE__`、当前 web/dist URL→本地文件映射替换 `__ASSETS__`，生成 `.dev/w0207-claude-directories/run-browser.js` 后由 Playwright MCP 执行；HTTP 和断连回放不等于浏览器实时原生执行。

- 旧 `bin/netcap` 未改：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。
- 开发 `.dev/bin/netcap`：`f108b73ee6818e5c5116c484bc4c189b77aa61f1172dbc8583ebc384e2e87c3a`。
- JS `index-CFgInIwH.js`：`97ae47039cacb7a2093a9be75327437bbab05833d1881efea46d414fde1b5d9f`。
- CSS 未变：`3781875c9ca317492b8773f0b67767aa507fe40eca497aedb876f6190b6c845a`。

[归档](W02-07-claude-directory-discovery/)、[源码差异](W02-07-claude-directory-discovery/source.diff)、[审计](W02-07-claude-directory-discovery/audit.json)及[清单](W02-07-claude-directory-discovery/manifest.json)保留本片依据。3013 个既有 W 证据未改，没有提交、推送或发布。

本片使用已经验证的 Claude 查询协议，没有新增 SDK 方法；固定 SDK 的 SlashCommand/skillFrontmatter 类型仍未报告完整路径，因此没有把目录路径回填到原生条目中。初始公共入口失败后转绿；后续边界、原生与浏览器检查均通过。

## 后续与硬缺口

继续 W02.07 来源准入、正文/资料的实际方法任务和 Gate，并收取已有 WSL 结果。目录发现不解决完整受管/插件来源及权限控制；原生配置确认丢失后的安全恢复、共享后台/输入独占、成功/异步/通用 hook、Windows/Unix proxy/peer 等既有硬要求保留。此次独立工作没有降低 W02.07/W02.08 或首版的完成标准。
