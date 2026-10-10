# W01.04 / W01.05 · 原生函数 hook、草稿与待执行输入撤回

2026-10-11。固定 Claude Code **2.1.280** 在启动环境启用 `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1` 后，能在原前台或原生后台会话内明确安装测试插件，保持原完整 UUID、PID 和启动标识，读取草稿并提交原生插件输入。两个自有测试插件还取得了人工输入后撤回待执行消息的局部正例。最终四组 **127 项原生观察、229 项独立复核、四组本机生成类型检查通过**。

这是隔离原生实验，**没有开放生产控制，也没有完成持久输入权交接**。默认旧前台仍不能通过运行中补写开关启用函数 hook；函数 hook 发生异常时原生会放行输入。原生 attach 自身不提供仲裁的[前一片反例](W01-04-native-input-control.md)继续成立，W01.04/W01.05 保持 `in_progress`，整体仍为 **9/40 done**。

## 启动前提与后置安装

| 实际条件 | 实测结果 |
| --- | --- |
| 默认前台、默认原生后台，预先提供测试插件 | `plugin validate` 能识别模块，传统 `UserPromptSubmit` 执行；函数模块不改写、不拒绝输入，`/plugin-types` 不可用 |
| 启动环境开启开关，预先提供模块 | 原生改写与拒绝均生效，能生成类型，原 PID/完整 ID 保持 |
| 原会话启动时没有插件，通过另一个 CLI 安装，再在原会话 `/reload-plugins` | 所测有/无启动开关两组均重载 0 插件、0 hook；外部安装命令成功不能证明原会话已加载 |
| 启动环境已开启开关，原会话启动时没有测试插件；在原会话明确安装并确认用户作用域 | 后置加载成功，原 PID/完整 ID 保持；不是预加载 netcap 插件或 wrapper 的样本 |
| 启动时没有开关，运行中在 `settings.env` 写入开关，再在原会话明确安装 | 传统 hook 生效，函数模块仍不生效。正式脚本再次取得同样结果，不能把它称为默认旧会话已经支持 |
| 同一 `hooks.json` 指定两个模块文件 | 原生校验明确拒绝：`modules` 只接受一个 hooks module。独立来源观察改用第二个自有插件 |

原会话安装流程使用自有本地 marketplace：`/plugin marketplace add <自有目录>`、`/plugin install <自有插件>@owned-functions`，匹配实际详情页后仅确认 `Install for you (user scope)`，再重载。最早一次脚本没有确认安装页而超时，保留为夹具失败，不算原生安装能力失败。九次可用性实验中八次完成观察，一次停在上述确认页；原始请求、传统 hook 记录及启动参数的[只读核对](W01-04-native-function-hooks/work/availability-audit.json.gz)保留了这些差别。

