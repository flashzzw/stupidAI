# W02.08 · Codex 仅含 Skill 的本地插件

2026-10-05。标准 `skills/<名称>/SKILL.md` 插件现在可以独立选用，无需附带 MCP。新建、原 ID 启停/升级/继承、双 Skill 输入及冷恢复已贯通；最终原生 **95 项**、纯 Skill 界面 **28 项**通过。W02.07/W02.08 仍为 **in_progress**，整体 **9/40 done**，Goal active。

## 变化与核对条件

Application 与 Runtime 移除“包内必须有 MCP”的限制，保留 1–128 项已支持组件、完整文件集合/权限/摘要、固定会话缓存、标准 Skill 入口与同名冲突检查。空包、自定义 Skill 根、hooks 及其他未核对组件不因此放行。原生接口继续沿用已验证的配置覆盖与整包禁用策略，没有新的全局安装或后台切换。

仅放开结构限制会暴露一个原先由 MCP 证据承担的检查缺口：没有 MCP 组件行时，插件摘要可能仍显示已核对，即使缓存改变、出现未声明 MCP、清单读取失败或线程未加载。新增测试先捕获这些失败，再使含 Skill 的插件证据同时要求完整选择检查、原生保存策略及线程已加载。只读读取未加载线程不恢复它，也不把其磁盘配置称为当前已核对。

纯 Skill 包仍查询完整原生 MCP 清单，以发现意外带入的组件或不完整观察；清单为空不凭空产生 MCP 资产或调用证据。界面纯 Skill 包显示“固定插件包与整包策略已核对”，混合包继续列出 MCP 核对范围。描述与正文载入仍在逐轮 Skill 上下文中独立核对，方法使用/Gate 不由该摘要证明。

## 实际验证

| 层次 | 结果 |
|---|---|
| Application | 启用/禁用两场景通过，保存独立插件绑定且不生成 MCP 选择 |
| Runtime | 6 场景先红后绿：启用、原生策略禁用、缓存漂移、未声明 MCP、未加载、清单不可用；正例完成显式 Skill 输入，反例不展示已核对 |
| 原生纯 Skill | 95 项通过；两个会话独立版本，新建启停、继承/重新添加、共享库更新不升级旧执行、明确升级、原 ID 冷恢复与历史保留。双 Skill、LF/CRLF、原请求/正文关联与丢回执不重发沿用上一片真实路径 |
| 原生回归 | 混合插件 57 项、混合插件显式输入 92 项通过 |
| 浏览器 | 纯 Skill 28、混合显式输入 25、混合选配 17、Claude 插件 16 项通过；纯包无 MCP 核对误称，保留输入选择/同 ID 重试/历史版本/断连/移动布局检查 |
| 其他 | app/console 全包 race、相关 Codex/异步/SkillContext race、nativecodex 标签 vet、`make dev-build`（含 web typecheck）通过 |

原生固定后台 **0.158.0**，配置的 proxy 声明为 **0.157.1**，使用隔离 stdio fixture。纯 Skill 组十轮仅请求不可用的 `127.0.0.1:9`，随后由 Application 显式中止；七轮分别请求两个 Skill，三轮禁用/继承对照先拒绝显式选择。没有成功模型响应、工具/hook 执行；没有启动夹具 MCP。三个最终原生组的九个自有后台均退出 0。当前代理的 listener/Windows 限制未重探，Runtime 全包未重跑或宣称通过。

浏览器使用首轮 `/tmp/netcap-w0208-skill-only-705383881` 的真实 Handler 导出，运行标识 `skill_only` 从该轮 result 加到回放外壳，Session 数据未合成。该轮与最终原生使用相同产品代码。HTTP/SSE、HTTP 回执丢失和内容不符等场景受控，回放不算新的原生执行。旧混合界面检查一度因仍要求“附带标准 Skill 目录的混合包”文案失败，按扩展后的支持范围更新断言，重跑通过。浏览器已关闭。

## 复跑与附件

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^TestCodexNative(SkillOnlyPluginInputs|MixedPluginSelectionAndChanges|ExplicitSkillInputs)$' -count=1 -v

GOCACHE=/tmp/netcap-w0206-auth-go-cache go test ./internal/app ./internal/runtime -run '^TestCodexSkillOnly' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativecodex ./internal/app ./internal/runtime ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

最终原生目录 `/tmp/netcap-w0208-skill-only-376377948`；回归目录 `/tmp/netcap-w0208-mixed-plugin-2675719827`、`/tmp/netcap-w0207-skill-input-3947928065`。[结果及协议](W02-08-codex-skill-only/native/result.json)、[原始 rollout](W02-08-codex-skill-only/native/rollouts/)、[界面](W02-08-codex-skill-only/browser-only.json)、[桌面](W02-08-codex-skill-only/desktop.png)、[移动](W02-08-codex-skill-only/mobile.png)、[红绿与回归日志](W02-08-codex-skill-only/logs/)、[差异](W02-08-codex-skill-only/changes.diff)、[审计](W02-08-codex-skill-only/audit.json)与[清单](W02-08-codex-skill-only/manifest.json)留存；installationId/宿主标识脱敏。浏览器复用 [codex_skill_input_browser.js](../../tests/codex_skill_input_browser.js) 的纯包分支，将 `__FIXTURE__` 替换为归档 `browser-responses.json`，`__ASSETS__` 替换为当前 `web/dist` 的绝对路径映射。

开发构建 SHA256：`14898fa4f4154da888cf3452dd3042871a36666bda275a90d9fe0d112b1d19c1`。1921 个旧证据及旧 `bin/netcap` 保持不变；差异基线 `/tmp/netcap-w0208-skill-only-work/before`。未提交、推送或发布。下一片继续普通目录/自定义根来源与完整方法作用；真实方法/Gate、hook 成功/异步、受管/安装来源、Windows/proxy 和跨客户端输入独占仍未完成，原有 WSL 待返回结果继续保留。
