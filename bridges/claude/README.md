# Claude SDK bridge

该 bridge 是 `serve` 的本地子进程，不是对任意独立 Claude CLI 的热接管接口。固定 SDK 0.3.280 / Claude Code 2.1.280，Node 24.13.0 已验。

```bash
make claude-deps claude-build
make claude-contracts
make claude-integration claude-recovery claude-idle
```

Go 通过 stdin/stdout 的 NDJSON v1 与 bridge 通信；每个 envelope 有 `v`、`id`、`method`、`params`，响应带同一 id 的 result 或 error。hello 握手验证协议/SDK。只支持明确的 create/send/snapshot/respond/interrupt；调度和路由准入在 Go，bridge 不接受任意脚本。

Go 为每个实例建立独立状态目录和 flock，FD 传给 bridge，控制台崩溃后旧 bridge 清理 SDK query 期间仍持锁。stdin EOF 和 SIGTERM 关闭自有 query。实例全部会话经完整证据确认空闲、30 秒无新操作后，Go 回收其 CLI 和 bridge；只读空闲视图最多缓存 30 秒，发送和未决操作核对始终查询新证据。后续快照按需启动只读 bridge，后续输入按原生历史恢复 query。Unknown/审批/后台占用不会被闲置回收误当空闲。metadata 只保存原生 UUID、来源、目录、模型、端点和权限；输入交接及 SDK 事件先 fsync journal，再提供回执。凭据使用每 query 的 env，经 stdin 传递，不写入 metadata/journal，也不修改 process.env。原生历史由官方 getSessionMessages 读取。

普通会话显式使用 claude_code preset、user/project/local settingSources 和 default 权限；只读策略对 Bash/Write/Edit/NotebookEdit 请求审批。探测只使用独立 profile/cwd 与明确关闭的文件设置/hook；指定端点/认证/模型沿用原 route。实际项目规则、hook 与双端点测试见 tests/claude_settings_smoke.py。控制台尚未启用外部通知；原生 hook 仍是其原有通知的唯一发送方。

逐次审批同时使用 SDK `PreToolUse` 的 ask 响应，覆盖只读写入工具及显式启用的 MCP/插件 MCP 命名空间。受管 `allowManagedPermissionRulesOnly` 会忽略原有 `flagSettings.ask`；SDK hook 使这些调用仍进入工作台审批，更严格的原生 deny 保持生效。初始化必须收到 `hooks_applied=true`，缺失或 false 均在记录输入前拒绝；错误检查先于 query 复用，关闭尚未完成时的 send 重试同样拒绝。`ConfigChange` 仍只在启用设置守卫时注册。固定版本的真实权限矩阵与基线绕过证据见[受管工具审批](../../goals_W/evidence/W02-09-claude-managed-tool-approvals.md)。

