# W02.08：Codex 明确恢复原配置

2026-10-06，Codex app-server 0.158.0 / proxy 0.157.1。原生目录 Skill 与 CLI 插件改配丢失原生确认后，新增只读条件核对和“按原配置恢复原线程”。仅在原线程未加载、原历史未变、没有未决工作且原来源可核对时，明确恢复原 ID、原有效快照中的配置与权限。

## 行为与边界

原 applying 候选必须绑定原 uncertain 操作。只读入口核对六项：事务、原线程及完整加载清单、未决工作、原历史基线、原配置来源/约束、读取稳定性。加载清单缺失或 null、分页循环、包含原线程、历史或来源漂移均不能通过；读取不加载线程或改写配置。

明确恢复先持久化独立 `recover_execution` 意图，再沿已有原生改配链路建立原配置。派发及成功归档前复核原来源和操作绑定；只有本操作、当前连接、完整意图摘要对应的建立依据与只读核对同时成立才确认成功。原 Scope、执行快照与生效历史不升级，完整失败候选保存在配置尝试中，旧应用标失败，恢复操作标成功，自动化仍暂停。

恢复在 Runtime 中已确认、仅 Runtime→Application 回执丢失时，可以只读核对成功。恢复自身的原生回执在到达 Runtime 前丢失，则该恢复也保持 uncertain：保留候选、原快照和暂停，拒绝重复恢复，重启不重放。连接关闭或失败清除内存建立依据，不把当前配置看似相同当作该操作成功。此片仍未解决跨连接持久恢复确认或存活共享后台的完整多客户端输入独占。

## 实际验证

真实 Application/SQLite/Runtime 通过隔离 stdio 连接固定版本后台，使用普通原生目录 Skill 和包含 Skill/MCP/同步 hook 的本地插件。项目、profile、缓存及状态目录均独立。供应商为不可用 `http://127.0.0.1:9/v1`；没有成功模型调用。失联注入只拦截测试连接的配置回执，不修改原 CLI。

| 场景 | 结果 | 归档 |
|---|---|---|
| 原改配丢确认后明确恢复；随后独立发起一轮显式输入 | 103/103 | [摘要](W02-08-codex-original-recovery/normal/summary.json)、[完整结果](W02-08-codex-original-recovery/normal/result.json.gz) |
| 恢复已被 Runtime 确认，Application 漏收回执 | 97/97 | [摘要](W02-08-codex-original-recovery/application-loss/summary.json)、[完整结果](W02-08-codex-original-recovery/application-loss/result.json.gz) |
| 恢复自身丢原生回执，保持未知并重启核对 | 96/96 | [摘要](W02-08-codex-original-recovery/native-loss/summary.json)、[完整结果](W02-08-codex-original-recovery/native-loss/result.json.gz) |

三例共 296 项。恢复前后原历史均为 6→6；正常例随后单独明确 resume、输入、interrupt、pause，历史才变为 7。该轮核对原目录/插件描述、两个原版本正文、原操作/快照归属及 UserPromptSubmit 载荷；hook 仍为已有的受控超时失败，不能据此声称 hook 成功或方法/Gate 通过。原插件内 MCP 的加载核对不等于实际工具成功调用。

独立读取原始协议、App 快照、SQLite 导出及 hook 载荷的 [95 项审计](W02-08-codex-original-recovery/audit.json)全部通过；[审计脚本](W02-08-codex-original-recovery/audit.py)可直接针对归档重跑。截获的原生成功响应仅证明 fixture 观察到响应，不能冒充 Runtime 已收到。初次审计将成功快照省略的 false 字段当作必有键，出现 KeyError；已按 JSON `omitempty` 契约修正，保留[记录](W02-08-codex-original-recovery/audit-first.log)，未改生产或原生结果。

原始真实失败是 App 拒绝 Codex 恢复入口，保留[红灯日志](W02-08-codex-original-recovery/native-red.log)和[原始数据](W02-08-codex-original-recovery/red/result.json.gz)。另一次边界红灯发现缺失/null 加载清单被当成空列表，已修复并保留[失败](W02-08-codex-original-recovery/boundaries-red.log)与[最终通过](W02-08-codex-original-recovery/boundaries-final.log)。新增 Runtime 21 类边界、既有权限12类及新增 App5类通过。App/console 全包 race、Codex/Claude Runtime 非原生回归、带标签 vet、Web 类型检查及开发构建均通过，日志与[命令终态](W02-08-codex-original-recovery/command-results.json)一并归档。

真实 App 数据回放的 Codex [浏览器26项](W02-08-codex-original-recovery/browser-final.json)和 Claude [界面回归20项](W02-08-codex-original-recovery/claude-browser-regression.json)通过。界面区分恢复条件、失败候选归档与再次失联，修正 recovering 候选误报“尚未生效”，显示“实际生效待核对”；[桌面](W02-08-codex-original-recovery/ready-1440.png)和[手机未知状态](W02-08-codex-original-recovery/unknown-390.png)截图留存。浏览器没有发起真实原生执行；Claude 回归复用前片原生数据，不能计作本片新原生运行。

## 重跑与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -race -tags=nativecodex ./internal/runtime \
  -run '^TestCodexNativeOriginalConfigurationRecovery' -count=1 -v

python3 goals_W/evidence/W02-08-codex-original-recovery/audit.py
```

[来源目录](W02-08-codex-original-recovery/original-directories.json)、[source diff](W02-08-codex-original-recovery/source.diff)、[清理核对](W02-08-codex-original-recovery/cleanup.json)和 [manifest](W02-08-codex-original-recovery/manifest.json)记录归档时的源码、构建与证据。开发 `.dev/bin/netcap` SHA-256 为 `818866cb0fedc929149c9abe87ac0dc5e086776450b604a1a624d70275411f9d`。4263 个旧证据文件、Claude bridge 与旧 `bin/netcap` 保持不变；测试自有执行器均已退出，浏览器关闭、临时执行脚本移出工作区，未提交或推送。

本轮官方文档搜索及页面获取均因已配置代理不可达失败，curl 退出7，未取得最新页面。依据已有归档调研及固定版本真实协议实现，未将资料缺口标为通过。

W02.07/W02.08 仍 in_progress，9/40 done，Goal active。下一片继续恢复确认再次丢失后的持久核对与原生故障边界。真实成功模型/工具与方法/Gate、完整受管/安装来源、共享后台/Unix proxy、多客户端输入独占、Windows 和五项既有 WSL 请求仍未完成；不重复索取待回传结果。
