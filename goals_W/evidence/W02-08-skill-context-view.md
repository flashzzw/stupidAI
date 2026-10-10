# W02.08 · Codex Skill 上下文与正文载入记录

2026-10-04 实施和原生测试，10-05 完成归档。执行详情新增“Skill 上下文记录”：用户手动读取已绑定原会话的原生日志，按轮次片段展示准备过的描述和正文载入记录。Runtime→Application→Handler→界面接通；来源路径与载入片段摘要可核对，不传出 Skill 正文，不猜插件资产或版本，不把历史记录当作当前配置或方法使用。W02.08 in_progress，整体 9/40 done，Goal active。

## 实现与边界

- `POST /api/sessions/{id}/skills/context` 只接受空 JSON 对象。使用原 Session 的 native ID、cwd、执行配置和状态目录；读取前后检查绑定及代次。结果不写入执行快照、Session/Operation 或输入所有权，不自动读取、恢复线程或改变插件选择。
- Runtime 只调用两次 `thread/read(includeTurns:false)`，从原生元数据取得 rollout 路径并在结束时重新核对。要求已保存 StateNamespace；文件必须位于其 sessions/archived_sessions 下，使用 `os.Root` 限制路径解析、非阻塞打开并检查普通文件。核对原生日志首行 ID、cwd、供应商和固定格式版本 0.158.0，文件/目录身份、大小和修改时间也在读取结束时重新检查。
- 完整文件读取上限 32 MiB、单行 4 MiB、200 个轮次片段；描述字段按输出片段累计上限 4 MiB，另有描述/正文记录数量限制。半行、未知格式、版本不匹配、文件替换和超限都返回安全错误，不返回部分成功，也不在错误中回显原文。
- 合并 `world_state.host_skills` 补丁，在 `turn_context` 边界记录该片段的描述。原生别名根解析为完整路径，只作来源展示，不读取 Skill 文件。缺失清单和明确空清单分别表示。正文记录要求原生 `skills.selected_skill_instructions` 类型标记、已知轮次和有效包装；普通用户文本不能凭 `<skill>` 字样冒充正文载入，明确 client_authored 的载入记录拒绝。
- 正文载入只返回原生记录 ID、名称、路径及**完整载入片段**的 SHA256；该摘要不是原文件版本。旧文件可已变化或不存在，本入口仍保留原历史路径，避免以当前磁盘内容改写历史。
- 界面按最近片段到历史片段展示。读取失败、断连或切换会话清除旧结果；源码名称/描述均按文本渲染。明确提示：改配后尚无新轮次时，最近记录仍可能属于旧配置；正文载入不证明模型已收到、方法已使用或 Gate 通过。

这是只读历史样本，不是每次输入前的能力校验，也不新增自动化权限。内部 rollout 格式的兼容范围只覆盖本次固定版本，其他格式保留未核对。读取失败不改变已有执行状态；操作历史和配置版本仍沿用原记录。浏览器所用 native fixture 直接在隔离原线程发起/取消输入，因此画面中的 Session 状态是 Application 当时保存的观察，并非本接口对当前占用作出的判断。

## 实际验证