普通会话可携带 `mcp_selections`，仅保存资产 ID、服务名、模式及定义文件路径。stdio 和 HTTP/SSE 定义在启动 query 时读取，通过 `mcpServers` 注入，保留其他继承 MCP；远程 `headers` 的环境引用由原生 CLI 展开。`mcpServerStatus` 只投影 name/status/source/tools，原始 config 不外传。启用项必须实际 connected 且来源为 dynamic，禁用项必须缺席或 disabled；初始化与输入前验证，单次 ask 规则复用已有权限回调。后台恢复重用原选择；历史成功调用与当前加载状态分别展示。SDK 的 `tool_use_result` 为按工具变化的值，Go 只对 Bash 单独解析后台/中断元数据，MCP 数组结果不再破坏整份观察。OAuth 凭据由原生 CLI 复用与刷新，`setMcpServers` 热改配仍未接入，见 [生产使用范围](../../CONSOLE.md#claude-原生-mcp-启动选择)、[stdio 验收](../../goals_W/evidence/W02-06-stdio.md)与 [远程/清单故障验收](../../goals_W/evidence/W02-06-remote.md)。

选中服务为 `needs-auth` 时，bridge 抛出带服务名的 OAuth 诊断并确认未发送模型输入，原始 config/error 不进入诊断。SDK 不启动交互式授权；Application 的 `prepare_mcp_login` 仅核对当前/候选快照并准备私有单服务项目与原生终端命令，回执持久化后可读回。原生 `mcp login --no-browser` 需要 TTY，负责 PKCE、授权回调、凭据保管和刷新；认证不赋予工具权限。A/B 凭据隔离、撤销/重新认证后重启自有控制台并按原 ID 恢复已经验证；不重启控制台路径的原生验收尚未完成。HTTP 完整准备流程通过，SSE 完整准备与新增准备 UI 的实测被当前环境阻断，见 [OAuth 验证范围](../../goals_W/evidence/W02-06-oauth.md)和[使用说明](../../CONSOLE.md#claude-mcp-原生-oauth-认证)。

针对已有会话的 `MCPAuthenticationRequired` 启动失败，现增加有条件的原 ID 重试：原生日志存在、旧输入均有终态且无后台/权限占用时，先关闭自有 query 并等待读取器和子进程退出，再检查关闭期间到达的占用事件，最后释放此次初始化错误。被拒输入不入日志、不自动重放；新 query 保留原配置、原 ID 和单次 ask 规则。其他错误、首次创建、未知工作和候选改配仍走原保护路径。`make claude-auth-contracts` 验证生产 Session 与受控 SDK 传输的 10 项生命周期行为；真实不重启恢复仍缺原生验收，见[记录](../../goals_W/evidence/W02-06-auth-recovery.md)。

HTTP/SSE 的 `headersHelper` 已经静态校验并原样传递到固定 CLI，bridge 不执行命令、展开凭据或暴露原始输出。SDK 0.3.280 的公开 MCP 类型遗漏该字段，真实 CLI 2.1.280 的动态注入路径已验证执行；升级版本时需重跑。helper 使用所选项目 cwd，相对路径不改写为资源目录；项目来源的原生信任与凭据环境清理保持。15 种组合的 58 项连接前检查通过，完整远程认证仍受 socket 限制未验收。独立 OAuth 准备对 helper 定义明确拒绝，避免静默改变 cwd。[使用方式](../../CONSOLE.md#claude-mcp-动态认证命令)与[验证范围](../../goals_W/evidence/W02-06-headers-helper.md)。

MCP 核对失败按所选服务区分清单不可读、缺席、重复名称、继承冲突和连接失败；只使用服务标识与已知来源，不转发原始 error/config。Go 的能力证据增加可选 `source`，重复/未知来源不会被解释为确定来源；准入规则保持原样。16 种启动配置的来源/策略枚举 44 项通过，但本次环境内 stdio 子进程在 initialize 前收到 EOF，13 项连接检查失败，整体原生探测保持失败；工具权限执行、完整受管来源与浏览器仍未验收。[分层记录](../../goals_W/evidence/W02-06-source-diagnostics.md)。

原有 MCP 的启停改配使用 `prepare_execution` / `apply_execution`。Go 负责持久意图、快照版本和原生执行范围准入；bridge 以配置加 SDK 历史（`includeSystemMessages: true`）摘要检查准备基线，只关闭已确认空闲的当前 query，并等待其进程退出。先持久 `applying`，再关闭/原 ID 启动/验证，将新绑定和 `verified` 结果一起 fsync/rename 提交；可确认退出的失败保留原绑定和 `not_sent`。metadata 兼容原平面 SessionConfig，新增可选 `_configuration` 记录操作 ID、前后选择、摘要和白名单清单，不含展开凭据。只读 snapshot 不重放改配；崩溃遗留 `applying` 阻止新输入。持久成功结果可在回执丢失后确认最后生效版本，离线时不会把旧加载清单显示成当前 verified。[改配验收与边界](../../goals_W/evidence/W02-06-apply.md)。

同一事务也支持新增资产、显式资源版本替换与恢复继承。Go 将固定资源引用展开为完整目标 `mcp_selections`；bridge 接受目标服务集合变化，但保留原会话身份、cwd/settings/认证。目标为空时传递空数组，仍读取原生清单；已撤去的旧服务必须不再属于动态注入来源。继承继续使用原生 settings sources 和权限，不清空所有配置。已执行成功的操作按持久候选和原生证据晋升，能力库后续准入变化不改变历史事实。[资源与继承验收](../../goals_W/evidence/W02-06-resources.md)。

输入 UUID 是 Go operation ID；恢复只核对原输入/原生终态，缺少结果不 replay。审批回调引用 requestId 和当前 connection，AskUserQuestion 的答案按原问题文本返回 SDK。停止时对当前自有轮次 interrupt，并通过 stopTask 终止该输入产生的后台任务；Go 等待终态及后台集合清空才释放占用。

T04 本地故障与浏览器验收已通过，证据见 [T04](../../goals/evidence/T04.md)。构建产物 dist 不纳入源码；本地打包前执行 claude-build，生产部署需要 Node、bridge dist 及锁定运行依赖。整个应用的打包与资源目标归 T14。
