# W02.08 · Codex 插件自定义 Skill 目录

2026-10-05。本地 `.codex-plugin/plugin.json` 插件可通过 `skills` 声明自定义目录，支持非空路径字符串及目录数组；方法名称与所在目录名无需相同。原 ID 启停、升级换目录、继承/重新添加、显式正文输入与冷恢复已贯通。最终原生 **116 项**、对应界面 **30 项**通过。W02.07/W02.08 仍为 **in_progress**，整体 **9/40 done**，Goal active。

## 变化与边界

Application 复用能力库的 manifest 解析结果和完整资源树，以声明中的实际 `SKILL.md` 路径绑定组件。显式 `skills` 目录替代默认 `skills/`；未选中的默认入口仍属于完整副本，但不进入本次可选组件。重复方法名拒绝，名称、完整原生名称和文件路径分别保存。

Runtime 不再把所有 Skill 强制还原为 `skills/<名称>/SKILL.md`，而是要求包内唯一的固定文件、合法相对路径和完整 SHA。浏览器仍只提交资产 ID/原生名称，服务器使用原快照解析实际路径。目录升级不改写旧请求路径或正文版本；新旧目录中的相对资料保持原位置和字节，没有搬成标准目录或改写正文链接。

空字符串、空数组、直接指向单个文件的 `skills` 声明尚未获得原生语义验收，生产入口明确拒绝；越界和缺失目录同样拒绝。此片不放开通用根 `plugin.json`、hooks、受管/已安装来源控制等尚未核对路径。缓存/整包策略准备与实际描述/正文载入继续分开，文件可见性和方法使用不由准备状态证明。

## 实际验证

| 层次 | 结果 |
|---|---|
| Application | 字符串/数组两类目录声明保留实际路径，默认入口不可显式选取，MCP 与 Skill 同包绑定；5 类不明确/无效目录声明拒绝；同名冲突保留 |
| Runtime | 现有输入回归新增绝对路径、父目录、缺失入口、目录冒充文件、重复文件和无效摘要 6 类拒绝，均无原生 `turn/start` |
| 原生 | 116 项：v1 两个目录 `methods/grouped-entry`、`helpers/other-entry`，v2 单目录 `workflows`；十轮均核对默认 Skill 描述缺席，七轮双 Skill 输入核对实际自定义路径、原操作/快照及正文摘要，七轮相对资料版本检查；独立会话、原 ID 变更、旧版本保留、冷恢复和丢回执同轮恢复不重发 |
| 原生回归 | 标准插件显式输入 92 项、外部共享 Skill 目录 101 项通过 |
| 浏览器 | 自定义目录 30 项；组件实际入口、默认方法不可选、升级后新路径和历史旧路径、正文匹配/不符、同 ID 重试、16 项上限、断连和移动布局；标准显式输入 25、共享目录 38、混合插件 17 项回归通过 |
| 其他 | app/console 全包 race、相关 Codex/异步/SkillContext race、最终定向 app race、nativecodex 标签 vet、`make dev-build`（含 web typecheck）通过 |

固定 Codex 后台 **0.158.0**，proxy 配置声明 **0.157.1**，使用自有隔离 stdio fixture。十轮只尝试不可用的 `127.0.0.1:9`，全部显式 interrupt；没有成功模型响应、工具或 hook 调用。MCP 仅初始化和清单查询。最终自定义组及两组原生回归的九个后台均退出 0。没有重探 listener/Windows，Runtime 全包未重跑或宣称通过。

两种声明格式先由 Application 红例捕获原先对 `skills` 字段的拒绝。初次原生 109 项通过后，扩展为多目录到单目录升级并增加七项实际输入路径核对，达到 116 项。最终检查补上空声明/单文件声明拒绝：红例证明它们曾被下发，修正后定向 race 和完整自定义原生流程重新通过。原先“目录名不等于方法名”的拒绝测试改为两个真实同名方法的冲突，避免继续拒绝已验证的合法入口。

界面使用 `/tmp/netcap-w0208-custom-skills-2880163131` 的真实 Handler 导出，运行当前构建的 `index-1tDnu_LZ.js`。HTTP/SSE、HTTP 回执丢失、内容不符与上限选项为受控回放，不算新增原生执行；末尾声明拒绝修复只影响 Application，界面代码和构建资产保持相同。新建/改配 UI 沿用既有组件和事务，实际原 ID 应用由原生测试覆盖。截图已检查，浏览器已关闭。

## 普通原生目录恢复缺口

本轮先继续检查 [W02.07 普通目录恢复](W02-07-codex-directory-context.md)留下的输入前核对条件。固定 schema 的 `skills/list` / `config/read` 未提供 thread ID 采样，线程恢复/设置回执没有逐项 Skill config；对该前片自有 `state_5.sqlite` 的只读 schema 检查，也没有找到线程 Skill config 字段。[补充检查记录](W02-08-codex-custom-skill-roots/directory-recovery-gap.json)保存范围与具体字段，没有读取用户会话数据库，也未把这次检查写成新的原生输入验收。

已有普通目录的生产恢复核对仍未完成，保留该缺口后转而实施本片独立的插件目录支持。没有用全局 `skills/config/write`、额外模型输入、旧上下文或参数接受代替逐会话生效证明。

## 复跑与附件

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^TestCodexNative(CustomSkillRoots|SharedSkillDirectory|ExplicitSkillInputs)$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

最终原生 `/tmp/netcap-w0208-custom-skills-451683403`；标准/共享回归 `/tmp/netcap-w0207-skill-input-1901891128`、`/tmp/netcap-w0207-skill-directory-1872382917`。附件：[最终结果及协议](W02-08-codex-custom-skill-roots/native/result.json)、[原始 rollout](W02-08-codex-custom-skill-roots/native/rollouts/)、[界面](W02-08-codex-custom-skill-roots/browser-custom.json)、[桌面](W02-08-codex-custom-skill-roots/desktop.png)、[移动](W02-08-codex-custom-skill-roots/mobile.png)、[日志](W02-08-codex-custom-skill-roots/logs/)、[差异](W02-08-codex-custom-skill-roots/changes.diff)、[审计](W02-08-codex-custom-skill-roots/audit.json)、[清单](W02-08-codex-custom-skill-roots/manifest.json)。installationId/宿主标识脱敏，未复制认证文件。

浏览器入口 [codex_skill_input_browser.js](../../tests/codex_skill_input_browser.js)：将 `__FIXTURE__` 替换为归档 `browser-fixture/browser-responses.json`，`__ASSETS__` 替换为当前 `web/dist` 绝对路径映射，截图写入 `.dev/w0208-custom-skills/`。既有格式依据沿用 [W02.05 官方结构与诊断证据](W02-05-formats.md)，逐线程行为以上述固定版本实际结果为准。

开发构建 SHA256：`e7910d1d213066e8b8618baa314ed0c3aff2821657cfcb0b6146bddca8733473`。2059 个旧证据文件与旧 `bin/netcap` 不变，差异基线 `/tmp/netcap-w0208-custom-skill-work/before`。未提交、推送或发布。

下一片继续通用插件 manifest 的生产版本/组件选配。普通原生目录恢复、完整方法/Gate、相对资料实际读取/使用、hooks 成功/异步、受管/安装来源、Windows/proxy、跨客户端输入独占以及既有三个 WSL 待返回结果仍保留；自定义目录支持不关闭 W02.07/08 整包。
