# W02.08 · 受管策略收紧后的原 ID hook 禁用与撤去

日期：2026-10-05。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 用户可见行为

已经由工作台建立的 Codex 本地 hook 会话，在原生要求变成“仅允许受管 hook”后仍停止普通输入。用户现在可以保存原包的禁用或恢复继承选择，点击“准备应用（保持暂停）”，核对通过后再明确应用；三个阶段沿用原会话 ID。保存和准备保持当前执行快照，只有原 ID 应用及实际配置核对成功后才提升候选版本。

本片覆盖带普通目录 Skill 的 hook 禁用、随后恢复继承，以及没有其他 Skill 的受限会话直接恢复继承。未改动的目录 Skill 保留准确路径、版本及选择；暂停状态和历史快照保留。恢复继承表示撤去本次插件覆盖，不表示全局关闭同名来源或删除历史正文。

## 准入边界与实现

仅允许禁用或撤去原本已选的 hook 包，可同时降低信任；不能借此启用、增包、换版本、提高信任或改变其他 Skill/MCP、路由和身份。候选不能留下启用的本地 hook。候选分类本身不授予执行权限。

Runtime 将配置建立依据分为完整指纹和排除受管要求的原配置指纹。明确的 managed-only 拒绝保留同连接建立原配置的历史事实，当前就绪仍为失败；其他漂移或错误继续使依据失效。内部撤去路径要求当前 managed-only 响应、原连接/原选择建立依据和未改变的配置指纹，并继续复核完整来源/缓存、原 ID、历史、Goal、在途请求、运行或未知 hook。保留的普通目录资源仍检查完整内容。私有撤去标志仅在准备/应用内部使用，不持久化、不能经 JSON 设置，也不能用于普通消息输入。

Application 将准确候选绑定到持久准备 Intent，分发及处理结果时复核原快照、候选和原权限。只有工作台管理、连接就绪、原生 idle 且执行证据完整的 unknown 会话可以尝试准备；人工接手、不完整状态或失去原连接依据仍拒绝。明确应用复用既有禁用自动恢复、退订、原 ID resume、配置核对和候选提升事务，不启动新执行来代替恢复，也不停止共享后台。

## 实际验证

| 层次 | 结果与范围 |
|---|---|
| 原始失败 | [native-red.log](W02-08-hook-deactivation/native-red.log)：真实链路在 Application prepare 被“原会话未空闲”拒绝，退出 1；修复后最终原生流程退出 0。 |
| Runtime | 候选分类 14 类、准备 17 类通过；其中 12 类继续检查 apply 拒绝且无原生写入。覆盖连接依据缺失/替换、配置/缓存漂移、在途 Goal/thread/审批、运行/未知 hook、历史不完整、候选缺失/篡改及 JSON/input 不能授予撤去权限。 |
| Application | 5 类环境及持久候选/权限篡改检查通过。补权限核对前的失败保存在 [permission-intent-red.log](W02-08-hook-deactivation/permission-intent-red.log)，最终相关事务回归通过。 |
| 原生与产品链路 | [最终 41 项](W02-08-hook-deactivation/native-final/result.json)全部通过：实际 app-server 0.158.0、生产 Application/Runtime/SQLite/Handler，三阶段改配、原 ID/历史、幂等、保留目录资源和暂停状态。实际原生返回 `requirements:null`；测试只在响应边界注入更严格 managed-only 要求。 |
| 浏览器 | [最终 39 项](W02-08-hook-deactivation/browser-direct-final.json)通过，使用本次构建和真实 Handler/阶段快照回放；纯 hook unknown 会话可准备直接继承。保存/准备不提前应用，原 ID、保留 Skill、暂停及窄屏无横向溢出均核对。桌面/窄屏截图已查看。属于 HTTP 回放，不是实时监听端到端测试。 |
| 独立审计 | [12 项](W02-08-hook-deactivation/audit.json)重新比较三个阶段的保存/准备当前快照、准备权限及准确插件候选；原生执行后台仅 3 次 `thread/start`，零 `turn/start`。 |
| 相关回归 | 组合资源原生 90 项、managed-policy 原生 16 项、managed-policy 界面 16 项通过。相关 Runtime race、app/console 全包 race、vet/tagged vet、typecheck/开发构建通过。全包 race 发生在最后权限 guard 之前；其后重跑相关 Application 事务检查、app vet、开发构建及最终原生 race。最终仅扩展测试，tagged vet 也通过。 |

