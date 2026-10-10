# W02.09 · Codex 当前磁盘配置来源

日期：2026-10-06。范围是固定 Codex 0.158.0 原生磁盘合并来源、字段来源、文件版本与受管要求的只读采样。W02.09 仍为 in_progress，W 系列 9/40 done；没有完成新增来源的持久绑定、完整权限或四配置原生联合验收。

## 原生事实与实现

先运行真实原生契约实验，17 项通过：同一受信项目内，近层配置覆盖父层；被覆盖父层变化仍改变原生层版本；删除近层文件后采用父层；取消项目信任后保留被禁用层并采用用户层。配置层版本不包含文件排版或权限。修改磁盘后，`config/read` 与新线程采用新值，已加载线程仍报告旧值；线程显式覆盖也不进入该磁盘读取接口。

依据这些边界接通 `POST /api/sessions/{id}/configuration/catalog`：只接受空 JSON、使用保存的 profile/cwd，读取前后核对 Application 绑定/Generation。Runtime 连续两次采样 `config/read(includeLayers:true)`、全部受管要求及文件摘要；变化或缺失时拒绝。保留原生层身份摘要/顺序/版本/禁用状态及文件存在/权限/字节摘要，固定公共字段关联其采用的来源层。其他字段来源、完整层内容、合并值与受管要求仅保留摘要。配置正文、自定义键、受管名称、原始错误和禁用原因不传出。

界面显示当前来源清单，可比较本页两次读取的合并值、字段来源、来源层/文件与受管要求变化。旧执行快照保持原样；页面明确说明 `disk_configuration` 不是加载线程配置，也不代表输入许可。这里还没有将新来源记录接入执行快照或新增输入拦截。

协议依据：复用仓库 [Codex App Server 资料](../../Codex%20App%20Server.md) 关于 `config/read` 读取磁盘有效配置的说明，并从固定 0.158.0 二进制重新生成 [类型摘录](W02-09-config-sources/schema/ConfigReadResponse.ts)。[配置层来源](W02-09-config-sources/schema/ConfigLayerSource.ts) 与 [配置层](W02-09-config-sources/schema/ConfigLayer.ts) 保留原文，不当作独立可编译 SDK。

## 验证与实际失败

| 层次 | 结果 |
|---|---|
| 真实原生契约 | 17 项通过。隔离 profile、Git 项目、父子目录、三个无模型输入的线程；配置层与字段来源使用真实协议回执。 |
| Runtime | 18 个场景通过：缺层/缺配置/缺字段来源、错误身份/版本、重复层、孤立或禁用层的字段来源、RPC/受管失败、配置/文件/策略/未展示元数据在读取期间变化，均不返回部分成功或原始私密正文；省略与 null 禁用原因均按实测处理。 |
| Application/HTTP | 9 个绑定/只读场景及 5 个 HTTP 认证/同源/参数边界通过；拒绝错误 ID/cwd/来源/作用域、完整性夸大、缺时间、期间转人工或登记改变。 |
| 真实生产链路 | 24 项通过：Application 创建后经 Handler 读取初始、排版/权限变化、解析值变化、撤销项目信任四组来源；保留原 Scope/快照，不新增模型输入或操作。16 个带调用标记的请求均为两种读取 RPC。 |
| 浏览器 | 24 项通过。使用真实 Handler 四组回放，另注入明确的读取不稳定错误；桌面/390px、字段采用来源、文件与解析值变化区分、禁用层、旧快照、失败撤下结果、断线禁用与只发空对象的绑定读取通过。 |
| 回归与构建 | app/console 全包 race、相关 Runtime race（排除已知依赖监听的 OpenCode 路径）、最终 Runtime 新场景、tagged 产品包 vet、TS/Vite 与开发构建通过。 |

首次 HTTP 边界测试遗漏同源 Origin，实际先返回 403；首次原生 Handler fixture 遗漏 JSON Content-Type，实际返回 415。补齐测试请求后，真实生产解析又误拒绝启用配置层：生成类型的 `disabledReason: string | null` 在原生启用层实际被省略。修复读取器接受省略/null，仍拒绝非字符串禁用状态；该原始 409、原生响应及最终通过均保留。浏览器首次假设断线后折叠区仍展开，角色定位超时；修正为重新展开后核对禁用，不改变产品断线失效机制。

## 复现与产物

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app ./internal/console -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime \
  -run '^Test(Codex|ReadCodex|VerifyCodex|Hook|FileSource|ClaudeSettings|MCP)' -count=1
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet -tags=nativecodex ./internal/... ./cmd/...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build

GOCACHE=/tmp/netcap-w0206-auth-go-cache \
NETCAP_NATIVE_CODEX_TEST=/home/zzw/.codex/packages/app-server-daemon/releases/0.158.0-x86_64-unknown-linux-musl/bin/codex \
NETCAP_NATIVE_CODEX_PROXY_TEST=/home/zzw/.codex/packages/standalone/releases/0.157.1-x86_64-unknown-linux-musl/bin/codex \
go test -tags=nativecodex -race ./internal/runtime \
  -run '^TestCodexNativeConfiguration(Layers|Catalog)$' -count=1 -v
```

浏览器入口 [configuration_catalog_browser.js](../../tests/configuration_catalog_browser.js)：将 `__FIXTURE__` 替换为真实生产测试的 `result.json`，`__ASSETS__` 替换为当前 `web/dist` URL 路由到绝对文件的映射，用已配置 Playwright MCP 执行。截图目录 `.dev/w0209-config-sources`，不启动监听服务。

原始工作目录 `/tmp/netcap-w0209-config-sources-work`；契约目录 `/tmp/netcap-w0209-config-layers-1636513457`；生产成功目录 `/tmp/netcap-w0209-config-catalog-513968893`。归档：[原生契约](W02-09-config-sources/native-contract.json.gz)、[生产结果与 trace](W02-09-config-sources/native-catalog.json.gz)、[浏览器结果](W02-09-config-sources/browser-final.json)、[桌面](W02-09-config-sources/changed-1440.png)、[窄屏](W02-09-config-sources/untrusted-390.png)、[差异](W02-09-config-sources/source.diff)、[核对](W02-09-config-sources/audit.json)、[哈希](W02-09-config-sources/manifest.json)。8 个自有原生进程正常退出，浏览器已关闭；5544 个旧证据、旧 `bin/netcap` 与 Claude bridge 源码不变。无真实供应商调用、提交或推送。

## 后续与限制

下一片把来源证据纳入原执行快照与输入前漂移检查，并保持已有 hook 文件采纳、新增项目目录、混合改配及冷恢复契约。磁盘来源不能被误当为线程已生效依据。系统文件本次为不存在的原生层，设备管理/企业下发/旧受管来源只按生成类型支持解析，没有真实受管宿主验收。完整外部依赖、动态账号权限、所有合并规则与来源准入、四配置原生联合、W02.07/08、真实模型方法/Gate、成功/通用/异步 hook、共享后台/输入独占、Windows 和五项待返回 WSL 结果继续保留。
