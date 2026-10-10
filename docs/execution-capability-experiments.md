# CLI 执行配置与接续：V0.3 实验结果

日期：2026-09-30。目的：为 [产品设计 V0.3](product-design.md) 与 [新实施计划](implementation-plan.md) 提供可复查的能力依据。实验运行真实 CLI，模型响应来自本机模拟 HTTP 服务；没有调用真实供应商，也没有读取或改动用户 CLI 配置。

机器可读摘要：[V03 实验证据](../goals/evidence/V03-capabilities.json)。当前生产 `Description.Capabilities` 未改动；实验通过不代表已接入工作台。

后续进展见 [W01/W02 接续与配置边界补验](workbench-boundary-results.md)：补充真实 TUI、独立前台、双 supervisor 与同 ID 命名空间样本。本文保留首批 E01–E06 的历史范围；最新矩阵不将部分实验扩大为六入口全部通过。

## 1. 环境和证据等级

| 对象 | 实测版本/方式 |
|---|---|
| Codex 客户端 | CLI 0.157.1，原生 app-server proxy |
| Codex 后台 | 0.158.0，自有 Unix socket；测试结束停止自有进程 |
| Claude | CLI 2.1.280；直接 `--print`、原生 `--bg` 与 TTY `attach` |
| 模型协议标签 | Codex `gpt-6-astra`；Claude `claude-sonnet-4-6`；仅作 fixture 协议值 |
| 凭据 | `fixture-A/B` 合成值；供应商记录是否匹配，不保存真实凭据 |
| 文件/状态 | `/tmp/netcap-config-probe-*`、`/tmp/netcap-claude-attach-*`；独立 HOME、CLI 状态目录与项目 |
| 外部文档 | 官方网页、Context7 返回的官方来源、本机 CLI help 与生成的协议 schema |

结果区分：**通过（指定场景）**、**部分验证**、**未通过（指定配置）**、**未执行**。没有把 help 中出现一个命令、记录一个哈希、或脚本退出 0 自动当成完整能力通过。

## 2. 实验矩阵

| 编号 | 实际操作与观察 | 结果 | 不能推出的结论 |
|---|---|---|---|
| E01 四配置并发 | 两个 Codex 后台、两个 Claude 配置，第一轮四个 HTTP 请求在 barrier 同时等待；每份配置完成三轮，恢复原 ID，实际端点、模型、合成认证和历史正确 | 通过（12 次模型请求） | 不证明仅改变一个目录就隔离所有机器资源；不含崩溃中的工具恢复 |
| E02 Codex 逐线程选配 | 同一后台中的三线程依次采用默认、排除测试资源、再采用默认；Skill 描述、MCP 原生 inventory、本地 CLI 插件的 Skill 分别核对 | 通过 | 未测试复杂插件的 hooks/子 Agent、MCP OAuth、所有隐含能力及模型实际调用 |
| E03 Claude 逐次选配与恢复 | 新会话加载项目 Skill、本地插件 Skill、stdio MCP；另一会话排除；第一会话结束后以新配置恢复，原 ID/历史保留且新能力清单排除相应项 | 通过 | 不证明在途热切换或已读进历史的知识可被清除 |
| E04 Codex 原执行连接 | 第二 proxy 客户端读取同一在途 turn，原轮完成后关闭第一客户端，第二客户端继续同一 thread；只有两个主请求 | 通过（预共享后台） | 不证明任意独立前台 TUI 能被接管，也不提供平台输入权仲裁 |
| E05 Claude 后台接续 | 独立 CLI `--bg` 启动主请求；请求保持在途时 TTY attach；原 worker PID 不变，观察回复后 Ctrl+Z 脱离，worker 仍在；主请求只有一次 | 通过（原生后台 + helper 凭据） | 不证明任意独立前台转后台、审批/工具在途迁移或生产 SDK bridge 已支持 attach |
| E06 Claude 后台认证差异 | settings.env 中放合成 API key，以及冷启动进程环境放相同 key，两种 fixture 都停在“Not logged in”，没有模型请求；改用 profile 的合成 `apiKeyHelper` 后 E05 通过 | 前两配置未通过；helper 路径通过 | 不据此断言所有用户环境变量认证都不受支持；需按连接方式做认证诊断 |

## 3. 四份执行配置并发与恢复

入口：`python3 tests/execution_config_probe.py --case concurrent`。

每个本地供应商在收到第一条实际模型请求后等待同一个四方 barrier；只有四方全部到达才释放响应。因此不是“同时启动四个进程，实际串行调用”。证据记录各请求到达和释放的单调时钟时间，满足最晚到达早于最早释放。

Codex 使用两份 CODEX_HOME、两套 Unix socket 与后台；重建客户端后读取原历史并继续。Claude 使用两份任意命名的 settings 文件，供应商参数来自 settings，另绑定不同 CLAUDE_CONFIG_DIR；每轮重新启动 CLI，以 `--resume` 继续原 ID。最终每份配置均有三轮及自己的 A/B 标记，另一供应商的标记未串入历史。