| 层次 | 结果 |
|---|---|
| Runtime 公共接口 | 正向记录及 17 个边界场景通过。覆盖历史与当前描述分离、正文不传出、路径/链接越界、错误身份/版本、重复首行、半行、缺少完整基线、未知/越界别名、客户端伪造载入、孤立轮次、读取期间原生身份/文件替换、未知载入格式、用户文本伪装、清单未报告和重复描述导致输出超限。 |
| Application | 8 个场景通过：使用已保存只读绑定，读取不改变快照/所有权/操作；配置、代次、原生 ID、cwd、来源或完整性变化时拒绝。 |
| HTTP | 既有 Handler 测试通过，新增 5 个授权/来源/空请求体/未知会话边界。真实原生测试另经生产 Handler 返回读取结果。 |
| 真实 Codex | [最终结果](W02-08-skill-context-view/final/result.json) **20 项通过**，启用继承插件时准备描述并显式载入正文，原 ID 禁用后新片段无该描述/无新增正文而旧载入保留。Application/SQLite 重开后读取同一历史；重启后台的实际调用仅 initialize 和 thread/read，没有加载线程。三个自有后台均退出 0。 |
| 浏览器 | [本片 20 项](W02-08-skill-context-view/browser/result-context-final.json)通过；真实 Runtime/Application/Handler 响应回放，HTTP/EventSource 受控。原生轮次、历史描述、正文来源/摘要、失败清理、切换/断连、手动读取及移动无溢出均验证。注入的 HTTP 409 产生预期 console 记录，无未捕获页面异常。Codex 插件界面回归 **18 项**、Claude WSL 旧工具/hook 记录回放 **15 项**通过。 |
| 回归/构建 | app/console 全包 race、相关 Codex/Claude MCP/插件 Runtime race、带 nativecodex 标签全包 vet、`make dev-build`（含 TypeScript 检查）通过。未重跑已知受限的 OpenCode 监听测试。 |

原生测试使用既有测试 stdio transport 和隔离继承插件；这不是生产插件选配或 Unix proxy/peer 验收。两次输入只连接不可用的本地 `127.0.0.1:9`，均观察到对应轮次连接错误并显式 interrupt，最终 interrupted。**没有成功模型、工具或 hook 执行**；不把正文注入当作方法行为。所有观察和副作用范围均限自有测试配置/状态。

## 红灯与修复记录

初始 Runtime 尚无入口、HTTP 无路由、旧界面无读取区域的红灯均保留。新增失败测试发现：未知原生格式版本和读取期间文件替换仍被接受，以及小日志的重复描述可展开为过大响应；修复后公开读取接口全部通过。

测试辅助问题单独保留：原生夹具最初使用含空格 command_id，随后 POST 缺 JSON Content-Type，均为夹具错误；首次浏览器在断连导致 details 重建关闭后查找不可见按钮，改为展开后核对。首次前端构建使用项目 ES2022 不支持的 `toReversed`，改为复制数组后 reverse，未更改目标环境或依赖。日志见 [logs](W02-08-skill-context-view/logs)；最初过大的失败日志以 gzip 原样压缩保留，另有简洁的同一失败重现。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags nativecodex -race ./internal/runtime -run '^TestCodexNativeSkillContextRead$' -count=1 -v
```

相关命令：`go test ./internal/runtime -run '^TestCodexSkillContext' -count=1 -v`、`go test -race ./internal/app ./internal/console`、`go test -race ./internal/runtime -run 'Test(Codex|ClaudePlugin|ClaudeMCP)' -count=1`、`go vet -tags nativecodex ./...`、`make dev-build`，均使用上述 GOCACHE。[浏览器模板](../../tests/codex_skill_context_browser.js)注入附件 [真实响应](W02-08-skill-context-view/final/browser-responses.json)及 web/dist 文件映射。

最终原生目录 `/tmp/netcap-w0208-skill-context-view-437615701`。开发二进制 SHA256：`4332c78de3c3f22ad082df20efaca7b970f6fe33a19495e849ff2a1d041d1cda`；旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。1670 个旧证据未改。未提交、推送或发布；自有后台和浏览器均关闭。归档宿主名称及 installationId 脱敏，rollout 中正文仅为自有测试标记。

[差异](W02-08-skill-context-view/changes.diff)、[审计](W02-08-skill-context-view/audit.json)、[归档清单](W02-08-skill-context-view/manifest.json)、[桌面](W02-08-skill-context-view/browser/desktop.png)与[移动](W02-08-skill-context-view/browser/mobile.png)可复核。

下一片继续把已取得的 Skill/MCP 混合组件契约接入逐会话选配，并保留描述准备、正文载入和方法/Gate 的区别。生产插件选择仍仅支持 MCP；完整 Skill/hooks、受管/安装来源、成功模型与权限、hook 成功/异步、Windows、生产 proxy/peer、输入独占及既有 WSL 待返回结果均未完成。
