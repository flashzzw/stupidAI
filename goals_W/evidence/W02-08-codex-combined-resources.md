# W02.07/W02.08 · Codex 组合资源改配与两类回执丢失

2026-10-05。普通原生目录 Skill 与含 Skill/MCP/同步 hook 的本地插件同时选配、组合撤销/升级、正文归属和冷恢复的生产链路已验证。最终原生 **90 项**、浏览器 **27 项**及原有 hook 流程回归 **40 项**通过。本片只扩展测试与证据，没有改变生产代码或两个二进制。

应用层漏收结果后，Runtime 保有本连接建立依据时可以确认原候选生效，未重放改配。原生恢复回执在到达 Runtime 前丢失时，结果仍未知；应用/自有后台重启后保留原 ID、当前快照、候选和暂停，不重放。后者验证的是保留与拒绝行为，**不表示安全恢复已完成**。W02.07/W02.08 in_progress，9/40 done，Goal active。

## 组合链路

复用原有 Application/SQLite → Runtime → 固定 Codex app-server 0.158.0 stdio → Handler 流程。A/B/C 同时引用同一原生目录和同一混合插件，各自固定资源版本与选择：A hook 信任、目录启用；B hook 未信任、目录禁用；C 整包禁用、目录启用。插件 MCP 已连接但没有工具调用。

实际轮次按原操作核对插件与原生目录两类描述/正文，使用资产自己的版本，不把目录方法转为插件。A 原 ID 撤销 hook 信任、升级插件到 asset-v2，再同时禁用目录与撤销 hook 信任；随后原地升级目录到 directory-v2 并恢复信任。逐轮正文 SHA256、原快照、原操作和 hook 明确超时记录匹配。冷恢复后仍为原 ID，两个 v2 绑定正确，历史 asset-v1 hook 归属保留。B/C 的原绑定不被 A 的操作重写。

全部八轮只尝试不可用的本地供应商，观察到连接错误后明确 interrupt。hook 夹具使用 2 秒命令和 1 秒期限；启用/信任时有明确超时，未信任或整包禁用时没有调用。没有成功模型、工具或 hook 结果，没有方法/Gate 验收。

## 两类故障及结果

| 丢失位置 | 故障注入与观察 | 可得结论 |
|---|---|---|
| Runtime → Application | 原生组合改配完成，Runtime 已核对配置并保存本连接依据；测试包装器记录真实 Intent/Receipt，向 Application 返回 unexpected EOF | Application 只读核对后提升原候选，原操作完成；同 command ID 重试仍为同操作，配置 resume 只增加一次 |
| Native → Runtime | 自有 stdio 包装器实际调用配置 `thread/resume`，保存完整原生响应，再丢弃响应并向 Runtime 返回 unexpected EOF | Runtime 没有取得新配置建立依据；旧快照、待应用候选、原 ID 和暂停保留，新输入/重复应用被拒绝 |
| 原生回执丢失后重启 | 关闭 Application，重启该测试自有后台，再打开同一 SQLite 状态 | 原线程为 notLoaded，结果继续 unknown；没有重新加载或重放配置/模型输入，原生历史轮数保持 |

真实丢失材料：[应用层结果](W02-08-codex-combined-resources/native-final/application-receipt-loss.json)、[原生响应与参数](W02-08-codex-combined-resources/native-final/native-receipt-loss.json)。完整协议、事件与快照保存在 result.json.gz；各文件由实际测试输出无损压缩。

检查名 `native_loss_applied_once` 统计的是该配置请求只发送一次并取得被测试包装器丢弃的真实响应；不声称回执丢失后逐项当前信任已被 Runtime 读回。本片没有在未知会话里追加“验证输入”。固定版本仍缺逐线程当前 Skill/hook 选择的可靠回读，不能凭旧上下文、全局目录清单或原 ID 保留将该操作判为完成。

该 stdio fixture 的连接关闭会退出其自有后台。故障后的短暂断连期间，正常生产接入会拒绝 fixture 未配置的 socket（日志中可见“Codex socket 不是可核对的本地后台端点”）；随后显式重开自有 fixture，读取原线程为 notLoaded。本片不将此过程外推为仍存活的共享后台多客户端/Unix proxy 恢复，也没有停止用户共享后台。

