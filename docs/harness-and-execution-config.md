# Harness 原则与 CLI 执行配置：设计依据

日期：2026-09-30。本文承接 [产品设计](product-design.md) 的新增需求：每次执行可选择 Skill、MCP、CLI 插件；多套供应商配置并发且会话隔离；共享能力统一管理；平台功能插件与 CLI 插件分开。

资料范围：Harness Books 两册目录与主体章节，重点对照控制结构、上下文、工具、恢复、协作及本地规则；另取回 OpenAI 与 Claude 官方配置文档。书中的源码路径、伪代码、阈值与接口属于作者分析时的快照，不能用作当前 CLI 公共契约。下文分别标明设计判断与官方事实；本轮未修改或运行用户的 CLI 配置。

## 1. 对两本书的理解

第一册 [Harness Engineering: A Design Guide to Claude Code](https://harness-books.agentway.dev/en/book1-claude-code/index.html) 主要解释：一个能连续工作的 Agent，如何由指令组织、运行循环、权限、上下文、恢复与验证共同维持秩序。第二册 [The Harness Design Philosophies of Claude Code and Codex](https://harness-books.agentway.dev/en/book2-comparing/index.html) 比较这些责任放在哪里：作者概括 Claude 更强调运行现场的循环与恢复，Codex 更强调明确的指令单元、策略、thread 与状态记录。这是侧重点比较，不是说其中一方没有另一方的能力。

对本产品最重要的取舍是：**平台明确管理执行选择、身份、能力来源、协作与交付；原生 CLI 继续负责自己的模型循环和工具运行语义。** 不再往 Lead 的提示词里塞进所有插件与规则，也不重造两套 CLI 的内部执行器。

### 第一册逐章落点

| 章节 | 本产品采用的判断 | 对设计的影响 |
|---|---|---|
| [1 · Why Harness Engineering Matters](https://harness-books.agentway.dev/en/book1-claude-code/chapter-01-why-harness-engineering.html) | 输出看起来正确不证明执行可靠 | 完成由实际产物与 Gate 判定；资源选择必须检查生效结果 |
| [2 · Prompt Is the Control Plane](https://harness-books.agentway.dev/en/book1-claude-code/chapter-02-prompt-is-control-plane.html) | 指令来源、作用域与组合规则需要明确 | 展示项目规则、角色指引、Skill 与任务要求各自来源，不用一段大提示词掩盖冲突 |
| [3 · Query Loop](https://harness-books.agentway.dev/en/book1-claude-code/chapter-03-query-loop-heartbeat.html) | 连续执行需要跨轮状态及不同停止原因 | 空闲、等待审批、被取消、失败、已验收分别记录；平台不以进程退出推断任务完成 |
| [4 · Tools, Permissions, Interrupts](https://harness-books.agentway.dev/en/book1-claude-code/chapter-04-tools-permissions-interrupts.html) | 能调用不等于已获授权；中断也需要交代结果 | 安装、选择、加载、允许调用分开；取消后保留已执行与未知动作 |
| [5 · Context Governance](https://harness-books.agentway.dev/en/book1-claude-code/chapter-05-context-memory-compact.html) | 上下文有预算，不是仓库 | Skill 先暴露必要目录信息，再按需读取；长输出保留产物引用；恢复时补齐约束与当前工作 |
| [6 · Errors and Recovery](https://harness-books.agentway.dev/en/book1-claude-code/chapter-06-errors-and-recovery.html) | 恢复必须有条件、计数和停止出口 | 连接重试、业务重试、CLI 原生恢复分开，平台不叠加无限重试或重复“继续” |
| [7 · Multi-Agent and Verification](https://harness-books.agentway.dev/en/book1-claude-code/chapter-07-multi-agent-and-verification.html) | 分工隔离不确定性，Lead 必须综合结果 | 子任务有明确契约；Lead 消化发现后派发，Reviewer 使用独立上下文和当前产物 |
| [8 · Team Adoption](https://harness-books.agentway.dev/en/book1-claude-code/chapter-08-team-landing-practices.html) | 先建立可复用的最小流程，不追求规则数量 | 先验证少量 Skill/MCP/插件的真实用途和 Gate，再扩容资产；不为个人产品增加企业审批层级 |
| [9 · Ten Principles](https://harness-books.agentway.dev/en/book1-claude-code/chapter-09-ten-principles.html) | 能力价值取决于系统约束和验证 | 将原则映射成产品验收，而不是只在文档写“遵循 Harness” |

### 第二册的关键补充

| 章节 | 对本产品的约束 |
|---|---|
| [1 · Why Compare](https://harness-books.agentway.dev/en/book2-comparing/chapter-01-why-this-comparison.html) | 选型比较责任划分与失效行为，不能只比较 Agent 和插件数量 |
| [2 · Two Control Planes](https://harness-books.agentway.dev/en/book2-comparing/chapter-02-two-control-planes.html) | 保存指令/资源来源和作用域，CLI 适配层分别解析原生组合语义，不发明一套通吃的配置优先级 |
| [3 · Loop, Thread and Rollout](https://harness-books.agentway.dev/en/book2-comparing/chapter-03-loop-thread-and-rollout.html) | 产品任务记录与 CLI 原生状态各有责任；恢复要同时核对，不只回放聊天摘要 |
| [4 · Tools and Policy](https://harness-books.agentway.dev/en/book2-comparing/chapter-04-tools-sandbox-and-exec-policy.html) | MCP 和插件带来的工具也必须服从有效权限；目录分离不是执行沙箱 |
| [5 · Skills, Hooks and Local Governance](https://harness-books.agentway.dev/en/book2-comparing/chapter-05-skills-hooks-and-local-governance.html) | 能力作为有来源与版本的资产管理；hooks 是有触发时机与副作用的行为，不是 Skill 文本 |
| [6 · Delegation and State](https://harness-books.agentway.dev/en/book2-comparing/chapter-06-delegation-verification-and-state.html) | 委派、控制交接与回收留下可核对记录；不能由实现者自我认证 |
| [7 · Convergence and Divergence](https://harness-books.agentway.dev/en/book2-comparing/chapter-07-convergence-and-divergence.html) | 不把动态灵活性和强一致控制全部堆在一层；先确定唯一责任方 |
| [8 · How to Choose or Build](https://harness-books.agentway.dev/en/book2-comparing/chapter-08-how-to-choose-or-build.html) | 当前最需解决的是多配置、多能力、多会话的来源和边界，先做明确的外层控制，再补最小必要的恢复接缝 |

不直接采用书中的固定 token 阈值、重试次数、周计划、强制父进程退出时杀子进程等示例。本产品的窗口关闭与父 Agent 中断有不同生命周期；已有授权、原生 CLI 能力和任务产物保留要求优先。

## 2. 官方配置事实及其边界

| 问题 | 官方资料支持的事实 | 产品必须补的保证 |
|---|---|---|
| Codex 多套配置 | `CODEX_HOME` 指向本地配置/状态目录；配置还能有 profile、项目、命令行等来源；认证可在文件或 OS credential store [O1][O2] | 分别启动和连接正确实例，核对实际配置与认证来源；不同目录不自动证明外部凭据设施完全隔离 |
| Codex Skill 来源 | 官方列出项目与用户 `.agents/skills`、管理/系统位置；同名不自动合并；可通过 `skills.config` 禁用指定 Skill [O3] | 切换 `CODEX_HOME` 后仍盘点其他发现路径，不能假设只看该目录下的 Skill |
| Codex MCP | 原生 `mcp_servers` 支持 server 启停、required、工具 allow/deny 及环境变量引用等 [O4] | 区分共享定义、执行连接、认证与工具可用状态；指定排除项必须验证确实未暴露 |
| Codex 插件 | CLI 有插件安装/启停入口；插件可以包含 Skill、MCP、hooks；安装后相关能力可能需要新会话才可用 [O5] | 记录实际支持的接口与生效时机，不把全局安装当作本次执行专属选择 |
| Claude 指定配置文件 | 标准文件名为 `settings.json`；`--settings` 接受文件路径或内联 JSON，覆盖所设置的键，遗漏键仍从其他来源继承 [C1][C2] | 用户自定义文件可以有自己的名字；不能把指定一个文件误当作替换全部配置或隔离会话 |
| Claude 本地状态 | `CLAUDE_CONFIG_DIR` 可重定位 settings、session history、plugins 等本地目录；还有全局、项目和 managed 来源 [C1] | 多供应商执行同时绑定配置文件、独立状态目录与进程环境；明确实际读取的额外来源 |
| Claude 每次 MCP/插件 | `--mcp-config`、`--strict-mcp-config`、`--plugin-dir`、`--setting-sources` 提供相关入口，且有版本/managed 策略限制 [C2][C3][C4] | 不把 `--plugin-dir` 的追加加载当作禁用了其余已安装插件；不把 `--setting-sources` 当作关闭所有规则、记忆和工具的总开关 |
| Claude Skill | Skill 可来自用户、项目、嵌套目录、插件和账号同步；有渐进发现与部分热更新行为 [C5] | 区分已选择、实际发现、已加载与已使用；共享源更新不能无声改变在途执行 |

这些事实来自研究当日官方网页，不代替用户安装版本的行为测试。配置能力需要分别适配 Codex 与 Claude；文档中的当前 profile、Skill、插件规则不得反向修改用户旧版配置。

## 3. 产品模型：定义可以共享，运行身份必须明确

一次执行由五个独立选择组成：**Role/Profile + CLI 执行配置 + 本次能力绑定 + 项目/工作范围 + Gate**。模型供应商路由属于执行选择，不能用角色名代替；平台功能插件属于工作台功能，不能混入原生 CLI 插件清单。

| 示例执行配置 | Backend | 原生配置来源 | 本地状态归属 | 能力选择 |
|---|---|---|---|---|
| Codex / 供应商 A | Codex | A 的 `CODEX_HOME` 与所选配置层 | Codex-A 命名空间 | 共享 Skill/MCP + 本次选定 Codex 插件 |
| Codex / 供应商 B | Codex | B 的 `CODEX_HOME` 与所选配置层 | Codex-B 命名空间 | 可引用同版本共享资产，执行绑定独立 |
| Claude / 供应商 A | Claude | A 的 settings 文件与所选继承来源 | 独立 Claude-A 配置/状态目录 | 共享 Skill/MCP + 本次选定 Claude 插件 |
| Claude / 供应商 B | Claude | B 的 settings 文件与所选继承来源 | 独立 Claude-B 配置/状态目录 | 可引用同版本共享资产，执行绑定独立 |

表中均为产品示例，不是真实路径或已创建环境。会话定位至少绑定“执行环境 + 原生状态命名空间 + 原生会话 ID”；显示名、供应商名称或原生 ID 单独都不足以定位。两个配置别名指向同一状态目录时应显示共享关系，不能谎称隔离。

供应商配置隔离、会话/历史隔离、文件工作区隔离、权限沙箱是四个维度。同一个项目的两个写任务仍需工作区约束；不能因为使用不同 `CODEX_HOME` 或 Claude settings 文件就允许无协调地改同一份代码。

## 4. 每次选择的实际语义

能力资产库管理 Skill 正文与引用资源、MCP 定义、原生 CLI 插件包及其组件。共享区表示资产可被两类 CLI 引用；CLI 专属区表示其目标工具与适配边界，不保证同名包可跨工具加载。

选择发生在启动任务/普通会话时，结果生成不可变快照；重连沿用原绑定。续聊或恢复前仍可提出变更，但界面必须说明该 CLI 能否在原会话中安全生效、是否需要空闲/重启、是否会失去原上下文。显式改配生成新快照并记录生效轮次，保留旧快照；做不到时保留原会话并说明，不能静默新建。

有效配置预览同时列出：本次明确选择、从项目/原生配置继承、由插件带入、不可覆盖的受管项、实际拒绝/未验证项。平台的选择意图不能冒充 CLI 生效结果。用户明确要求排除的可选能力若无法确保排除，该组合不通过严格选择验收；不可覆盖的原生约束需明示，不能伪装可取消。

CLI 插件可以一次带入 Skill、MCP、hooks、子 Agent 等组件。选择插件时展示完整组成和来源；不可单独禁用的组件按整体处理。必要 MCP 初始化失败则不启动依赖它的任务；可选 MCP 失败可报告降级，但不能把依赖它的 Gate 标通过。

资产入库、安装、绑定、实际加载和调用是不同状态。Skill 被选中不保证模型已经调用；MCP 已启动不保证所有工具有权限；插件可见不等于已认证。它们必须有分别可核对的状态。

## 5. 避免跨执行污染

- 共享资产采用明确版本与引用；在途执行使用的材料不能指向会被自动覆盖的 `latest` 文件。已有 CLI 热更新无法固定时，监测变化并标记配置漂移，重新核对受影响行为与验证。
- 每个子进程或原生服务有自己的环境与设置组合。不能先修改工作台全局环境变量再启动 A/B，也不能交替覆写同一个配置文件模拟并发。
- 共享 MCP 定义不默认共享一个已认证、有状态的连接。凭据引用、cwd、工具范围与连接生命周期分别绑定；只有服务明确支持并经验证的复用条件下才共享运行实例。
- 资源包升级、删除或 CLI 插件停用不会偷偷改变在途任务；保留被历史执行引用的版本或说明无法恢复的缺口。删除资产不删除 CLI 会话。
- 不把用户的 home 当成可随时清空重建的缓存，不复制真实凭据到可导出的执行快照；恢复在原状态归属内进行。旧来源保留“外部管理”标识，未经指定不迁移配置。
- 共享 Skill 的脚本、依赖、相对路径和工具名也需检查兼容性；只有 Markdown 结构相似不能证明它在两种 CLI 上含义相同。

## 6. Harness 原则变成验收

| 原则 | 本次新增的验收样例 |
|---|---|
| 控制来源明确 | 页面能解释为何某个 MCP/hook 生效；本次覆盖与 CLI 实际优先级冲突可见 |
| 按需装载 | 大能力库不把全部正文注入每个成员；只选择需要的资产，记录实际加载情况 |
| 不混淆能力与授权 | 选中插件后仍遵守原生有效权限；没有用资源开关覆盖更高层约束 |
| 连续性可恢复 | A/B 供应商同时执行、同名会话及进程重启后仍回到各自原生身份和能力版本 |
| 恢复有界 | 原生 CLI 正在重试时平台不重复派发；MCP/hook 失败有原因和停止出口 |
| 交接保持工作语义 | 上下文压缩/Lead 交接保留目标、当前任务、有效约束、能力清单和证据引用 |
| 独立验证 | Reviewer 记录自己的执行配置与工具版本，验证针对当前产物；没有仅因使用了“验证 Skill”就通过 |
| 生命周期分工 | 关闭窗口、停止 Agent、停用 CLI 插件、停用平台插件影响明确且彼此不同 |

对原生 CLI 内部工具、压缩、hooks 或子 Agent 事件没有可靠观测接口时，应显示未验证或不可控制，不宣称平台截获了全部动作。平台先做到配置来源、关键状态与交付证据可追溯，不为个人工作台建设无关的企业审计平台。

## 7. 官方来源与验证边界

- **[O1]** [OpenAI 官方 Configuration basics](https://developers.openai.com/codex/config-basic/)：配置来源与优先级。
- **[O2]** [OpenAI 官方 Advanced configuration](https://developers.openai.com/codex/config-advanced/)：`CODEX_HOME`、状态、凭据、供应商与 profile。
- **[O3]** [OpenAI 官方 Skills](https://developers.openai.com/codex/skills/)：发现范围、渐进加载、禁用与重启。
- **[O4]** [OpenAI 官方 MCP](https://developers.openai.com/codex/mcp/)：server 配置、工具范围、认证引用及 required。
- **[O5]** [OpenAI 官方 Plugins](https://developers.openai.com/codex/plugins/)：原生插件组成及支持界面的生效方式。
- **[C1]** [Claude Settings](https://code.claude.com/docs/en/settings)：settings 文件、`CLAUDE_CONFIG_DIR`、合并和外部环境变量。
- **[C2]** [Claude CLI reference](https://code.claude.com/docs/en/cli-reference)：`--settings`、`--setting-sources`、MCP 和插件参数。
- **[C3]** [Claude MCP](https://code.claude.com/docs/en/mcp)：连接配置、作用域与限制。
- **[C4]** [Claude Plugins](https://code.claude.com/docs/en/plugins)：原生插件目录和组成。
- **[C5]** [Claude Skills](https://code.claude.com/docs/en/skills)：发现、作用域、插件 Skill 与热更新。

初版完成的是文献核对与产品设计约束。2026-09-30 已补充 [V0.3 实验](execution-capability-experiments.md)：四配置并发、测试资源的选择/排除、Claude 已结束会话改配恢复，以及两类原生接续候选。机器级/账号全部隔离、复杂插件、在途热更新和完整六入口仍未验收；实验结果不是已有产品功能声明。

新增原生事实：Claude 后台 supervisor 随 `CLAUDE_CONFIG_DIR` 使用独立实例；当前版本有 `--bg`、`attach` 和前台转后台入口，见 [Agent view](https://code.claude.com/docs/en/agent-view)。本轮背景会话的合成 `apiKeyHelper` 路径成功，settings.env/API key 和进程环境 API key 路径在该 fixture 未就绪，应按运行方式核对认证。原生辅助请求也参与路由、生命周期和用量，不能被平台的主消息计数遗漏。
