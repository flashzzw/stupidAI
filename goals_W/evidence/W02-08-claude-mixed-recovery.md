# W02.08：混合能力恢复与 Bridge 确认丢失

2026-10-06，Claude Code 2.1.280 / SDK 0.3.280。本片验证已有的明确恢复实现，不修改生产代码、界面或构建。原本地设置采纳失败后，恢复原 ID 和原配置还必须恢复原 Skill/CLI 插件版本、撤去失败候选组件，并区分持久恢复结果与当前查询加载状态。

## 实际路径

真实 Application/SQLite/Runtime/Session 使用隔离项目、profile、状态目录、独立 Skill 和包含 Skill/SessionStart hook 的 CLI 插件。所有供应商指向不可用 loopback 地址；没有模型输入或模型结果。设置 v2 的启动 hook 在开始后把文件改成 v3，以形成已有的真实失败场景；随后恢复原 v1 文件并明确请求恢复。

| 新场景 | 结果 |
|---|---|
| 原能力禁用，失败候选启用：恢复后撤下候选 | 45/45 |
| 原能力启用，失败候选禁用：恢复原能力 | 45/45 |
| 原资源 v1，失败候选 v2：恢复原文件来源与声明版本 | 45/45 |
| Bridge 提交恢复后、输出协议回执前退出 | 46/46 |
| App 已派发，Bridge 在开始原生恢复前退出 | 33/33 |
| 丢回执恢复后明确冷重开原配置 | 48/48；冷重开脚本另有 12/12 |

六例共 262 项；既有混合应用、只读条件、明确恢复/再失败和冷恢复共 224 项相关原生回归通过。测试夹具改用具名参数，避免继续追加位置布尔值；旧场景行为由回归核对。

Bridge 退出由隔离包装脚本在公开 `Session.recoverExecution` 边界注入。提交后退出例先确认磁盘 `recovered/verified` 与恢复操作匹配，再关闭自有 query，退出 Bridge，确实省略 RPC 回复；App 从持久记录核对成功，没有重开。开始前退出例没有原生 recovery 记录，App 保持 uncertain、原候选和暂停，重启及新恢复请求均不重放。包装脚本没有生产故障开关，未改原 Bridge/CLI 文件。

这证明的是工作台自有 Bridge 的持久提交与协议回执窗口，不等于 Claude CLI 内部事务、SIGKILL/断电或在途模型/工具的完整恢复。明确冷重开是测试单独发起的操作，原 v1 启动 hook 因此多执行一次；普通 App 重启不执行该动作。

## 对照与拒绝条件

- 相同资产、不同版本分别保存在原快照与失败候选中，失败候选不进入实际生效历史；能力库继续列出其引用。
- v1/v2 插件启动标记与配置环境逐项匹配；冷重开仍加载原插件路径/1.0.0 声明和原 Skill 描述，候选专属方法不再出现。
- Bridge 已退出时恢复操作可以有持久成功依据，但当前 Skill/插件证据保持未核对，设置阻止机制不显示已注册。浏览器桌面/390px 共 26 项通过；仅回放真实 App 响应，无浏览器发起的原生执行。
- Runtime 新增 17 个反例/对照，覆盖候选 Skill 名称、别名、描述、插件包/hook/MCP 残留，当前或持久清单缺失，以及同名插件的错误版本/路径；恢复相关共 29 类 race 检查通过。MCP 在本片仅为受控负向投影测试，没有真实 MCP 调用。
- 独立读取 SQLite、metadata、journal、hook 标记共 93 项通过。首版审计错误地假定原生只有一个插件包；原始清单同时包含独立 Skill 的加载包。已按两个原资产的精确包名集合核对，并保留[首次审计](W02-08-claude-mixed-recovery/audit-first.json)，没有忽略额外包或放宽生产验证。

可查看[原生最终日志](W02-08-claude-mixed-recovery/native-final.log)、[两个 Bridge 退出窗口](W02-08-claude-mixed-recovery/native-exit.log)、[明确冷重开](W02-08-claude-mixed-recovery/native-cold-final.log)、[独立审计](W02-08-claude-mixed-recovery/audit.json)、[浏览器结果](W02-08-claude-mixed-recovery/browser.json)、[离线手机截图](W02-08-claude-mixed-recovery/offline-390.png)和[来源目录](W02-08-claude-mixed-recovery/original-directories.json)。相关带标签 vet 通过；本轮没有产品修改，因此未重复构建或无关全包测试。

[Source diff](W02-08-claude-mixed-recovery/source.diff)、[清理核对](W02-08-claude-mixed-recovery/cleanup.json)与 [manifest](W02-08-claude-mixed-recovery/manifest.json)记录本片测试/文档及产物摘要。4153 个旧证据文件、生产代码、bridge/Web 构建和两个二进制不变，所有自有进程与浏览器已收尾，未提交或推送。

W02.07/W02.08 仍 in_progress，9/40 done，Goal active。下一片转向已有 Codex 改配在原生确认丢失后的核对/原 ID 接续缺口。真实模型历史、方法/Gate、MCP 混合真实调用、完整受管/安装来源、全局输入独占、Windows/proxy 与五项既有 WSL 请求继续保留；此片不关闭整项。