## 界面与验证

浏览器从真实快照和 Handler 导出回放三个阶段：组合改配前、应用层漏回执已核对后、原生回执丢失且重启后。普通 Skill 与插件来源分开展示，两个资产版本和 hook 信任差异明确；保存候选不改变当前选择。恢复确认后原操作显示已完成，禁用目录从输入选择器消失。未知阶段仍显示旧当前选择和新候选差异，暂停/禁止输入，不提供重复应用，候选不能随意撤销，重新核对只发送 reconcile。

| 验证 | 结果与附件 |
|---|---|
| 最终真实原生链路 | 90 项通过；[摘要](W02-08-codex-combined-resources/native-final/summary.json)、[日志](W02-08-codex-combined-resources/logs/native-loss-first.log) |
| 初步组合/应用层丢回执 | 80 项通过，未包含随后补入的原生回执故障；[摘要](W02-08-codex-combined-resources/native-first/summary.json)，不与最终计数累加 |
| 原 hook 流程回归 | 40 项通过；[摘要](W02-08-codex-combined-resources/regression-hooks/summary.json) |
| 浏览器 | 27 项通过；[结果](W02-08-codex-combined-resources/browser.json)、[已核对 1440px](W02-08-codex-combined-resources/recovered-1440.png)、[未知 1440px](W02-08-codex-combined-resources/uncertain-1440.png)、[未知 390px](W02-08-codex-combined-resources/uncertain-390.png) |
| 静态/竞争 | 两个原生运行均开启 race；`go vet -tags=nativecodex ./internal/runtime` 与 gofmt 通过 |

最终原始目录 `/tmp/netcap-w0208-combined-resources-468417555`，四个测试自有后台均退出 0，原 profile 文件未改。初步组合目录 `/tmp/netcap-w0208-combined-resources-301941064` 的三个后台、回归 `/tmp/netcap-w0208-hook-selection-2276130200` 的三个后台也已退出。不存在待等待的运行句柄。

浏览器使用已有构建，HTTP/SSE、command ACK 与结果阶段切换受控；它验证 UI 请求和显示契约，不是浏览器真实原生执行。新测试首轮把 Adapter 接口误写为不存在的 Runtime 接口，编译失败；改正后才取得上述原生结果。浏览器首轮漏写现有“· Skill”标签，断言失败后按实际标签修正。两份失败日志均保留，属于测试夹具错误，没有将其记为产品修复。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^(TestCodexNativeCombinedResourcesAndReceiptLoss|TestCodexNativeHookSelectionAndTrustChanges)$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/runtime
```

[浏览器模板](../../tests/codex_combined_resources_browser.js)和 [prepare_browser.py](W02-08-codex-combined-resources/prepare_browser.py)使用最终原生 browser-responses.json 及已有 web/dist，由 Playwright MCP 执行。本片没有生产改动，因此未重复全包 Go 回归或重新构建。没有重复受限环境探测，没有新建 listener 或 Windows interop；四个既有终端请求仍待返回。

旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`；`.dev/bin/netcap` 保持 `b825dd6b9cd2030ec003ebf975c132dedc73efdc0e70226d10a4057a4c5b62ff`。Web 保持 `index-Dv170Vq0.js` / `index-DSkOCaGd.css`。2753 个旧 W 证据未改；修改边界、源码/证据哈希见 [audit](W02-08-codex-combined-resources/audit.json)、[diff](W02-08-codex-combined-resources/changes.diff)、[manifest](W02-08-codex-combined-resources/manifest.json)。未提交、推送或发布。

剩余硬要求：原生回执丢失且缺连接依据时的安全恢复、仍存活共享后台与多客户端输入独占、真实方法/Gate、受管/完整来源、成功/异步/通用 hook 和 Windows/proxy。下一片继续来源可控范围与故障解释，并沿已验证条件推进独立工作；不能用本片的“保持未知”代替完整恢复验收。
