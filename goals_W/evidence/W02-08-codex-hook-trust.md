# W02.08 · Codex hook 原生摘要、信任键与缓存定义

2026-10-05。固定 app-server 0.158.0，隔离 profile 下的九个插件缓存样本、两个自有后台，**62 项原生契约检查通过**。本片为后续 hook 选配核对必要的身份/版本条件，未接通产品 hook 选配，也未执行 hook、模型或工具。W02.08 in_progress，9/40 done，Goal active。

## 结果及对实现的约束

| 对照 | 原生结果 | 后续选配必须保留的区别 |
|---|---|---|
| 相同常量命令，复制到不同插件 ID / hook 相对路径 | 原生摘要相同，完整 key 不同 | 摘要不能代替来源身份；一个 key 的信任不会授予其他副本 |
| 两份相同 `${PLUGIN_ROOT}` 原始命令，位于不同缓存目录 | 返回的 command 展开为各自路径，原生摘要仍相同 | 不用展开后的显示命令重新计算摘要 |
| 字面绝对路径命令与变量命令展开后的文本相同 | 原生摘要不同 | 相同显示文本不能证明相同原始定义 |
| 命令 `true` 改成 `false`，或 timeout 从 1 改成 2 | 独立控制样本的摘要不同 | 审核依据需要包括实际定义，不能只按事件名 |
| 已信任 key 对应的磁盘定义变化 | 同后台 `hooks/list` 仍返回旧命令、旧摘要、trusted；自有后台重启后返回新命令、新摘要、modified | 原生目录清单可能反映缓存，不能独自证明磁盘版本 |
| 只修改命令所指脚本文件的正文 | 冷启动后的原生摘要和 trusted 均保持 | hook 摘要不等于整个插件资源摘要；完整文件树版本核对仍必要 |
| 只改变 hooks JSON 的缩进和字段排列 | 冷启动后原生摘要保持 | 它不是该 JSON 文件的原始字节 SHA256 |
| 禁用一个 hook | 该项仍可发现，enabled=false；modified 状态保留 | 禁用不等于重新授信 |
| 禁用整个插件 | 该插件的 hook 从目录清单缺席，其他来源保留 | 空目录结果不能单独证明资源不存在 |

上述结论只覆盖列出的固定样本，不提供通用摘要算法。后续应保存原生返回的完整 key/currentHash，并结合独立资源版本核对；不能从命令文本、事件名、hash 或 sourcePath 中任选一个推断完整资产归属。整包选配、原生信任、完整包版本以及实际执行结果仍是不同状态。

## 逐线程读取边界

九个副本预先存在时，只在 profile 中启用的一个来源出现在最初目录清单。`thread/start` 通过线程配置增加另一个缓存来源，随后 `hooks/list` 的结果仍与原 profile 清单相同。此实验只验证目录查询不随该线程覆盖改变；没有发送输入，不声称第二个来源已在该线程执行。

固定版本的 [HooksListParams](W02-08-codex-hook-trust/schema/HooksListParams.ts)只有 `cwds`；[插件预览 hook 摘要](W02-08-codex-hook-trust/schema/PluginHookSummary.ts)只有 key/eventName，不提供 currentHash。为取得各控制样本的元数据，本实验显式修改了**临时 profile** 的 plugins/hooks.state；这些写入不构成产品应修改用户配置的方案。下一片需要核对能为选配副本提供原生元数据、且不改用户 profile 或原线程的准备入口，再接生产选择/信任交互。

## 执行与验证

```bash
python3 tests/codex_hook_trust_probe.py
```

[可重跑探针](../../tests/codex_hook_trust_probe.py)复用已有 Stdio 和隔离环境。最终目录 `/tmp/netcap-w0208-hook-trust-gg0sqwod`；[62 项结果](W02-08-codex-hook-trust/native/result.json)、[首次后台协议](W02-08-codex-hook-trust/native/initial-protocol.json)、[冷启动协议](W02-08-codex-hook-trust/native/cold-protocol.json)、最终缓存文件与原始文件哈希均已归档。恰有三个计划内测试文件变化：copy-a 的命令、moved 的 JSON 排版、script 的正文。每次目录读取前后都检查临时 profile 字节不变；控制用 profile 写入单独记录。两个最终后台退出 0，唯一测试线程在未输入时取消本连接订阅。

最初 57 项样本与首次 62 项样本分别保留，不累计为最终计数。新增路径展开对照时，“展开后的路径会改变原生摘要”的假设被实测推翻，失败结果和日志保存在 `earlier/expanded-path-assumption-failed/` 与 `logs/native-final.log`。随后增加字面路径独立对照，最终预期依据这两个真实观测建立。`earlier/exploration/` 对应的最初探索仅采样，没有检查汇总，不能当作通过验收。

Python AST 检查通过；没有修改生产 Go/Web 或构建，因此未重复 race、浏览器或开发构建。所有原生动作都在隔离 fixture 中完成，没有 turn/start、命令调用或 hook 事件；原生插件后台启动可能尝试内置远程目录元数据，与成功模型结果不同。资料来自 [OpenAI Docs hooks](https://developers.openai.com/codex/hooks)和[插件说明](https://developers.openai.com/plugins/build/plugins)，摘录见 [docs.txt](W02-08-codex-hook-trust/docs.txt)；资料没有规定缓存刷新与完整包哈希，上述边界以固定版本实测为据。

[审计](W02-08-codex-hook-trust/audit.json)、[差异](W02-08-codex-hook-trust/changes.diff)和[归档时点哈希](W02-08-codex-hook-trust/manifest.json)核对旧证据、两个二进制、源码与附件。生产行为保持原状，未提交。hook 成功执行的 WSL 对照及既有三个终端结果仍待返回，不重复请求；异步、受管来源、实际组件作用/失败归属、Windows/proxy、方法/Gate 和输入独占等硬要求继续保留。
