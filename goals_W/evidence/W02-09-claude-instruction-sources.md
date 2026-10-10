# W02.09 · Claude 当前查询的指令文件来源

2026-10-10。“执行配置快照 → Claude Skill 来源清单”现在单列当前查询报告的指令文件路径、原生来源类别和本地 token 估算。已通过 **8 项原生 Bridge 检查、真实 Application/Handler 双会话与 3 类绑定拒绝、28 项浏览器检查、37 项独立复核**。W 系列仍为 9/40 done。

## 行为与边界

复用 `skillInventory()` 已有的 `getContextUsage({detail: 'summary'})` 调用，仅在读取来源清单时输出 `instructions`；启动、选择、改配所持久化的 Skill inventory 不增加此字段。没有新增模型输入、token-count 请求或启动/恢复查询。

SDK 和 Runtime 分别校验绝对路径、来源类别、非负安全整数 token 数、重复项及上限，只传出允许的元数据字段。缺失、无效与有效空清单分别处理，旧 bridge 不会被解释为“没有指令”。未知原生类别原样保留。Runtime 同时核对原查询的设置阻止机制版本、来源记录与安装绑定；Application 继续核对输入归属和快照。

本地隔离夹具真实报告了五类文件：用户 `CLAUDE.md`、父目录 `CLAUDE.md`、父目录 `.claude/rules/owned.md`、当前项目 `CLAUDE.md` 及其导入的 `reference.md`。报告只包含路径、User/Project 标签与估算，不包含正文。磁盘文件变更不会让工作台读取新正文并冒充原查询加载版本。

此报告没有文件内容摘要或加载版本证明，也不是所有受管、动态或隐藏来源的完整枚举；单项排除、权限覆盖和来源冻结均未据此开放。

## 分层验证

| 层次 | 实际结果与材料 |
|---|---|
| 固定 SDK 契约 | 新增 11 项，加上原有选择/改配共 26/26；[首次红灯](W02-09-claude-instruction-sources/validation/sdk-red.log.gz)、[绿灯](W02-09-claude-instruction-sources/validation/sdk-green.log.gz) |
| 直接原生查询 | 五类路径、原生类别和零模型结果；[原始报告](W02-09-claude-instruction-sources/direct/direct-result.json.gz) |
| 生产 Bridge/Session | 8/8；原 ID/配置/连接/日记不变，无正文，磁盘变化后不输出新正文，关闭查询后读取不重开；[结果](W02-09-claude-instruction-sources/bridge/result.json.gz) |
| Application/SQLite/Handler/Runtime/CLI | 两个真实查询，原生路径透传，无输入或额外操作，设置阻止机制、来源记录与安装绑定三个不匹配场景均拒绝；[测试日志](W02-09-claude-instruction-sources/application/application.stdout.gz)、[真实 HTTP DTO](W02-09-claude-instruction-sources/application/browser-fixture.json.gz) |
| 浏览器 | 28/28；真实 DTO 配合受控 HTTP，另用显式合成数据检查缺失/不可用/空/未知来源；失败、切换会话和断连清除旧清单，只有 8 次既有空请求体读取，没有执行命令；[结果](W02-09-claude-instruction-sources/validation/browser-result.json.gz)、[桌面](W02-09-claude-instruction-sources/validation/native-1440.png)、[390px](W02-09-claude-instruction-sources/validation/native-390.png) |
| 独立复核 | 37/37；从原生配置、日记、只读 SQLite、HTTP DTO、实际浏览器请求与磁盘变更复核身份、范围和无输入；[审计](W02-09-claude-instruction-sources/validation/audit.json.gz) |

`go test -race ./internal/runtime ./internal/app ./internal/console -count=1` 全部通过；`go vet -tags='nativeclaude nativecodex' ./cmd/... ./internal/... ./tests/...` 通过。bridge、Web、nativeclaude 测试程序与 `.dev/bin/netcap` 构建通过，Web 构建先于依赖其内嵌资源的 Go 构建。浏览器截图已经查看，1440px 和 390px 均无横向溢出。

最初的原生探针先被旧 hello 夹具断言阻止：它要求回复只能有两个字段，而生产握手已有兼容能力声明。修复为核对协议版本和固定 SDK 后，原生探针在缺少 `instructions` 时得到预期红灯；两次记录均在 [hello 失败](W02-09-claude-instruction-sources/failure-hello/runner.stderr.gz)与[缺字段失败](W02-09-claude-instruction-sources/failure-metadata/runner.stderr.gz)。后续扩展原生测试时曾把来源记录误写为字符串数组，编译即拒绝；改用现有 `ClaudeSettingsSource` 类型后通过。没有放宽生产校验。

## 版本、隔离与复跑

固定版本：Claude Code **2.1.280**、Agent SDK **0.3.280**、Node **24.13.0**、React **19.3.0**。接口依据为[官方 SDK TypeScript 参考](https://code.claude.com/docs/en/agent-sdk/typescript)以及已安装版本的 `sdk.d.ts`：`memoryFiles` 提供 `path/type/tokens`；summary 使用本地估算，默认 full 会发 token-count 请求。

所有原生测试使用 `/var/tmp` 私有目录、bwrap user/pid/net 隔离、自有 `/etc/claude-code`、受控注册表缺席及本地模拟供应商。查询报告的路径均核对属于本夹具；没有调用真实供应商或用户后台。最后两条日记均只有 `initialized` 和 `closed`，没有输入。旧 `bin/netcap` SHA-256 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。

```bash
npm --prefix bridges/claude run build
node --test tests/claude_instruction_sources_test.mjs tests/claude_skill_selection_test.mjs tests/claude_skill_change_test.mjs
npm --prefix web run build
go test -tags=nativeclaude -c -o /var/tmp/netcap-instruction-sources.test ./internal/runtime
python3 tests/claude_instruction_sources_native.py --direct
python3 tests/claude_instruction_sources_native.py
python3 tests/claude_instruction_sources_native.py --binary /var/tmp/netcap-instruction-sources.test
```

最后一条命令输出私有 Evidence directory，其中 `browser-fixture.json` 可用于 `tests/claude_instruction_sources_browser.js` 的 `__FIXTURE__`；`__ASSETS__` 对应当次 `web/dist` 文件映射，`__OUTPUT__` 为截图目录。浏览器使用受控 HTTP 回放，不作为再次执行原生 CLI 的证据。[源码/构建摘要](W02-09-claude-instruction-sources/manifest.json)与[归档索引](W02-09-claude-instruction-sources/archive-index.json)对应本次实际产物；原始材料按[证据约定](README.md)仅保留在本地，顶层 Markdown 同步 Git。

W02.07/W02.08/W02.09 仍为 `in_progress`；总计 **9 done / 8 in_progress / 23 pending，Goal active**。完整受管/remote/helper 来源真实性与新鲜度、动态权限、外部竞态、四配置联合、真实方法/Gate、六入口与输入权、Windows 及其他首版硬要求仍保留。下一片继续原生来源的新鲜度与动态边界，不将路径展示当作已加载版本绑定。