最终原生目录 `/tmp/netcap-w0208-hook-deactivation-644690052`，两个自有后台均退出 0。组合回归 `/tmp/netcap-w0208-combined-resources-3206872966`、策略回归 `/tmp/netcap-w0208-managed-hooks-482316774`。较早的顺序“禁用→继承”31 项样本保留在 `native-sequential/`，其 27 项界面结果是 `browser-final.json`；它们不是最终直接继承样本。`native-before-permission-check/` 同样保留其发生顺序，不代替最终结果。

两个合成 paused Goal 仅用于让原生日志落盘、核对暂停与同 ID 保留，明确不是模型会话历史。新流程没有模型、工具或 hook 执行，也没有真实受管策略文件/MDM 强制执行验收。已有 OpenCode 环境失败不归本片修复；Runtime 全包未作为本片通过依据。

先前测试夹具的 pending 请求 key 缺少实例/原生 ID 前缀，以及浏览器定位器同时命中两段文字，分别保留在 `pending-fixture-key-failure.log`、`browser-selector-failure.json`；修正测试后通过，没有因此放宽生产检查。

## 复验入口与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run '^(TestCodex|TestAsyncQuestion|TestSkillContext)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeManagedHookDeactivation$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
```

浏览器脚本：[codex_hook_deactivation_browser.js](../../tests/codex_hook_deactivation_browser.js)。用最终 `browser-responses.json` 替换 `__FIXTURE__`，用本次 `web/dist` URL→文件映射替换 `__ASSETS__`，生成 `.dev/w0208-hook-deactivation/run-browser.js` 后交由现有 Playwright MCP 执行。浏览器已关闭，没有重复探测或改动受限 IPC 环境。

- 旧 `bin/netcap`：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`，未改。
- 开发 `.dev/bin/netcap`：`b0b4827b8c52852918b45ee17f7cac416eb3d15cd299ac3f1762cdfde0247f89`。
- JS `index-tdHkyIue.js`：`8242360e2afa4de20d793805a4894196e37db361af3220a6cecec58240f026c2`。
- CSS `index-DSkOCaGd.css`：`3781875c9ca317492b8773f0b67767aa507fe40eca497aedb876f6190b6c845a`。

[归档](W02-08-hook-deactivation/)包含来源副本、相对于本片起点的 [diff](W02-08-hook-deactivation/source.diff)、日志、原生轨迹、受控响应、浏览器结果与截图。[审计](W02-08-hook-deactivation/audit.json)与[清单](W02-08-hook-deactivation/manifest.json)核对 2892 个既有 W 证据、旧二进制及本片产物；没有提交或推送。

用户再次提供的 `/tmp/netcap-console-bp2rp6xu` 与[既有 Claude 插件执行证据](W02-08-plugin-execution.md)的 33 项结果完全一致，已核对，不计为新一轮执行，也不替代仍待结果的插件改配/Skill/生命周期探针。

## 剩余范围

本片只解决原连接依据仍在、原配置未变时的明确限制或撤去。原生到 Runtime 回执丢失后的安全恢复、已丢连接/未知占用、仍存活共享后台与多客户端输入独占没有完成。真实 `requirements.toml`/MDM、受管与安装来源的完整控制/权限、方法/Gate、成功/异步/通用格式 hook、Windows 与 Unix proxy/peer 仍待验收。

下一片继续原生丢回执的核对与恢复条件，并收取四项已请求的 WSL 结果：`claude_plugin_change_smoke.py`、`claude_skill_method_probe.py`、`codex_skill_selection_probe.py`、`codex_hook_lifecycle_diagnosis.py`。不重复无新条件的环境探测，不把本片通过记成 W02.08 整项完成。
