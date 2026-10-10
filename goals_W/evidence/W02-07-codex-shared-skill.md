# W02.07 · Codex 外部共享 Skill 的独立交付

2026-10-05。已登记、尚未由原生目录发现的 Skill 单文件或资源目录，可在 Codex 中新建选用，并沿原 ID 启停、升级、恢复继承和重新添加。资产保留 Skill 类型、原资源路径与版本，消息可显式请求当前方法正文。最终原生单文件 **94 项**、目录 **101 项**，对应界面各 **38 项**通过。W02.07 仍为 **in_progress**，整体 **9/40 done**，Goal active。

## 实现与范围

Application 从能力库保存的完整资源版本构造交付计划，Runtime 复用每会话、每资源摘要独立的原生插件缓存和整包禁用策略。交付包只有一个标准 Skill 组件；原 `SKILL.md` 正文不改写，目录中的相对资料和权限一并保留，原资源不增加插件 manifest。内部包装版本固定为 `1.0.0`，它不替代能力资产版本；界面、历史输入和改配差异仍使用原资产版本和 `kind: skill`。

新建与明确应用才物化副本；保存候选、观察和恢复不自动修复缓存。共享库更新不升级已有执行，明确换版本生成新缓存键；原 ID 冷恢复沿用原绑定。原操作、快照、轮次、方法名称/路径及正文 SHA 的关联复用既有显式输入路径。界面单列“本次原生 Skill 选择”，展示原资源与交付来源；新建、改配和消息选择均使用资产 ID，客户端不能指定缓存路径。

本路径只交付原生尚未发现的已登记资源。发送准备和只读观察均读取完整 `skills/list`，核对入口文件身份；原生已发现的路径、符号链接及硬链接别名拒绝独立交付。清单缺失、入口无法核对、重复嵌套 `SKILL.md`、资源或缓存漂移均不放行。来源后来进入原生目录时，输入被阻止，观察撤销 `verified` 和可输入状态并显示原因；查询不会 resume、更新线程设置或发起轮次。已有普通原生入口的生产选配/恢复核对仍待接通，不能用副本的禁用来声称排除了原入口。

## 实际验证

| 层次 | 结果与覆盖 |
|---|---|
| Application | 单文件/目录两类新建启停，保留资产类型、版本、来源及独立命名空间；公共改配预览验证禁用/继承仍标为 Skill，保留方法名和原版本；计划不写缓存、不发模型输入 |
| Runtime | 8 场景：单文件、目录、原生入口、符号链接、硬链接、清单不完整、来源漂移、嵌套 Skill；实际字节复制、缓存篡改拒绝及原目录不写入插件 manifest |
| 原生单文件 | 94 项通过；同资产两会话独立、新建启停、原 ID 改配、显式升级/继承/重新添加、旧版本保留、冷恢复、正文归属、真实输入回执丢失后同轮恢复且不重发、动态原生来源重叠阻止输入和验证声明 |
| 原生目录 | 101 项通过；除上述路径外，七次显式输入阶段分别核对原版本相对资料副本；不声称模型读取了资料 |
| 浏览器 | 单文件/目录各 38 项；类型/版本/来源、消息原 ID 重试、原快照与正文关联、历史版本、内容不符、只读失败/断连、16 项上限、新建与改配请求边界、移动布局 |
| 回归 | 原显式输入原生 92 项；界面显式输入 25、混合插件 17、Claude 插件 16、既有完整插件升级 18 项通过 |
| 检查 | app/console 全包 race、相关 Codex/异步/SkillContext race、nativecodex 标签 vet、`make dev-build`（含 web typecheck）通过 |

原生固定后台 **0.158.0**、proxy 配置声明 **0.157.1**，使用隔离 stdio fixture。两个最终共享组各十轮只请求不可用的 `127.0.0.1:9`，随后明确 interrupt；各七轮附带一个 Skill，其余为禁用/继承对照。没有成功模型响应、工具或 hook 执行，没有启动夹具 MCP。两共享组与本片显式输入回归的九个自有后台均退出 0。listener/Windows 限制未重探，Runtime 全包未重跑或宣称通过。