本实验也分别隔离 HOME、XDG 与 cwd。这是一个验证过的实验组合，不能据此保证用户共用 HOME、项目目录、OS 凭据库或 supervisor 时的结果。共享 MCP 定义的跨 CLI 复用、同原生 ID 的不同命名空间、用户真实配置导入和四份配置同时崩溃重启仍归 W01/W02。

原始记录：`/tmp/netcap-config-probe-pjtjcyg7/concurrent/`。

## 4. Codex 资源选择

入口：`python3 tests/execution_config_probe.py --case codex-assets`。

- 共享 Skill 放在测试 HOME 的 `.agents/skills`，位于 CODEX_HOME 外。这证明配置目录之外确有发现来源，不能只扫描 CODEX_HOME。
- 本地插件通过 `.agents/plugins/marketplace.json` 及 `.codex-plugin/plugin.json` 声明，使用本机 schema 的 `plugin/install` 安装到测试配置；插件只包含一个测试 Skill，不访问远程市场。
- MCP 使用本地 Python stdio 服务，只实现初始化、工具列表和固定 pong；观察 `tools/list` 与线程作用域的 `mcpServerStatus/list`。
- 第二线程通过 `thread/start.config` 禁用指定 Skill、MCP、插件；相应 Skill 描述不再提供，原生 inventory 不再含测试 MCP 工具，也没有启动新的 MCP 实例；AGENTS.md 标记仍在。
- 第三线程恢复默认选择，两个 Skill 再次出现，证明前一线程的排除没有变成全局停用。

原始记录：`/tmp/netcap-config-probe-pjtjcyg7/codex-assets/`。早期版本只检查 MCP 进程未重新启动，证据不足；最终增加原生 inventory 的正反例，避免对本来不在初始模型工具列表中的延迟工具做空洞断言。

这仍是发现、暴露和初始化的证据，不是模型实际遵循 Skill 方法或调用 MCP 的证据。操作系统仍可能允许经普通文件工具读取这些资产；资源选择不构成文件访问隔离或知识撤回。

## 5. Claude 资源选择与续聊

入口：`python3 tests/execution_config_probe.py --case claude-assets`。

启用路径采用项目 Skill、`--plugin-dir` 的本地 CLI 插件和 `--strict-mcp-config --mcp-config`。模型请求包含两个测试 Skill 的描述；原生 init 列出插件、已连接 MCP 与工具。排除路径使用 `skillOverrides: {"v03-project": "off"}`、空的严格 MCP 配置，并不再提供插件目录。三个测试资源从新的原生清单消失，CLAUDE.md 仍保留。

随后用排除配置恢复第一条已经结束的会话，原生 ID 不变，旧用户消息仍在，新 init 不再包含被排除的 Skill/插件/MCP。该变化应生成新的执行配置快照并记录生效轮次，不能覆盖先前的配置记录。

原生仍暴露 `deep-research`、`verify` 等内置 Skill 和工具。本实验没有声明它们可全部关闭。产品应列出原生附带项，不能将“取消全部库中资源”显示为“运行中没有任何能力”。

原始记录：`/tmp/netcap-config-probe-pjtjcyg7/claude-assets/`。

## 6. 接续入口及认证差异

Codex 入口：`python3 tests/execution_config_probe.py --case codex-live`。原始记录：`/tmp/netcap-config-probe-pjtjcyg7/codex-live/`，后台记录位于其结果指向的 `/tmp/netcap-console-oqqjqwul/`。平台未参与这次交接；两个客户端遵守测试安排，不代表原生后台已经强制唯一输入者。

Claude 入口：`python3 tests/claude_attach_probe.py --auth-source helper`。原始记录：`/tmp/netcap-claude-attach-lzf6djm5/`。直接使用原生 TTY attach，未实现 Windows 终端嵌入或 Web 交互转换。

E05 特意只阻塞内容精确匹配主任务的请求，避免误把后台标题生成请求当作正在执行的任务。实测 1 个主请求及 3 个辅助请求，认证均指向本地 fixture；辅助请求用于标题/状态/标签。本轮数量不是固定产品常量，但说明预算不能只统计用户发送次数。

E06 的失败记录保留：settings 路径 `/tmp/netcap-claude-attach-8slyb_wo/`，进程环境路径 `/tmp/netcap-claude-attach-m4f8lnmv/`。这些实例可 attach 到同一个进程，但原任务没有发出模型请求，不能算成功续行。helper 从独立配置调用只打印合成 key 的本地脚本；没有复制真实凭据。

官方说明 `CLAUDE_CONFIG_DIR` 下的后台 supervisor 有独立实例与会话。本轮在临时目录启动并只停止该实例；双 supervisor 同时运行、长程资源占用与升级恢复仍需专门验证。[C2]

