# W02.08 Codex 本地 hook 的受管策略准入

日期：2026-10-05。W02.08 in_progress；9/40 done，Goal active。

## 实现与边界

本地 CLI 插件选择现在读取原生 `configRequirements/read`，而非只依赖 `config/read` 中的配置与信任值。`allowManagedHooksOnly=true` 时拒绝启用含 hook 的本地插件，包括已明确信任和未信任的选择；禁用整包的新建仍可执行。平台私有缓存不会被当作受管 hook 来源，信任审核不能覆盖管理员的来源限制。

缺失响应、无效对象/字段、非布尔值或 RPC 错误均不放行；明确 `requirements:null` 表示原生未配置要求。对象缺失可核对的 `allowManagedHooksOnly` 字段时不猜默认值。原生错误正文不转发，要求正文不写入快照或 UI。

完整受管要求以保留数字精度的规范 JSON 参与已有连接配置指纹。建立前后、观察和输入检查会发现要求变化，撤下就绪状态并保留原快照/信任选择；查询候选不清除原选择的建立依据。重复观察缺少 witness 的会话时仍读取当前 hook 限制，明确显示“仅允许受管 hook”，不把原因退化成泛化的缺少连接依据。

本片没有添加受管配置编辑入口，也没有写 `/etc`、MDM 或用户配置。完整权限合并、其他强制要求的逐项解释及原 ID 在策略收紧后的安全禁用/恢复仍未完成。本片的策略拒绝与保持未知不能称为这些路径已经恢复。

## 证据层次

| 层次 | 实际结果 |
|---|---|
| Runtime 回归 | 21 场景通过，覆盖 trusted/untrusted、候选读取、来源和缓存漂移、受管拒绝、禁用、无要求、缺失/错误字段、RPC 错误脱敏、建立期间政策变化、既有连接政策变化及超过 IEEE-754 精度的数值变化。 |
| 原始缺陷 | `managed-only` 在修改前仍到达原生 create；先失败后修复。策略变化后具体原因被通用错误替代的问题同样先失败后修复，日志均保留。 |
| 原生控制 + 策略响应边界 | 最终 16 项通过。真实固定 0.158.0 返回 `requirements:null` 时，正常本地 hook 配置建立并核对成功。随后测试接缝对**真实原生读取回执**加入更严格的 managed-only 字段，Production Runtime/Application/SQLite/Handler 拒绝新建启用、允许新建禁用、撤下旧执行就绪状态并阻止输入。 |
| 持久状态与幂等 | 被拒绝创建的原操作记录为 `not_sent`，无原生 ID；同命令不新增 native create。SQLite 重开仍保留旧执行快照、unknown 和拒绝记录。 |
| 浏览器 | 生产构建与 Handler 导出回放 16 项通过。政策变化显示具体原因，原快照/信任不改写，输入禁用；创建拒绝显示“确认未发送”；禁用整包可核对。1440/390 截图已查看，无横向溢出。HTTP 交付受控；页面异常为空，fixture 未映射的 favicon 请求不属于生产 API 验收。 |
| 已有真实原生回归 | 组合资源/两类丢回执 90 项、原 hook 信任改配 40 项通过。保留原生→Runtime 丢回执后的 unknown，不称安全恢复完成；原 hook 事件样本仍为超时。 |
| 工程检查 | 相关 Runtime race、app/console 全包 race、tagged vet、gofmt、开发构建通过。完整 race 后补充了具体原因保留，最终重跑全部 21 类 hook 选择测试的 race、16/90/40 原生链路及构建。Web 源码未改，构建资源 hash 与前片相同。 |

**严格区分**：managed-only 的应用链路由测试 transport 增强原生响应取得，不是本机真实 `requirements.toml`/MDM 生效试验。接缝保留真实响应并只增加更严格限制，没有删除或放松真实要求；测试先确认实际原生要求为 null。原生请求轨迹与交付给 Runtime 的受控响应分别保存，不能把后者当作 daemon 的实际策略。最终新流程两个自有后台均退出 0；组合回归四个、hook 回归三个均退出 0。初步 15 项是同一流程较早结果，不累计计数，其两个后台也已退出。

新准入流程没有发送模型轮次或执行 hook/MCP；回归流程按既有脚本使用不可用本地供应商、显式中断与超时 hook，不调用真实供应商，不停止用户共享后台。

## 原生契约来源

本轮使用 OpenAI Docs 路径尝试读取官方 [Codex App Server](https://developers.openai.com/codex/app-server/)，返回 403，未取得新官方正文。沿用仓库 [Codex App Server.md](../../Codex%20App%20Server.md) 的 `configRequirements/read` 说明与既有固定 0.158.0 生成 schema；`ConfigRequirements` 明确包含 `allowManagedHooksOnly: boolean | null`。摘录、schema 与请求失败记录已归档，schema 不替代真实受管强制执行证明。插件 list/install 接口此前的开发中限制没有改变，本片不新增它们的生产调用。

## 复跑入口与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime \
  -run '^TestCodexHookSelectionEstablishesOnlyOwnVerifiedConfiguration' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime \
  -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^(TestCodexNativeHookManagedRequirementsAdmission|TestCodexNativeCombinedResourcesAndReceiptLoss|TestCodexNativeHookSelectionAndTrustChanges)$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器：[脚本](../../tests/codex_hook_requirements_browser.js)。将 `__FIXTURE__` 替换为最终 `browser-responses.json`，`__ASSETS__` 替换为本次 web/dist URL→本地文件映射，生成项目内 `.dev/w0208-managed-hooks/run-browser.js`，使用现有 Playwright MCP 执行。没有启动监听服务或更改受限 IPC 环境。

- 工作目录：`/tmp/netcap-w0208-managed-hooks-work`。
- 最终新链路：`/tmp/netcap-w0208-managed-hooks-861564885`；初步：`/tmp/netcap-w0208-managed-hooks-2811526216`。
- 组合回归：`/tmp/netcap-w0208-combined-resources-775478543`；hook 回归：`/tmp/netcap-w0208-hook-selection-585673810`。
- [归档](W02-08-managed-hook-policy/)包含结果、原生轨迹、受控响应、源码差异、日志与截图；[审计](W02-08-managed-hook-policy/audit.json)确认 2850 个旧 W 证据及旧二进制未改。
- `.dev/bin/netcap`：`0bc6088c8de1c4ca65141cff7f3951e75a41f1b88c78cd2a4b1833abf031f035`。
- JS：`692b97f3ac1d41930622e634af4e8c9cc9a68773214770ced8a2de9717e95f7b`；CSS：`3781875c9ca317492b8773f0b67767aa507fe40eca497aedb876f6190b6c845a`。
- 旧 `bin/netcap`：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。

下一片继续政策或来源变化后的原 ID 显式撤去/禁用及故障核对，并收取已发出的四项 WSL 请求。真实受管与完整来源、方法/Gate、成功/异步/通用 hook、原生丢回执安全恢复、仍存活共享后台/多客户端输入独占、Windows/Unix proxy/peer 等硬要求保留，未提交或推送。
