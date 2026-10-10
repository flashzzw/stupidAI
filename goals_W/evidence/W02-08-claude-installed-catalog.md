# W02.08 · Claude 已安装插件登记展示

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 用户可见结果

在“执行配置快照 → Claude 已安装插件登记”点击“读取安装登记”，显示保存 profile 下的完整来源键、登记范围/项目、登记版本及缓存状态。已知本地目录市场另列来源目录、市场声明的插件目录和当前 manifest 版本。登记缓存与声明目录分别展示；版本不一致、重复声明、路径冲突、未知或尚未支持的来源、损坏元数据均有明确提示。

读取失败、快照变化及断线清除旧结果，断线禁用读取。登记文件不存在时显示零记录并说明其他来源仍可能存在。结果不进入执行快照，不自动升级或授权启停；已安装来源的生产选配限制保持。

## 实现与范围

新增 `Application.ReadSessionInstalledPlugins` 与 `POST /api/sessions/{id}/plugins/installed`，HTTP 请求仅接受 `{}`。只读保存的 Claude console 会话绑定；读取前后核对执行快照、状态目录、当前 Instance、generation 和 Scope。目录/文件采样在状态锁外，不调用 Runtime、不启动或恢复查询、不读取 MDM、不安装或 reload，不改变输入归属。

支持 `plugins/installed_plugins.json` version 2，结合 `known_marketplaces.json` 中的本地 directory 市场读取声明。完整键为 `name@marketplace`；缓存只检查目录存在与解析路径，不据缓存推断加载目录。其他市场仅返回已知类别，不返回 repo/URL 或下载资源。来源与安装市场目录不同、市场名称不符、同名声明不唯一、相对路径越界或目录外链均不继续推断。

元数据单文件上限 1 MiB、整次读取 4 MiB、安装条目 512、市场声明 2048、条目展示预算 2 MiB；JSON 拒绝重复键。`os.OpenRoot` 约束父目录解析，打开文件时用 nonblocking/no-follow，拒绝 FIFO、叶子链接和非普通文件，读取后核对文件身份、大小、时间和模式。取消、格式错误及超限返回不完整信息，不返回元数据正文、hook 命令或解析输入。

`complete` 仅表示支持范围内此次登记/声明采样完成，**不是全部原生来源、实际加载、有效权限或多文件原子快照的证明**。缓存缺失单列状态，不妨碍读取仍存在的市场声明。读取后源文件还可能变化；这些结果不能用于生产准入、来源冻结或版本升级事务。

## 实际验证

| 层次 | 结果与边界 |
|---|---|
| Application 状态与文件边界 | 公共入口分列缓存/声明且不改变执行；20 类元数据边界通过，含版本漂移、缓存/登记缺失、无效/重复 JSON、schema、未知/远程市场、目录不同、重复声明、越界/外链、market/plugin 元数据父目录外链、非法 scope、大小、FIFO、叶子链接、取消、数量上限。读取中接管会话后拒绝旧结果。最终 app 全包 race 包含这两项新增父目录边界。 |
| HTTP 契约 | 未授权、外部 Origin、额外路径字段、null、未知会话五类边界通过。有效红灯为 [http-route-red.log](W02-08-claude-installed-catalog/http-route-red.log)，原路由 404；[http-green.log](W02-08-claude-installed-catalog/http-green.log)转绿。最初 `http-red.log` 用错测试名、没有测试运行，保留但不作红灯依据。成功数据读取由 Application 直接调用验证，未运行真实 HTTP listener。 |
| 真实本地安装 | [27 项](W02-08-claude-installed-catalog/native-result.json)通过。真实 CLI 添加自有 directory marketplace，执行 `plugin install catalog-hook@hook-local --scope user --json`，然后生产 Application/SQLite/Claude Runtime 创建一个隔离会话并转人工。读取真实安装登记；将测试 manifest 从 1.0.0 改为 2.0.0、再清空测试 known markets，分别核对漂移和未知来源。各次保留原 ID、快照、控制模式、操作数量及登记文件。 |
| 浏览器 | [22 项](W02-08-claude-installed-catalog/browser.json)通过：手动读取、完整来源键、准确路径/双版本、漂移、不完整/未知/远程/错误提示、失败/快照变化/断线清空、空 POST 与无执行命令。当前构建回放实际 Application 响应；HTTP/SSE、远程/错误和新快照为受控夹具，不代表浏览器实时连接 CLI。无页面异常；注入 409 的控制台网络错误符合预期。 |
| 独立审计 | [21 项](W02-08-claude-installed-catalog/audit.json)通过，从实际登记文件、改后的 manifest、导出响应和 journal 核对路径、版本、绑定和零模型输入，未把测试结果布尔值当独立证明。 |
| 工程检查 | [app/console 全包 race](W02-08-claude-installed-catalog/race.log)、nativeclaude 标签 app/console vet、gofmt、TypeScript 检查与 [make dev-build](W02-08-claude-installed-catalog/build.log)通过。Runtime/bridge 生产源码未改，不重复无关原生矩阵。 |

