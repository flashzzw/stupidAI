# W02.05 · 能力库与固定版本首片

日期：2026-09-30。完成生产能力目录、固定副本与版本引用管理的纵向切片，**W02.05 仍为 in_progress**。目录清单导出不等于资源包导出；声明兼容不等于实际原生可用。

## 已实现行为

复用 `CapabilityAsset`、`CapabilityReference` 和不可变执行快照。`manage_capability` 经 Application 事务保存库状态，预览核对版本、来源与已记录使用位置。支持登记、更新版本、停用/恢复新引用和移除登记；版本标识不能重用，同名 ID 保持独立。库变更不修改会话输入权、在途操作、旧快照或原生加载。

外部管理保留原来源；平台管理将所选资源复制到独立版本目录，核对完整树摘要和权限后才持久登记。副本包含脚本；复制使用 `os.OpenRoot` 限定来源范围，拒绝嵌套链接、特殊文件/权限与已知原生配置/账号/会话文件。失败保存清理本次未登记副本，并保留此前版本。已登记副本改变后阻止以同版本创建新引用；旧快照的来源检查保持有效。

当前复制限制为 2048 项 / 32 MiB。版本不自动回收，移除登记也不删除资源。崩溃若发生在复制完成但数据库提交前，可能遗留未引用副本；其存在不代表库事务成功。本片没有实现崩溃残留副本回收。

schema 5 保存 `capability_library`。启动外部定义与持久管理版本分别保留，冲突的启动 JSON 不得覆盖已保存历史；schema 4→5 在隔离库验证。执行配置登记沿用 schema 4 数据，不重建用户库。

UI 增加能力库分区、来源/版本/同名提示、管理方式、预览与引用列表；新建会话继续复用引用入口。兼容状态明确为 `declared_only`，插件整体声明不提供虚假的组件排除开关。

## 实际验证

| 层次 | 依据 | 结果 |
|---|---|---|
| Application/存储 | [全量 race](W02-05-library-go-race.txt)、[最终相关包](W02-05-library-targeted-tests.txt) | 版本保留、外部漂移、导入副本不受原目录变更影响、已管理副本篡改阻断、并发同 command_id 只登记一次、失败持久化清理、过期预览拒绝、同名 ID、启动冲突与 schema 4→5 保留迁移通过 |
| 真实 CLI + 本地供应商 + HTTP | [11 项结果](W02-05-library-native.json)、[生产快照](W02-05-library-snapshot.json)、[库记录](W02-05-library-library.json) | Codex / Claude 分别引用同一固定 Skill 副本；两份模型流式运行时更新 v2、停用、移除，原轮次成功且原 v1 快照不变；重启后原会话继续第二轮 |
| 复制与来源 | 同上及状态测试 | 副本包含脚本；原目录修改不改变已固定的执行依赖；原生 auth 文件不能导入成 managed 资源 |
| 目录清单导出 | HTTP 和状态测试 | `netcap.capability-catalog.v1` 只返回元数据/摘要，排除文件内容、来源路径、环境/账号/会话；样本中的凭据内容未进入导出，且 `source_required: true` 明确需要重新指定来源 |
| 原生能力语义 | HTTP/native 11 项 | 引用没有自动安装或加载 Skill；两条模型路由/凭据与历史各自正确，不把被动引用称为生效 |
| 浏览器 | [12 项](W02-05-library-browser.json) | 导入、引用真实 Claude 会话、更新影响、停用/移除、旧版本/会话跳转、刷新、1440/768 通过；无 pageerror / console error |
| 既有能力回归 | [28 项 MCP](W02-05-library-mcp-regression.json)、[18 项改配](W02-05-library-apply-regression.json) | Codex 原生选配/单次审批、原 ID v1→v2→v3 与历史版本保持通过；本片未重跑丢回执/unknown 故障组合 |
| 静态与构建 | [vet](W02-05-library-go-vet.txt)、[构建](W02-05-library-build.txt) | Go vet、TypeScript、Vite 与开发 Go 构建通过 |

实际命令：

```sh
go test -race ./...
go test -race ./internal/app ./internal/console
go vet ./...
make dev-build
python3 tests/capability_library_smoke.py
python3 tests/native_mcp_selection_smoke.py --netcap .dev/bin/netcap
python3 tests/execution_apply_smoke.py --netcap .dev/bin/netcap
```

浏览器：`tests/capability_library_smoke.py --hold --info ...` 启动自有夹具，将非敏感 assets 注入 `tests/capability_library_browser.js`，通过固定 Playwright CLI 的独立 `w0205-library` 会话运行。认证仅使用夹具 token_file，临时状态文件 mode 0600。截图：[1440 列表与历史](W02-05-library-1440.png)、[768 列表](W02-05-library-768.png)、[768 更新影响](W02-05-preview-768.png)。

所有原生测试使用隔离 `/tmp/netcap-*` 目录、真实固定 CLI 和本地模拟供应商；没有调用真实供应商、停止用户共享后台、搬迁用户数据或外发。固定版本：Codex proxy 0.157.1 / server 0.158.0、Claude 2.1.280 / SDK 0.3.280。被测源码、开发构建与归档证据哈希见 [manifest](W02-05-library-manifest.json)。

浏览器首轮失败来自测试对带子元素的引用段落做完全相等文本匹配；实际预览包含正确会话名、版本及快照 ID。改为检查相应引用容器后通过，没有改产品行为来绕过断言。

## 仍需完成的 W02.05 条件

- 可移植**资源包**的导出、导入与文件/凭据边界验证。当前只有目录清单；不能把元数据下载算作完整资产导出。
- 按资产类型核对入口、相对路径/脚本依赖、版本描述和 CLI 适配条件。当前完整树摘要只能证明所选文件的状态，不会发现未声明的缺失依赖；来源可读不等于兼容。
- 展开 CLI 插件隐含组件并定位不可拆分或重复定义。本片仅明确组件未知，不把该提示算作完成解析。
- 补足共享 MCP / 专属资产的库管理与兼容样本。实际原生 MCP、Skill、CLI 插件启用/排除分别继续接入 W02.06–08，完整有效来源归 W02.09。

下一片沿现有版本库继续资源格式、类型诊断和资源包路径。保留原任务全部验收，done 数仍为 6；本轮是有实现和实测依据的阶段进度。
