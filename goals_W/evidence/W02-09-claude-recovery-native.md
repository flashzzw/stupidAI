# W02.08 / W02.09 · 原生恢复补验与测试来源隔离

2026-10-10。补齐此前因 bwrap 权限阻塞而未执行的五条恢复路径：冷原查询启动失败、已有安装绑定下的冷失败、恢复回执丢失，以及本地设置还原后的独立恢复版本及其丢回执路径。最终 **157 项原生检查、140 项独立复核通过**。

本次使用真实 Application/SQLite、进程内 console Handler、Runtime、生产 Session 和固定原生 CLI；模型供应商为隔离命名空间内的本地模拟服务。范围限定为 **custom settings、ordinary 自有策略、受控的注册表缺席**。没有浏览器操作、真实供应商调用或 Windows 桌面验收。

## 修复与原始失败

1. [原首次尝试](W02-09-claude-recovery-native/failures/original-first/command.log)等待故障状态 51.94 秒后失败。实际应用已成功，故障标记没有消费。改为遇到意外终态立即失败后，[8.19 秒复现](W02-09-claude-recovery-native/failures/original-observed/command.log)。启动观测确认固定 SDK 使用 `--resume=ID`，旧探针只识别 `--resume ID`。
2. 探针现在兼容等号与分离参数，继续要求原生流式恢复、已持久化的 `original` 阶段、没有恢复计划且候选未启动。只在这一边界模拟启动器退出 72；预检、候选和恢复查询不受影响。[10 个边界检查](W02-09-claude-recovery-native/wrapper-boundaries/result.json)覆盖两种参数形式，以及 bare、原阶段、候选、恢复和仅指定新 ID 的场景。
3. 首批五条原生路径虽通过，实际模型请求仍包含宿主上层的两个指令来源：测试 cwd 位于项目的 `.dev` 下，独立 HOME 不会阻止原生 CLI 搜索 cwd 的祖先。[来源失败记录](W02-09-claude-recovery-native/ancestor-instructions-red.json)保留。探针改用 `/var/tmp` 中的私有目录，避开宿主项目/主目录祖先及 bwrap 遮蔽的 `/tmp`；新增检查会在请求报告了测试目录外指令来源时使探针失败。[原始请求回归](W02-09-claude-recovery-native/instruction-guard-regression.json)确认旧两组均能被检出，新两组不再出现。

这两项修复属于验收夹具。生产恢复语义、来源守卫、退出确认和未知结果处理均未放宽；生产侧完整指令来源枚举仍未实现。

## 最终原生矩阵

| 路径 | 检查数 | 实际依据 |
|---|---:|---|
| 冷原查询启动前失败后明确恢复 | 25 | [结果](W02-09-claude-recovery-native/final-original/result.json.gz) |
| 已有安装绑定下的同类恢复 | 53 | [结果](W02-09-claude-recovery-native/final-installed/result.json.gz) |
| 冷原查询恢复的成功回执丢失 | 27 | [结果](W02-09-claude-recovery-native/final-original-lost/result.json.gz) |
| 本地设置还原后的独立恢复版本 | 25 | [结果](W02-09-claude-recovery-native/final-adoption/result.json.gz) |
| 独立恢复版本的成功回执丢失 | 27 | [结果](W02-09-claude-recovery-native/final-adoption-lost/result.json.gz) |

最终五条均由含参数修复、独立目录和来源检查的探针运行；先前尝试不重复计数。每条包含 A/B 两个配置，核对原会话 ID、已有历史、原配置/版本关系、失败候选归档、暂停保持、一次恢复派发和后续冷输入。恢复成功后的重复命令使用原回执。

丢回执由适配器在原生已保存成功结果后丢弃 Application 所接收的响应；随后通过重启与只读核对收口。这不替代原生确认落盘前崩溃的验收。原查询启动失败由测试启动器注入，恢复和后续输入使用真实 CLI。

## 独立复核与构建

[140 项独立复核](W02-09-claude-recovery-native/audit.json)从原生日记、原/恢复 metadata、初始化与插件协议回复、HTTP 命令回执、供应商实际请求和只读 SQLite 导出核对：

- 原 ID、路由、状态目录与权限保持；历史前缀和先前模型回复进入恢复后的真实请求。
- 恢复只派发一次，重复命令回执一致；SQLite 中保存成功操作及最终快照。
- 原查询早期失败保留 `original` 阶段且没有伪造原组件清单；首次成功清单独立保存在恢复记录中。
- 本地还原保留原内容摘要，以独立版本记录新文件身份；原快照和失败候选继续可追溯。
- 恢复后的原生插件清单与冷输入清单一致；实际包来自测试安装来源，未使用被标记的缓存副本。
- 最终请求未报告测试目录之外的指令文件；原生探针的来源检查也实际执行。

首次审计把所有场景的 A 请求数固定为 2，遗漏已有安装场景的正常前置输入；[原失败](W02-09-claude-recovery-native/audit-first.json)保留。现从原生日记中的真实输入数核对请求次数，没有放宽恢复的唯一派发条件。

本轮重新构建 bridge 与 nativeclaude 测试程序，执行 `go vet -tags nativeclaude ./internal/runtime`、Python 编译和 Go 格式检查，均通过。生产 Go、Web 及依赖未改，因此没有重复上一轮已通过的全量 race/构建，也没有重新运行浏览器。

固定版本为 **Claude Code 2.1.280 / SDK 0.3.280 / Node 24.13.0 / React 19.3.0**。被测 nativeclaude 程序 SHA-256 为 `31f71d72e63f33478d1161bbb3d6c461e41483baea9ff48f8cd5efd26769037a`；[源码与产物摘要](W02-09-claude-recovery-native/manifest.json)绑定到本轮归档。旧 `bin/netcap` 与 15,665 个既有证据文件均[核对未变](W02-09-claude-recovery-native/preservation.json)。[148 个归档文件](W02-09-claude-recovery-native/archive-index.json)按原字节摘要复核；旧目录中的原始请求仍原位保留。

## 复跑与剩余范围

```bash
npm --prefix bridges/claude run build
go test -c -tags nativeclaude -o /var/tmp/netcap-recovery-native.test ./internal/runtime
python3 tests/claude_installed_settings_apply_probe.py --binary /var/tmp/netcap-recovery-native.test --case original-recovery --settings-source custom --policy ordinary
```

其他路径分别替换 `--case` 为 `original-recovery-installed`、`original-recovery-lost`、`adoption-recovery`、`adoption-recovery-lost`。保留 bwrap 隔离和版本检查；每条命令输出其私有证据目录。归档按 [证据约定](README.md)保存在本地，Markdown 摘要进入 Git。

W02.07/W02.08/W02.09 仍为 `in_progress`，W 系列仍 **9/40 done、8 in_progress、23 pending，Goal active**。完整受管/remote/helper 来源真实性与新鲜度、动态权限、外部竞态、四配置联合、真实方法/Gate、严格前台与输入权、Windows 及其他首版硬要求均保留。下一步继续完整来源/权限边界；本轮发现的父目录指令来源也应在生产来源覆盖中单独考虑，不能将验收夹具的隔离修复算作生产覆盖完成。