| 首版入口 | 当前证据 |
|---|---|
| Codex 新建 | 本轮原生 proxy 实验通过；生产 Alpha 有历史验收 |
| Claude 新建 | 本轮直接 CLI settings 路径通过；生产 SDK 路径有历史验收 |
| Codex 实时接入 | 预共享后台有本轮证据；任意独立前台 TUI 未执行 |
| Claude 实时接入 | 原生后台 + helper 有本轮 attach 证据；独立前台转后台、平台输入交接未执行 |
| Codex 暂停/退出后恢复 | 完成轮次后的客户端恢复通过；原前台退出与在途工具释放仍待补 |
| Claude 暂停/退出后恢复 | 直接 CLI 退出后的同 ID 恢复通过；独立交互 TUI、在途工具和输入权仍待补 |

因此完整 P02 仍未通过。现有 SDK Adapter 的 `external_tui=unsupported` 保持原义；应新增按原生连接方式验证的能力，而不是把所有 Claude 会话直接改成 supported。

## 7. 对产品与实施的具体影响

1. 接续面板同时展示来源、原生运行方式和输入控制能力；“进程可连接”“认证可用”“任务可继续”分开显示。
2. 执行配置绑定 CLI、状态目录、原生服务实例、认证来源与能力版本；不能只保存供应商标签和一个 settings 文件。
3. 选择能力的检查至少比较原生发现/inventory 与配置意图；需要实际调用的任务另由工具记录与 Gate 验证。
4. 已结束会话改配有可行路径，但运行中更改需要单独实验；历史已经含有的内容不能靠禁用资源撤回。
5. 预算包括可观测的原生辅助请求；后台 supervisor、内置任务和插件子 Agent 不能成为绕过平台容量的隐含执行。
6. 下一步收敛 W01 的严格接续契约；W02/W03 可交错推进。保留硬需求，暂不锁底座。

## 8. 来源与复查

- [O1] [Codex Skills](https://developers.openai.com/codex/skills/)：用户/项目发现、`skills.config`、渐进加载；本轮请求重定向至 `learn.chatgpt.com/docs/build-skills`。
- [O2] [Codex Advanced configuration](https://developers.openai.com/codex/config-advanced/)：配置/状态与供应商来源；本轮重定向至 `learn.chatgpt.com/docs/config-file/config-advanced`。
- [O3] [Build plugins](https://developers.openai.com/plugins/build/plugins)：本地 marketplace、manifest、安装与插件配置；本轮取回正文。旧 `/codex/build-plugins/` 本轮重定向内容不适合作为原生打包依据，未用于断言。
- [O4] CLI 0.157.1 `app-server generate-json-schema`：`ThreadStartParams`、`SkillsListParams`、`PluginInstallParams`、`ListMcpServerStatusParams`。后台仍固定 0.158.0，实际调用验证两者组合。
- [C1] [Claude CLI reference](https://code.claude.com/docs/en/cli-reference)、[Settings](https://code.claude.com/docs/en/settings)、[Skills](https://code.claude.com/docs/en/skills)：通过 Context7 查询官方来源，并以本机 `--help`、init 和实际请求核对。直接 HTML 请求失败，未把未打开页面当作证据。
- [C2] [Agent view](https://code.claude.com/docs/en/agent-view)：本轮通过官方 `.md` 入口取回全文，涵盖 attach、前台转后台、状态目录和 supervisor 生命周期；正文说明前台转后台可能等待工具或重启无法迁移的工作，所以还需专门验收。

来源与编辑前快照保存在 `/tmp/netcap-v03-4znpkw3j/`。仓库只保存合成实验摘要和重跑脚本；原始临时目录可被系统清理，摘要记录来源与哈希供追溯。

## 9. 本轮最终检查

通过：

```bash
python3 tests/execution_config_probe.py
python3 tests/claude_attach_probe.py
python3 -m py_compile tests/execution_config_probe.py tests/claude_attach_probe.py
python3 tests/execution_config_probe.py --help
python3 tests/claude_attach_probe.py --help
```

第一条运行 E01–E04，第二条默认使用 `--auth-source helper` 验证 E05。脚本先核对固定 CLI 版本，再运行测试；版本变化时拒绝沿用本次结论。E06 的 settings/process 变体是已记录的失败配置，不能纳入“全部路径通过”。最终测试实例的 supervisor 已退出，并核对未残留引用这些 fixture 目录的 CLI/helper 进程。

文档检查覆盖本轮 Markdown 的相对文件引用、表格/围栏和尾随空白，以及新计划对全部 27 项需求的覆盖。本轮没有改生产 Go/Web/bridge 源码，没有重新运行完整 T07、浏览器、Windows 桌面或真实供应商验收；上一轮总览中的 Go race/vet 与类型检查保持历史证据，不冒充本轮执行结果。
