# W02.08：明确按原配置恢复查询

2026-10-06，Claude Code 2.1.280 / SDK 0.3.280。原本地设置采纳失败后，用户现在可先只读核对，再明确按原 ID 恢复原配置。原 Scope 与执行快照不升级；失败候选、原失败诊断、恢复操作与时间单独归档，自动化继续暂停。能力库保留失败候选的引用位置。

## 行为与边界

- 新命令 `recover_execution` 只引用已保存的原快照、失败候选和原应用操作。App 在准入、派发前及成功归档前核对原来源/路由；Runtime 与 Session 再核对失败事务、已退出的自有查询、未决工作、历史和文件版本。
- 独立恢复意图先落盘，原应用操作仍保留 `uncertain`。原生先持久化 `recovery.status=recovering`，再重开原配置；核对原 ID、历史、设置阻止机制与原能力/撤去候选组件后提交 `recovered/verified`。不能只靠状态字符串解除未知。
- 成功才把完整失败候选移到 `execution_change_attempts`，原应用记为失败，恢复操作记为成功；不加入实际生效历史。人工接管不被覆盖，自动化保持暂停。
- 明确未发送才释放恢复标记；prepared 重启同样只释放恢复意图。派发后结果未知、恢复启动再次失败或回执丢失均只读核对，不再次启动。原应用失败与恢复失败分别绑定操作 ID。
- 原文件需先恢复到记录的版本。重开会再次运行原配置启动 hooks；此前副作用不会撤销。本片没有自动修改用户设置文件，没有模型输入，不证明完整原生配置、受管策略、全局输入独占或真实模型历史恢复。

## 验证

| 检查 | 实际结果 |
|---|---|
| 真实 App/SQLite/Runtime/Session，schema 失败后恢复 | 36/36 |
| 候选启动后来源漂移，关闭工作台后明确恢复 | 39/39 |
| 原恢复成功但 App 传输回执丢失 | 36/36；只读确认、一次启动 |
| 原配置恢复启动后再次漂移 | 29/29；独立失败诊断、重启 unknown、不重放 |
| App 恢复边界 | 原 16 + 新 7 类；含派发/归档前漂移、人工接管、明确未发送、prepared 重启 |
| Runtime 恢复证明和失败绑定 | 12 类；相关 Claude race 通过 |
| Session 公开 API | 13 类；插件公开 API 19 类回归通过 |
| HTTP 命令边界 | 新 4 类及已有授权/同源检查通过 |
| 能力库失败尝试引用 | 1 项通过 |
| 浏览器 | 20/20；真实 App 响应回放、桌面/390px 截图已查看 |
| 独立 SQLite/metadata/journal/hooks 核对 | 56/56 |
| 构建与静态检查 | App/console 全包 race、相关 runtime race、vet、bridge/Web/开发构建通过 |

真实四例的 [最终日志](W02-08-claude-explicit-recovery/native-complete.log)、[原始目录](W02-08-claude-explicit-recovery/original-directories.json)、[独立审计](W02-08-claude-explicit-recovery/audit.json)、[浏览器结果](W02-08-claude-explicit-recovery/browser-reviewed.json)、[恢复失败截图](W02-08-claude-explicit-recovery/unknown-1440.png)、[手机归档截图](W02-08-claude-explicit-recovery/recovered-390.png)及 [source diff](W02-08-claude-explicit-recovery/source.diff)已保存。中间出现测试类型名错误、浏览器文件根限制和回放缺少刷新事件，已修正并保留[记录](W02-08-claude-explicit-recovery/intermediate-results.json)；没有改变权限或放宽生产准入。

开发二进制 SHA256：`7a2398f8df24d7eede60e7518c98f4d6f63b812529338511e5c32f092a0a46ca`。4092 个旧证据文件及旧 `bin/netcap` 保持原摘要，见 [清理核对](W02-08-claude-explicit-recovery/cleanup.json)与 [manifest](W02-08-claude-explicit-recovery/manifest.json)。仅本地修改，未提交/推送或对外操作。

## 后续

W02.07/W02.08 仍为 in_progress，9/40 done，Goal active。继续验证混合能力失败候选的原配置恢复、原生进程内部提交/回执丢失边界与真实模型历史；全部有效/受管来源、方法/Gate、全局输入独占、Windows/proxy 及五项既有 WSL 请求仍待完成。此片不关闭整项。
