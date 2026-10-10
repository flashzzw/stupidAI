# W02.07 · Claude 已有 Skill 的原生选择纵切

2026-10-02。实现新建 Claude 会话对已登记 Skill 的显式启停：Application 核对用户/项目入口、解析原生目录键与声明名称，把原生入口和实际资源路径与版本一起保存到快照/Scope；Runtime 转交生产 Session，SDK 应用目录键 override，并同时核对命令和描述条目。新建表单、执行详情和历史快照展示已接通。

实现依据是[上一片固定 CLI 选择键实测](W02-07-skill-selection-contracts.md)。范围包含当前原生来源中的共享链接和专属 Skill；尚不自动安装来源外的资产，也不提供 Skill 原 ID 改配。不能把这片当作整个 W02.07 收口。

## 行为与契约

- 声明名称和 native_key 分别保存；不以展示名称冒充原生选择键。来源必须唯一对应所选执行配置/项目的实际 `SKILL.md`。同一资源的多入口、同目录键跨来源以及不支持的来源会拒绝。
- 原生启动前后比较其他继承命令与描述条目，避免 key 联动误改未选资源；不使用 SDK skills 白名单清空其余上下文。
- Session 恢复时重用保存选择。新输入和权限回复前核对来源/清单，发现新增同键来源或错误来源时拒绝；只读采样不可用不会被当成成功禁用。`skill_evidence` 只表示原生命令与描述清单，未添加 loaded/used/gate_passed 声明。
- YAML 元数据解析复用能力库路径。引用保留内容哈希，Skill 正文不进入执行快照/配置元数据。只读原生观察清除执行选择，MCP 改配绑定校验包含保持不变的 Skill；混合资源提案保留 Skill，但实际混合改配还需端到端验证。

## 实际验证

所有 Go 命令使用 `GOCACHE=/tmp/netcap-w0206-auth-go-cache`。真实 CLI 固定 Claude 2.1.280 / SDK 0.3.280；独立 HOME、profile、项目、journal 与 SQLite，原生端点仅为未监听的本地 fixture 地址。没有模型输入或真实供应商请求。

| 层次 | 实际结果与入口 |
|---|---|
| Application/Runtime 契约 | [Go 日志](W02-07-claude-skill-selection-unit-go.log)：`go test -race ./internal/app ./internal/runtime -run '^Test(ClaudeSkill\|CapabilityDiagnosisSkill\|NativeObservation)' -count=1 -v`；Application 9 个 Skill 场景、Runtime 10 个场景通过，含 source alias、重启快照、冲突拒绝、混合 MCP 候选保持和缺失/伪造清单。复用 YAML 诊断与只读观察回归也通过 |
| Session 边界 | [Node 日志](W02-07-claude-skill-selection-node.log)：`node tests/claude_skill_selection_test.mjs` 的 6 项通过；继承保留、symlink 漂移、同键/同名拒绝、原 flags 保留及无效值不泄露 |
| 原生 Session/SDK | [12 项结果](W02-07-claude-skill-selection-native-session.json)，`python3 tests/claude_skill_selection_native.py`；最终目录 `/tmp/netcap-w0207-claude-selection-0r8ms7yc`。同项目启用/禁用、共享 symlink、原 ID 重开与保存选择、同级查询不受影响通过；新增同键来源在 input journal 写入前拒绝 |
| 真实 Application/SQLite/Runtime | [9 项结果](W02-07-claude-skill-selection-native-app.json)，`NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude go test -tags=nativeclaude -race ./internal/app -run '^TestClaudeNativeApplicationSkillSelection$' -count=1 -v`；最终目录 `/tmp/netcap-w0207-claude-app-720618323`。两个 profile 共享同一资源、独立 namespace、命令/描述启停、冲突发送 not_sent、原生 journal 零 input 通过 |
| 浏览器 | [14 项结果](W02-07-claude-skill-selection-browser.json)，重放最终原生 Application 的真实调用方数据；启停状态、目录键/来源、表单序列化、客户端不传原生路径、方法 Gate 边界和窄屏通过。[1440px](W02-07-claude-skill-selection-1440.png)、[390px](W02-07-claude-skill-selection-390.png)已实际查看。HTTP 和创建回执为受控重放，没有新增原生创建或模型输入 |
| 相关回归与构建 | app/console 全包 race、Runtime Claude/MCP/Codex Skill/原生观察筛选 race、MCP 认证及认证恢复 10 项、带 nativeclaude/nativecodex 标签的相关 vet、bridge tsc 和 `make dev-build` 通过 |

浏览器使用已有 `/api/commands` 格式；新来源绑定在服务器生成，不接受客户端指定原生路径。Application 原生测试直接调用生产应用接口，没有经过 HTTP listener；浏览器也不能替代该传输层验收。完整生产 HTTP/WSL 使用、实际模型上下文和方法执行没有在本片测试。

开发过程中修正了测试初稿的参数顺序、改配命令名与符号链接删除方式；初稿失败日志保留在 `/tmp/netcap-w0207-claude-selection-work`。浏览器首稿匹配半角加号而页面使用全角加号，改正后通过；最终截图显式滚动到 Skill 区域。这些测试入口修正没有改变产品准入条件。

## 状态与下一步

W02.07 继续 in_progress，累计 9/40。尚需来源外共享/专属资源加载、完整来源解释、Skill 原 ID 改配与版本更新、正文按需读取、实际方法与 Gate，以及 Codex 原生选择。已有用户终端 Codex 完整探针请求保持待结果，本轮未重复环境探测。禁用仍不撤销文件读取权限，也不抹除历史方法内容。

原始路径、投影、源码和开发构建哈希见[manifest](W02-07-claude-skill-selection-manifest.json)，变更见[diff](W02-07-claude-skill-selection-diff.patch)。旧证据与 `bin/netcap` 不变；无提交、安装依赖、共享后台操作或对外发送。Goal active，完整 W 系列未完成。
