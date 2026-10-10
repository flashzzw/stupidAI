# W02.07 · Codex Skill 来源发现纵切

2026-10-02。普通 Codex 会话可在“执行配置快照 → Skill 来源发现”按需刷新名称、描述、路径、原生来源类别、目录配置启停及扫描错误。生产 `Codex.DiscoverSkills`、`Application.ReadSessionSkills`、鉴权 Handler 与构建后的界面已贯通。此切片是目录/profile 采样，不提供逐会话启停，不写执行快照或授权输入，也不声称加载、已使用或方法 Gate 已通过。

`POST /api/sessions/{id}/skills/discover` 只接受空对象，按保存的实例和工作目录查询。查询前后检查绑定与 generation；替换实例、查询中切换绑定或不匹配目录会拒绝结果。资产内容变化仍可查看当前发现，但不会解除原执行的来源漂移限制。清单缺页/缺失必要字段/不匹配目录被拒绝，重复路径、未知来源及原生扫描错误保留不完整；错误仅呈现路径与稳定说明，不回传原始解析错误可能引用的正文。查询不替换输入权、原 ID 或版本历史。

## 接口依据

按既有 OpenAI Docs / find-docs 查询复用[官方域名索引摘录](W02-07-skill-discovery-docs.json)。直接打开官方 Codex 页面曾返回 HTTP 403，未冒称页面读取成功。[官方 App Server 文档](https://developers.openai.com/codex/app-server)的最新索引含额外根目录参数；固定 CLI 本地生成的[0.158.0 schema](W02-07-skill-discovery-schema.txt)只有 `cwds`、`forceReload`。实现严格使用后者，返回 `path/scope/enabled/pluginId`，不调用 `skills/config/write`。Claude SDK 0.3.280 的 skills/skillOverrides 已核对，但本片没有生产 Claude Skill 接入。

## 实际验证

所有 Go 命令设置 `GOCACHE=/tmp/netcap-w0206-auth-go-cache`。隔离 HOME/profile/项目与 SQLite；原生固定 server 0.158.0、proxy 描述版本 0.157.1，不操作共享后台或真实供应商。

- [Runtime/Application/Handler 筛选日志](W02-07-skill-discovery-unit.log)：`go test -race ./internal/runtime ./internal/app ./internal/console -run 'Test(CodexSkill|SessionSkill|LocalAuthorization)' -count=1 -v` 通过；Runtime 15 场景、Application 7 场景、Handler 59 场景（新增 5）。涵盖缺失字段、同名不同来源、部分失败、原始错误正文过滤、绑定变化和鉴权边界。
- [原生结果投影](W02-07-skill-discovery-native.json)：`TestCodexNativeSkillDiscovery` 的 12 项检查通过。最终原始目录 `/tmp/netcap-w0207-native-skills-3656673707`；实际执行 [Application/SQLite/Handler→Runtime→原生 stdio](W02-07-skill-discovery-native.log)。用户来源禁用、项目来源启用，损坏→修复→删除刷新正确；原生还返回隔离 profile 内的 6 项系统内置 Skill。未验证管理员/插件的实际优先级与排除能力。
- 原生方法轨迹只有 `initialize` 2 次、`skills/list` 4 次、`thread/start` 2 次。首次发现不创建线程；随后两个线程仅用于真实 Handler/界面绑定验证，零 `turn/start`、零配置写入。原生的两次进程启动均已退出 0。结果不包含测试 Skill 正文，配置文件与 Application 状态保持。
- [浏览器 18 项](W02-07-skill-discovery-browser.json)通过。重放原生测试导出的真实 Handler 响应，验证手动刷新、部分扫描、修复、删除、失败清空、迟到回执与会话切换、断连/重连、空请求与 390px 无横向溢出；[1440px](W02-07-skill-discovery-1440.png)、[390px](W02-07-skill-discovery-390.png)已实际查看。HTTP 传输与 EventSource/503 故障为受控重放；不是生产 listener/proxy/peer 整链路。无 pageerror；唯一 console error 是主动注入的 503。
- [app/console 全包 race](W02-07-skill-discovery-regression.log)、`go vet -tags=nativecodex ./internal/runtime ./internal/app ./internal/console`、`make dev-build` 通过，后者含 TypeScript 检查与 Vite 构建。未重跑无关 WSL/Claude MCP 原生矩阵。

首轮测试夹具遗漏同源 Origin/JSON Content-Type，分别得到预期框架拒绝 403/415；补齐请求头后通过，未改变鉴权规则。浏览器脚本最初使用服务 realm 不提供的 URL 构造器，并未在断连重建面板后展开；修正脚本后通过。这些是测试入口问题，没有据此削弱产品断言。原生首轮失败目录 `/tmp/netcap-w0207-native-skills-4230049323` 与日志保留，后续通过目录不覆盖旧记录。

原生复现入口：

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache make codex-skills-native \
  NETCAP_CODEX=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
  NETCAP_SERVER_CODEX=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex
```

浏览器源 `tests/skill_discovery_browser.js` 以原生目录的 `browser-responses.json` 替换 `__FIXTURE__`、以当前 `web/dist` 绝对文件映射替换 `__ASSETS__`，在配置的 Playwright 浏览器执行；只重放文件/请求，不启动测试服务器。

## 保留范围

W02.07 改为 in_progress，整体仍 9/40。下一片接通逐会话 Skill 选择，再补 Claude 发现、共享/专属资源加载、按需读取、方法小任务与 Gate、受管/插件不能排除项和版本恢复。当前发现按钮不代表这些要求通过；禁用也不等于抹除历史或撤销文件权限。

[diff](W02-07-skill-discovery-diff.patch)与[manifest](W02-07-skill-discovery-manifest.json)记录归档时点。旧证据和 `bin/netcap` 保持原哈希；开发构建已更新，未经提交、发布或共享后台切换。原生 Goal active，未关闭完整 W 系列。