浏览器使用首轮单文件 `/tmp/netcap-w0207-skill-file-2034263724`、目录 `/tmp/netcap-w0207-skill-directory-3651685215` 的真实 Handler 导出；构建资产与最终开发构建相同，`index-BaTt-sNN.js`。HTTP/SSE、丢回执、内容不符和额外上限选项为受控回放。共享 Skill 的改配预览故意返回 409，只验证请求中的原快照/版本边界；原 ID 实际应用由原生测试验证。另保留旧插件完整升级界面 18 项，不将其当作共享 Skill 的完整浏览器应用验收。末尾来源观察修复仅改 Runtime，共享原生重新验收，界面代码与产物未变化。截图已检查，浏览器已关闭。

## 失败与修正

- Application 原先拒绝 Codex Skill 的红例修复后通过；旧 Claude 测试仍把 Codex 当作不支持后端，调整为仍不支持的 OpenCode 后通过。
- 不同路径的硬链接曾被误当外部资源，真实红例保留于 `hardlink-red.log`；改为 `os.SameFile` 后通过。
- 动态来源重叠时发送已被阻止，但只读观察仍称已验证。原生红例 `overlap-read-red.log` 与完整失败结果保留；观察路径补同一来源检查后，最终两组验证未核对状态、原因与查询无写入。
- 修复后后台观察会先撤销空闲状态，中间原生测试因只接受异步 `not_sent` 而失败。断言改为接受立即拒绝或持久操作 `not_sent`，两种都必须零新增 `turn/start`；中间失败亦保留，不算产品仍放行输入。
- 最初 Runtime 红/绿日志中的目录权限失败来自 fixture 未显式设为 0700，已修正测试目录；它不作为硬链接或其他产品缺陷的红例。

## 复跑与附件

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^TestCodexNative(SharedSkillFile|SharedSkillDirectory|ExplicitSkillInputs)$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go test ./internal/app ./internal/runtime -run '^TestCodexSharedSkill' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

最终原生目录 `/tmp/netcap-w0207-skill-file-3555746962`、`/tmp/netcap-w0207-skill-directory-555207872`；本片显式输入回归 `/tmp/netcap-w0207-skill-input-1721047006`。归档包含[单文件结果/协议](W02-07-codex-shared-skill/native/file/result.json)、[目录结果/协议](W02-07-codex-shared-skill/native/directory/result.json)、各组原始 rollout、[单文件界面](W02-07-codex-shared-skill/browser-file.json)、[目录界面](W02-07-codex-shared-skill/browser-directory.json)、[截图](W02-07-codex-shared-skill/browser/)、[失败记录](W02-07-codex-shared-skill/failures/)、[日志](W02-07-codex-shared-skill/logs/)、[差异](W02-07-codex-shared-skill/changes.diff)、[审计](W02-07-codex-shared-skill/audit.json)和[清单](W02-07-codex-shared-skill/manifest.json)。installationId/宿主标识脱敏。

浏览器入口 [codex_shared_skill_browser.js](../../tests/codex_shared_skill_browser.js)：将 `__FIXTURE__` 替换为归档 `browser-fixtures/<file|directory>/browser-responses.json`，`__ASSETS__` 替换为当前 `web/dist` 绝对路径映射；截图目录在项目 `.dev/w0207-shared-skill-<file|directory>`。HTTP 回放只使用本地构建，不产生额外原生输入。

最终开发构建 SHA256：`f4f8049dc8a9ce1858e48d4725ef4417bacc3511d5a04301b7699c35fb62771b`。2000 个旧证据文件和旧 `bin/netcap` 不变，前置基线 `/tmp/netcap-w0207-shared-skill-work/before`。未提交、推送或发布。

本片完成外部共享/专属 Skill 资源的 Codex 独立交付和原请求正文归属。`backends: [codex, claude]` 是兼容登记，未据此宣称两 CLI 联合方法验证。相对资料只验证位置/字节，不证明方法执行或 Gate 通过；禁用不抹除历史、不撤销文件权限，也不提供跨客户端原子输入独占。下一片继续普通原生来源生产绑定和输入前恢复核对；完整方法/Gate、hooks 成功/异步、受管/安装来源、Windows/proxy，以及既有三个 WSL 待返回结果均保留。
