# W02.07–09 · 恢复可执行环境后的五项原生探针

日期：2026-10-07。当前代理进程的 Unix socketpair、Unix 监听与 loopback TCP 监听重新检查通过，直接完成此前待用户 WSL 终端执行的五项已授权探针。无需继续等待这五份结果；历史目录和失败证据均保留。

## 结果

| 探针 | 正式断言 | 结论 |
|---|---:|---|
| Claude 插件原 ID 改配 | 72 | A/B 真实版本化 MCP 返回、逐次审批、Pre/Post hook、启停/升级/恢复继承和带历史重开通过 |
| Claude Skill 正文与相对资料 | 13 | 描述、按需正文、Read 资料、禁用排除和原 ID/历史保留通过；脚本供应商不证明自主方法 |
| Codex Skill 目录选择 | 23 | 三线程默认/禁用选择、其他来源、冷恢复和显式正文传递通过 |
| Codex 同步 SessionStart hook | 15 | 信任后真实 `hook/started` → `hook/completed`，原生状态 completed；未信任/整包禁用不执行 |
| Claude SDK Windows 来源查询 | 2 | 真实 reg.exe 查询 HKLM/HKCU 均可判定 absent；不等于完整生产 preflight |

正式共 **125 项**通过。独立读取 SQLite、原始事件、工具返回、供应商请求与 hook 协议，另有 **111 项审计**通过。没有生产代码或构建变更；Python AST、Node 语法检查通过。未重跑无关测试或重新构建。

固定版本为 Claude Code 2.1.280、SDK 0.3.280、Codex app-server 0.158.0；插件改配使用当前 `.dev/bin/netcap`，运行前后 SHA256 均为 `26a3b6257e4bcc3998d5c78c514025eb761ccf34e0bdf6910494513141321b58`。工具/Skill 探针只访问自有本地脚本供应商，hook 探针使用不可用 loopback provider 并取消轮次，Windows 只读查询不输出受管策略内容。用户配置、共享后台与真实供应商未修改或调用。

## 两个首次失败及修正

Claude Skill 首次失败在 `historical_body_retained`：断言在 SDK 消息投影 `history` 中寻找正文，实际正文保留在 `entries` 原始事件里。改配前后的 entries 完全相同，history digest 也相同。修正为先要求原 entries 含正文，再要求改配后整个 entries 原样保留；未将缺失正文视为通过，未改生产历史语义。

Claude 插件首次失败在 A 升级后的 hook 路径：资源版本已采用 managed 副本，真实 hook 从能力库保存的 v2 路径执行，测试却要求它来自导入前目录。修正为核对所选源的完整文件内容等于对应夹具版本、版本号正确，然后核对 hook 载荷来自该源。原有真实 MCP 返回版本、审批、原 ID 和历史检查保持不变。

首次失败目录 `/tmp/netcap-w0207-method-3q3wyf40`、`/tmp/netcap-console-dqgo7040` 与最终通过目录均归档；只修改这两个测试入口。未放宽 Runtime 准入、来源或历史契约。

## 新增证据的范围

插件 A 调用版本为 1/1/2，B 为 1/1/1/2；七次成功调用分别有 Pre/Post 载荷、独立审批和对应执行快照。A 禁用与恢复继承后的实际请求不暴露该工具，也没有额外调用或插件 hook。B 在 A 升级后仍使用旧版本，应用重启后保持原 ID/快照和历史，再明确升级。

Claude Skill 最初只向本地供应商提供描述；调用 Skill 后正文到达，Read 后资料到达；禁用会话不传正文/资料。重开和原 ID 禁用保留事件记录。Codex 固定版本的显式禁用输入本次观察没有传入正文；该观察不构成文件读取权限承诺，也不替代产品的显式选择约束。两者均没有自主 Skill 方法/Gate 结论。

Codex hook 本次收到 completed 原生回执，耗时 3 ms。/proc 采样没观察到短命子进程退出 0，并不否定已收到的原生完成；也不能反向把子进程退出当作成功。此结果补齐固定版本旧格式同步 SessionStart 的原生成功契约，但没有证明过去受限环境超时的根因，尚未验收成功记录贯穿生产 Application/Runtime/Handler、持久恢复与界面的链路。

Windows SDK 读取返回两个 hive 的 absent，用户和项目隔离设置均被识别。真实受管值存在、读取拒绝、动态 helper、Git/worktree 来源解析及完整权限合并继续按既有缺口处理。

## 复现与归档

```bash
python3 tests/claude_plugin_change_smoke.py
python3 tests/claude_skill_method_probe.py
python3 tests/codex_skill_selection_probe.py
python3 tests/codex_hook_lifecycle_diagnosis.py
python3 tests/claude_settings_resolution_probe.py --host
```

[原始目录与结果](W02-07-09-unblocked-probes/original-directories.json)、[命令退出状态](W02-07-09-unblocked-probes/command-results.json)、[独立审计](W02-07-09-unblocked-probes/audit.json)、[审计脚本](W02-07-09-unblocked-probes/audit.py)、[来源差异](W02-07-09-unblocked-probes/source.diff)、[清理](W02-07-09-unblocked-probes/cleanup.json)、[IPC 前置](W02-07-09-unblocked-probes/ipc-prerequisites.json)和[哈希清单](W02-07-09-unblocked-probes/manifest.json)保存在同名目录。结果、原生协议和必要自有事件以 gzip 留存；供应商请求保存脱敏摘要及原文件 hash，不归档凭据文件或完整供应商请求正文。

5861 个旧证据和全部基线生产源码、Web/bridge 构建、两个二进制保持不变。所有执行句柄已收取，自有测试进程已退出，未提交或推送。下一片将已通过的 Codex hook 成功契约接到现有生产验收，核对原操作/版本归属、重启历史与界面；不重复已有超时样本。W02.07/08/09 仍 in_progress，9/40 done；一般配置、完整四配置/受管/权限、真实方法/Gate、其他/异步 hook、共享后台/输入独占、Windows 日常链路及 W04–W07 等硬要求保留，Goal active。
