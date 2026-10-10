# W02.09 配置与混合能力恢复再次丢回执

日期：2026-10-07。继续验证非空 effort 采纳与混合插件、独立 Skill 的同一候选事务；本片只扩展测试及证据，没有生产改动。

原配置为 high 且启用两个能力资源，候选为 medium 且禁用资源。策略写入或退订的原生回执丢失后，原快照和候选保持不变；还原原文件并通过只读核对后，明确恢复原 ID、high、原能力与历史，失败候选归档一次，自动化继续暂停。

进一步在恢复的中间加载、策略清除、退订、最终加载四处再次丢失回执。四种情况下均保留原 Scope/快照/历史/候选，以及各自独立的应用和恢复操作；两次操作均为 uncertain，不生成恢复确认或失败候选归档。重开自有应用和 stdio 后台后状态不变，不重新派发。即使丢失的最终回执显示 high，也不能据此把没有收到确认的恢复标为成功。

## 实际验证

| 场景 | 原生断言 | 结果 |
|---|---:|---|
| 改配策略写入丢回执后明确恢复 | 27 | 通过，high、原能力与原历史恢复 |
| 改配退订丢回执后明确恢复 | 27 | 通过，失败候选仅归档一次 |
| 恢复中间加载再次丢回执 | 42 | 通过，双操作持久未知 |
| 恢复策略清除再次丢回执 | 42 | 通过，不继续退订或最终加载 |
| 恢复退订再次丢回执 | 42 | 通过，不继续最终加载 |
| 恢复最终加载再次丢回执 | 42 | 通过，原生返回 high 仍不伪造确认 |

正式六例共 **222 项**通过；首个中间加载试跑另有 42 项通过，单独保存，不计入正式总数。原生轨迹复核实际丢失位置、请求/回执的 effort、加载次数、历史与 hook 标记。恢复再次丢回执后，send/resume/prepare/apply/discard/新的 recover 均拒绝；重复原命令只返回原操作 ID，重启后的读取和拒绝命令没有额外 thread/resume。

浏览器用四例真实 Handler 的未知状态及重开状态回放，共 **83 项**通过：原/候选快照、high → medium 差异与暂停均保留；没有成功归档、恢复核对入口或应用按钮；输入、撤销和候选勾选锁定。1440/390 宽度截图已保存，无页面错误、无横向溢出，全部请求为 GET。此回放不替代原生执行。

tagged vet 通过。固定 app-server 0.158.0、proxy 0.157.1；全部输入、配置、SQLite、后台和 hook 属于临时测试目录，provider 指向不可用 localhost 并取消输入。hook 标记用于检查实际执行边界，刻意超时不算成功生命周期。此次没有完整供应商、Skill 方法/Gate 或四配置联合结论。

```bash
export GOCACHE=/tmp/netcap-w0206-auth-go-cache
export NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
export NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeConfigurationPolicyReplyLoss$' -count=1 -v
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeConfigurationRecoveryReplyLoss$' -count=1 -v
go vet -tags=nativecodex ./internal/... ./cmd/...
```

浏览器模板为 `tests/codex_configuration_recovery_loss_browser.js`，只注入各实际结果的两阶段 Handler 响应及当前 Web 资源路径。

## 证据与边界

[原生目录索引](W02-09-config-recovery-loss/original-directories.json)、[命令结果](W02-09-config-recovery-loss/command-results.json)、[浏览器结果](W02-09-config-recovery-loss/browser-loss.json)、[桌面截图](W02-09-config-recovery-loss/unknown-1440.png)、[移动截图](W02-09-config-recovery-loss/unknown-390.png)、[独立轨迹审计](W02-09-config-recovery-loss/audit.json)、[真实差异](W02-09-config-recovery-loss/source.diff)、[清理](W02-09-config-recovery-loss/cleanup.json)和[哈希清单](W02-09-config-recovery-loss/manifest.json)随片保存。归档脚本首次把未纳入基线的 cmd 文件误认为新增，已修正比较范围并保留[审计脚本失败说明](W02-09-config-recovery-loss/audit-initial-failure.json)；没有修改产品以满足审计。

7 份原生结果完整压缩保存，40 个自有原生进程正常退出，浏览器及工具句柄收尾。5829 个旧证据、基线覆盖的生产 Runtime/Web/Claude bridge 源码、Web/bridge 构建和两个二进制保持原哈希；未重建、提交或推送。开发二进制 SHA256：`26a3b6257e4bcc3998d5c78c514025eb761ccf34e0bdf6910494513141321b58`。

缺失原生确认的恢复仍为未知；本片不增加强制恢复或清理入口。完整一般配置及清除 effort、混合/四配置联合、受管/外部依赖/动态权限、真实模型方法/Gate、成功/通用/异步 hook、共享后台/输入独占、硬崩溃和 Windows 仍未收口。当前执行环境的监听/IPC 限制已解除，下一片直接补跑此前待 WSL 终端的五项隔离探针，按真实结果修复并归档，不重复索取已授权操作。W02.07/08/09 保持 in_progress，9/40 done，Goal active。