原始目录 `/tmp/netcap-w0208-installed-catalog-3487476731`，工作记录 `/tmp/netcap-w0208-installed-catalog-work`。固定 Claude Code **2.1.280**、SDK **0.3.280**；自有 HOME/XDG/profile/项目与测试凭据引用，供应商端点为不可用本地地址。测试插件只含工具阶段 hook，没有 SessionStart。journal 仅 `initialized`/`closed`，没有模型 input、工具或 hook 执行。Application 正常 Close，命令与浏览器已结束；没有修改用户原生配置、共享后台或旧二进制。

首轮浏览器执行在测试脚本修改 workspace 摘要时访问了不存在的快照字段，见 `browser-first-error.log`；改为只更新真实存在的字段后重跑通过，没有为此修改产品。截图已查看：[桌面详情](W02-08-claude-installed-catalog/native-1440.png)、[窄屏版本漂移](W02-08-claude-installed-catalog/drift-390.png)。

用户再次提供 `/tmp/netcap-console-bp2rp6xu`；读取核对仍为 33/33，通过情况已在 [插件执行记录](W02-08-plugin-execution.md)归档，不改旧证据、不计作本片新的原生轮次。五项其他 WSL 请求仍待新结果。

## 复验与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app -run '^TestInstalledPluginCatalog' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache NETCAP_CLAUDE_NATIVE_TEST=/home/zzw/.local/bin/claude \
go test -race -tags nativeclaude ./internal/app -run '^TestClaudeNativeApplicationInstalledPluginCatalog$' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags nativeclaude ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 goals_W/evidence/W02-08-claude-installed-catalog/audit.py
```

[浏览器脚本](../../tests/claude_installed_plugins_browser.js)以归档的 `native-browser-responses.json` 替换 `__FIXTURE__`、当前 web/dist URL→绝对路径映射替换 `__ASSETS__`，写入 `.dev/w0208-installed-catalog/run-browser.js` 后通过 Playwright MCP 执行。归档 [source.diff](W02-08-claude-installed-catalog/source.diff)、[manifest.json](W02-08-claude-installed-catalog/manifest.json)记录本片源码、测试和产物；哈希对应此次归档时点。

- 旧 `bin/netcap` 未改：`e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。
- 新开发二进制：`758d3764aed997585d9c95ca86d68156ea511dd3de3a359a0f9139b7aed25855`。
- JS：`bd102bfa8eb890c8816a2c2a1b91976ee121ad8fd85b6ec4cf4eebc23b37eab2`；CSS 未变。
- **3355** 个原有 W 证据逐文件哈希不变；没有提交、推送或发布。

下一步继续可靠的受管来源预检与准确安装来源绑定，再接生产安装插件事务。方法任务/Gate、原生确认丢失后的安全恢复、共享后台/输入独占、成功/异步/通用 hook、Windows/Unix proxy/peer 与五项待回 WSL 对照仍是未完成条件；本片不关闭 W02.07/W02.08。
