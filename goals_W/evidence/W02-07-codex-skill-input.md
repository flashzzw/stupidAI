# W02.07 · Codex 显式 Skill 输入与原版本正文归属

2026-10-05。普通 Codex 会话可随一条消息显式选择当前插件中的 Skill；请求保存原操作和执行快照，原生正文载入记录可核对请求版本。最终原生 **92 项**、新界面 **25 项**通过。W02.07/W02.08 仍为 **in_progress**，整体 **9/40 done**，Goal active。

## 用户可见行为与边界

消息框新增“本条消息附带 Skill”，默认不选，只列当前启用插件中的 Skill，并显示保存的资产版本。一次最多 16 项；切换会话或执行快照清除旧选择。HTTP 回执丢失保留草稿和选择，同一请求继续使用原 command ID；接受后清除。当前支持标准 Skill/MCP 混合插件中的 Skill，普通目录 Skill、仅含 Skill 的包及 Claude 输入不由本片接通。

公共 `send` 请求增加 `skill_input: {snapshot_id, items: [{asset_id, name}]}`。浏览器不传原生路径、正文或文件摘要。Application 先核对当前快照和唯一启用的组件，再把选择与原 Scope、ExecutionSnapshotID 一同持久化。旧快照、空列表、重复、未知或禁用项及其他命令均拒绝；Runtime 再按保存绑定解析路径，执行既有完整资源、来源、路由、权限、空闲和整包策略检查。

Runtime 在原生 `turn/start.input` 的原文本后追加 `{type: "skill", name, path}`，保留 `clientUserMessageId`、权限和无关插件禁用 ID。这是一条消息中的明确载入请求，不是新的 Session，也不额外授予工具权限。

“Skill 上下文记录”新增本轮显式请求：原操作、原快照、资产版本和请求正文摘要。仅当唯一保存操作的原生轮次、原 Scope 和原快照均匹配时关联；名称、路径及正文 SHA256 全部一致才显示“请求版本正文已载入”。内容不符、摘要缺失、未观察到载入分别保留对应状态；缺回执、歧义或缺原快照不补造归属。历史版本取自该操作引用的快照，不随能力库更新重标。

`body_sha256` 是 `<skill>` 包装内原文件字节的摘要；已有 `content_sha256` 仍是完整载入片段摘要，二者含义不变混用。真实 LF 与 CRLF 文件、同轮两个不同正文均已核对。查询先复制 Reader 返回值，再附加关联，不修改 Reader 持有的数据、Session 或 Operation；正文不传给浏览器。

正文载入不证明模型已使用方法，也不证明 Gate 通过。禁用不删除历史正文，不能替代文件权限；完整跨客户端输入独占、所有 Skill 来源与方法验收仍未完成。

## 验证结果

| 层次 | 实际结果 |
|---|---|
| Application | 持久请求与同 ID 幂等、8 类拒绝、7 个正文/回执/歧义/快照关联场景通过；共享 Reader 数据不会被修改。app 全包 race 通过 |
| Runtime / HTTP | 原文本与原路径输入、8 类拒绝及正文摘要通过；HTTP 拒绝原生路径、inline body、错误 items 类型和缺会话。相关 Codex/异步/SkillContext race 与 console 全包 race 通过 |
| 显式 Skill 原生 | 92 项通过：新建、启停、明确升级、继承/重新添加、共享库更新保留旧版本、原 ID 冷恢复、双 Skill、LF/CRLF 与原请求关联；一次真实回执丢失后同轮次恢复，`turn/start` 只发一次 |
| 原生回归 | 混合插件 57 项、上下文读取 20 项通过；读取回归另连续 10 次通过，原始并发测量失败完整保留 |
| 浏览器 | 最新构建新输入 25 项通过；混合插件 17、Skill 上下文 20、Claude 插件 16 项通过。含草稿/选择保留、同 ID 重试、16 项上限、历史版本、内容不符、断连清除与移动布局 |
| 构建与检查 | `make dev-build`（含 web typecheck）、相关 vet、带 nativecodex 标签的 vet 通过；最终差异、文档引用和归档审计见附件 |