CLI 自己在 `plugin test --help` 提示功能处于 early access、需设置上述环境变量。文档依据为官方 [Mods README](https://github.com/anthropics/claude-code/blob/main/mods/README.md)、[插件参考](https://code.claude.com/docs/en/plugins-reference)和实际生成声明。GitHub `v2.1.280` tag 的声明首行仍写 **2.1.277**，不能代替本机版本；最终每次均在原会话 `/plugin-types` 生成 **2.1.280** 声明，再用它检查实际加载的模块。四份主声明 SHA-256 均为 `5b85b402a717841acae3f80ef8895922201c2533d821d198cb64c3dfe7cf47a8`。Context7 查询、实际检索 URL/状态和版本文件留在本地归档。

## 最终原生样本

四组都没有在启动时预加载测试插件，均在原会话内明确安装。每组保有独立 profile、PTY、本地供应商和完整原生身份；以下统计只计最终正式脚本，不把早期重跑累加为最终覆盖。

| 样本与本地结果 | 原生观察 | 独立复核 | 主请求 / 辅助请求 |
| --- | ---: | ---: | ---: |
| [独立前台，模型响应在途，只观察](W01-04-native-function-hooks/final-foreground-baseline/result.json.gz) | 28 | 55 | 7 / 4 |
| [原生后台，模型响应在途，只观察](W01-04-native-function-hooks/final-background-baseline/result.json.gz) | 28 | 55 | 7 / 18 |
| [独立前台，模型响应在途，撤回](W01-04-native-function-hooks/final-foreground-withdrawal/result.json.gz) | 33 | 56 | 6 / 4 |
| [原生后台，真实 Bash 在途，撤回](W01-04-native-function-hooks/final-background-tool/result.json.gz) | 38 | 63 | 6 / 14 |

原始目录分别为 `/var/tmp/nc-native-prompt-wn6nf2e4`、`/var/tmp/nc-native-prompt-svoyfblv`、`/var/tmp/nc-native-prompt-rq7tb_kk`、`/var/tmp/nc-native-prompt-fi3tytn2`。完整 UUID、PID/启动标识、计数和类型摘要见[汇总](W01-04-native-function-hooks/work/summary.json.gz)。四组共 26 次主模型请求、40 次标题/状态等辅助请求，全部由本地脚本回答；不是真实模型方法或 Gate 验收。

实际行为及其范围：

- **草稿保留**：终端输入 `NETCAP_NATIVE_INPUT=human_draft 中文🧪` 后，`$.prompt.read()` 返回完整文本和 UTF-16 光标 36。插件提交独立输入前后，草稿及光标相同；草稿只在后续终端 Enter 后进入模型。`prompt.read()` 在没有输入框时也可能返回空值，因此没有用空值证明不存在草稿。
- **原生来源**：程序调用的回执和原生持久消息带 `{kind: 'plugin', name: 'netcap-input-observer-fixture'}`。所测提交模块没有收到自身的 `prompt.submit`，独立测试插件收到该来源。所测前台及 attach 的终端 Enter 被标为 `composer`；没有据文本前缀认证来源，也没有验证任意 socket 输入的来源。
- **排队与回执**：当前轮次未结束时，`$.prompt.submit` 保持等待；独立插件在原轮次完成、原生取出排队消息时才收到该消息事件。只观察的两组中，插件消息仍先于后来的人工消息执行。调用超时不能当成未发送或已取消，`next(e)` 的类型注释不能代替调用侧时序观察。
- **受控撤回**：测试见证插件记录真实 `composer` 输入携带的原在途 `turnId`；在原生取出待执行消息时，仅对指定测试发送插件返回 `{drop: ...}`。两个撤回样本中，待执行及之后的一次插件输入都取得拒绝回执，没有进入模型或新轮次；原在途轮次正常结束。这是两个自有插件的实验策略，没有持久控制租约、重启恢复或生产认证通道。
- **原工具与人工文字**：后台样本先记录真实审批等待，再明确单次批准自有 Bash。人工输入后原工具仍活着，实际启动只有一次；原调用的完整 JSON 输出回到原 `tool_use_id`。人工文字在此路径随工具结果进入正在运行的轮次，没有另一个人工 `turn.start`；原生日记记录该输入的入队及移除，不能按“一条输入等于一个新轮次/独立聊天消息”统计。附加人工文字和原生 token 提醒没有被误当成原工具 stdout。
- **异常不会自动拒绝**：显式 `{drop}` 没有模型请求；故意抛错的 hook 则允许原输入进入真实原生轮次。文档所述失败后放行已实测，不能只靠一个 hook 宣称可靠输入仲裁。

`$.session.messages()` 的普通结果没有原生消息 UUID，程序提交回执也没有原生消息或轮次 ID。本片通过唯一的测试标记、原始请求、原生日记和事件交叉核对实验归属；没有把这套标记比对推广为重复真实用户输入的生产关联协议。

## 复跑与独立证据

入口为[可用性探针](../../tests/claude_native_function_hooks_probe.py)、[原生输入探针](../../tests/claude_native_prompt_probe.py)、[提交模块](../../tests/claude_native_prompt_module.ts)、[见证模块](../../tests/claude_native_prompt_witness.ts)和[独立审计](../../tests/claude_native_prompt_audit.py)。原生探针最终会生成当前 CLI 的声明，并检查实际加载模块；独立审计只读已有文件与进程身份，不启动 CLI、不重发模型输入，也不使用探针的通过布尔值替代原始证据。

```bash
python3 tests/claude_native_function_hooks_probe.py --foreground --late-plugin --enable-after-start --in-session-install
python3 tests/claude_native_prompt_probe.py --witness plugin
python3 tests/claude_native_prompt_probe.py --witness plugin --background
python3 tests/claude_native_prompt_probe.py --witness plugin --revoke-queued
python3 tests/claude_native_prompt_probe.py --witness plugin --background --revoke-queued --inflight tool
python3 tests/claude_native_prompt_audit.py /var/tmp/nc-native-prompt-fi3tytn2 --output /var/tmp/native-prompt-audit.json
```

最后一条目录替换为对应探针实际打印的目录。依赖当前固定 CLI、项目已安装的 TypeScript、bwrap、用户命名空间、PTY 与本地 IPC。外层使用自有 `/var/tmp` 目录、独立 profile、自有 `/etc/claude-code` 和注册表缺席夹具；不隔离 PID/网络，不修改用户配置。原生审批只确认一次自有工具，没有选择永久允许或绕过权限模式。

四份独立结果位于本地归档的 `work/audit-*.json.gz`。除上述验证外，Python 语法、差异和文档引用检查通过。没有修改 Go、bridge、Web、依赖或构建产物，没有重跑无关的 Go 全包、浏览器或 Windows 验收。

开发二进制仍为 `5c86bc5339ed3a9c543b155bde4aed4553de8a366f39aebfc97f938e94a9cddb`；旧 `bin/netcap` 仍为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。22 个本轮实验目录中的 29 个已采样自有进程均确认退出，未找到仍以这些目录为 cwd/参数路径的进程，见[清理核对](W01-04-native-function-hooks/work/process-audit.json.gz)。

原始材料按明确文件白名单压缩归档，根目录 `.gitignore` 排除整个原始材料目录；**不复制原生 profile 整目录，不归档 daemon/session 私钥**。源码、版本、原始字节及压缩产物摘要见[归档清单](W01-04-native-function-hooks/manifest.json)。Git 只同步测试源码和 Markdown 报告，从远程克隆不包含这些本地原始材料。

保留的失败包括：早期安装页未确认、两个模块被原生校验拒绝、误期望提交模块收到自身事件、误期望在途程序调用立即返回，以及把后台状态/标题请求误计为重复主请求。工具样本的首次结果采集跳过了合并人工文字的工具回执；原始请求已包含完整输出，修正的是夹具采集。模板字符串的类型检查失败和早期结果缺少新字段的汇总错误也保留；没有将这些夹具问题归为生产故障或放宽原输出、次数、身份要求。

下一片需要核对模块失效/卸载/重载与连接中断、原生回执与持久 Operation 的唯一关联、未决审批和未知占用，再判断生产 Application 接入条件。默认旧前台、严格输入交接、worker 丢失前台工具回执后的完整恢复、完整配置/权限/冻结、第二有效 Windows/WSL 环境及所有首版硬要求继续保留。Goal 仍为 active。