显式 Skill 夹具固定 **app-server 0.158.0**；配置中的 proxy 版本为 **0.157.1**，测试替换为隔离 stdio 传输，不证明生产 Unix proxy/peer。十轮输入中七轮各附两个 Skill，三轮先拒绝禁用/继承状态下的显式选择再发普通文本对照。均只尝试不可用本地供应商 `127.0.0.1:9` 并由 Application 明确中止；没有成功模型响应、工具调用或 hook。最终三组原生测试的九个自有后台全部退出 0，没有启动 listener、改用户 profile 或切换共享后台。

新浏览器测试使用 `/tmp/netcap-w0207-skill-input-247781947` 的真实 Application/Handler 导出，HTTP/SSE、回执丢失、内容不符及 17 个选项的上限场景受控；不把回放算作额外原生执行。该导出与最终原生 92 项使用相同产品代码，后续仅改测试。所有截图和浏览器结果使用最终 `index-DojFXgIb.js` 构建；浏览器已关闭。

## 失败、诊断与修复

新增请求、Runtime 输入、正文摘要和关联测试先红后绿。关联最初直接修改 Reader 返回的共享 Frame，回归捕获后增加复制；另一个快照比较受同时间操作排序影响，测试按操作 ID 排序后比较。

既有原生读取回归最初把两个协议 checkpoint 之间的全部调用归入一次读取。重复十次观察到后台轮询跨区间：另一个会话及同会话的 queue/goal/terminal/list/resume 调用被算入，另一次观察回写还改变了完整展示快照。原始结果和区间调用保存在 `failures/`。测试传输现在记录调用者 context 标签，仍保持真实后台轮询，只核对该次读取的调用均为原 ID 的 `thread/read(includeTurns:false)`；同时核对所有会话的绑定、控制权和执行快照不变。完整无写入契约继续由 Application 的受控测试验证，不把持续变化的观察视图要求为静态。未为此修改产品行为或放行原生写调用。

新增真实丢回执实验在 Runtime 已取得原生 ID/turn ID 后，把整个回执丢弃并返回 EOF 给 Application。只读恢复找回同轮次、原请求及正文载入，未重发。首次新断言误要求终态 `succeeded`；SQLite 显示实际为主动中止后的 `interrupted`，身份已正确恢复。改正断言后整组通过，未把中止称为模型成功。

补拍移动截图时，执行快照更新已清掉旧上下文展示，初次截图定位超时；测试显式重新读取后取得截图，25 项重跑通过。生产界面不为截图保留旧读取结果。此前已知 listener/Windows 限制没有重探或绕过，Runtime 全包没有重跑或宣称通过。

## 复跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^TestCodexNative(ExplicitSkillInputs|MixedPluginSelectionAndChanges|SkillContextRead)$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime \
  -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

最终原生目录：`/tmp/netcap-w0207-skill-input-3344630130`；回归为 `/tmp/netcap-w0208-mixed-plugin-4283645955` 和 `/tmp/netcap-w0208-skill-context-view-671020146`。[原生结果/协议](W02-07-codex-skill-input/native/result.json)、[原始 rollout](W02-07-codex-skill-input/native/rollouts/)、[界面结果](W02-07-codex-skill-input/browser-input-final.json)、[桌面](W02-07-codex-skill-input/desktop.png)、[移动](W02-07-codex-skill-input/mobile.png)、[测试日志](W02-07-codex-skill-input/logs/)、[差异](W02-07-codex-skill-input/changes.diff)、[审计](W02-07-codex-skill-input/audit.json)及[清单](W02-07-codex-skill-input/manifest.json)可复核。installationId/宿主标识脱敏，未复制认证文件或 SQLite。

浏览器入口 [codex_skill_input_browser.js](../../tests/codex_skill_input_browser.js) 的 `__FIXTURE__` 使用归档 `browser-responses.json`，`__ASSETS__` 替换为当前 `web/dist` 的绝对路径映射；实际使用受控 Playwright 路由，无需本地监听。其他浏览器回归沿用对应测试脚本和归档夹具。

开发构建 SHA256：`b28e578473d55d6f0baa86ec50b98f613e8ce632cfd0c5b19da119042210a027`。1860 个旧证据与旧 `bin/netcap` 保持不变，源码差异相对 `/tmp/netcap-w0208-skill-invocation-work/before`；未提交、推送或发布。下一片继续仅 Skill/普通来源的完整选择与方法/Gate 证据，Claude 插件改配、Claude 方法和 Codex 完整选择的既有 WSL 待返回结果仍保留，不能据本片关闭 W02.07/W02.08。
