# W02.05 · 类型诊断与 ZIP 资源包切片

2026-09-30。本片在[能力库首片](W02-05-library.md)上完成生产 ZIP 预览、导出、导入和固定版本更新；补充 Skill/MCP/旧布局 CLI 插件静态诊断。W02.05 保持 in_progress：通用根 `plugin.json` 的扩展/覆盖规则尚未完整实现，非文本资源目前被拒绝，后续仍需收口。没有用静态检查或被动引用代替 W02.06–W02.08 的原生加载验收。

## 用户可见结果

- 能力卡显示入口、组件、相对路径问题、重复 MCP、所需环境变量和原生加载未知。Skill 使用 YAML v3 解析，支持多行和引用标量并拒绝重复键。插件组件随整体引用，不增加独立开关或权限。
- 当前或历史版本可预览并下载 ZIP。文件字节、目录、权限和摘要保留；清单只保存资产定义，不包含原绝对路径、会话或运行连接。
- ZIP 可在另一个隔离工作台预览后登记新资产，或更新已有 ID。先完整解码/校验，再写入唯一版本目录并提交既有库事务；失败持久化和并发重复清理本次未提交副本。
- 来源、引用或登记修订变化使预览过期；导入重试沿用 command_id 回执。旧执行和旧版本保留，浏览器不把包字节写入 sessionStorage。
- 只给两个 import HTTP 入口增加 base64 所需请求额度，继续执行本地认证、同源和未知字段检查。

## 验证结果

| 层次 | 入口 | 实际结果 |
|---|---|---|
| 应用/HTTP 定向 | `go test ./internal/app ./internal/console -run TestCapability -count=1` | [通过](W02-05-package-targeted-tests.txt)：往返、权限、历史版本、重启、预览过期、并发去重、持久失败清理、ZIP 路径/重复/链接/摘要/权限/超额解压、YAML、插件组件、外部映射、认证和请求大小边界 |
| 全包 | `go test -race ./...` | [通过](W02-05-package-go-race.txt)，未受影响的包使用 Go 缓存 |
| 静态/构建 | `go vet ./...`、`make dev-build` | [vet](W02-05-package-go-vet.txt)与[构建](W02-05-package-build.txt)通过；构建包括 TypeScript 检查 |
| 两工作台/真实 CLI | `python3 tests/capability_package_smoke.py --netcap .dev/bin/netcap --hold --info .dev/w0205-package/fixture-info.json` | [10 项通过](W02-05-package-native.json)，[资产诊断与历史](W02-05-package-library.json) |
| 生产浏览器 | 固定 Playwright CLI 命名会话 `w0205-package`，执行 `tests/capability_package_browser.js` | [17 项通过](W02-05-package-browser.json)，原生文件下载与生产 ZIP 逐字节一致 |
| 原能力库 | `python3 tests/capability_library_smoke.py --netcap .dev/bin/netcap` | [11 项通过](W02-05-package-library-regression.json) |
| 原生 MCP | `python3 tests/native_mcp_selection_smoke.py --netcap .dev/bin/netcap` | [28 项通过](W02-05-package-mcp-regression.json) |
| 原 ID 改配 | `python3 tests/execution_apply_smoke.py --netcap .dev/bin/netcap` | [18 项通过](W02-05-package-apply-regression.json) |

真实 CLI 样本包括共享 Skill、共享 MCP，以及 Codex/Claude 专属插件。从工作台 A 导出，经生产 HTTP 导入 B，逐文件核对字节与权限。B 的两种 CLI 独立引用共享和专属资产；两轮真实模型请求均指向本地模拟供应商。在途导入 v2、停用/移除后，原快照不变，重启后的原生下一轮仍保留原 ID/v1。模型请求未出现所引用资源的指令，未声称插件或 MCP 已原生加载。

截图：[导入预览 1440](W02-05-package-preview-1440.png)、[导入预览 768](W02-05-package-preview-768.png)、[能力库 1440](W02-05-package-library-1440.png)、[能力库 768](W02-05-package-library-768.png)。已实际查看截图，预览/确认操作可达，未见横向溢出；最终浏览器 console 为 0 errors / 0 warnings。

## 失败、修复与复核

[首次定向失败](W02-05-package-initial-failures.txt)复现了三处漏检：包清单重复 JSON 字段、MCP `../` 脚本依赖、导出大小写冲突。分别加入递归重复键拒绝、显式外部脚本路径检查和资源树大小写检查后，原失败与全包 race 均通过。

首轮生产脚本已走过往返与原生重启，但末尾测试命令 ID 含 `.`，被正式接口拒绝；修正测试 ID 后完整 10 项重跑通过。首轮浏览器在无效 ZIP 输入处使用了 CLI 沙箱不提供的 `Buffer`，改用独立测试文件后完整 17 项重跑通过。没有修改产品校验来适应这些测试错误。

## 版本、隔离与复跑

- 控制台 schema 5，Go 1.26.0；新增直接依赖 `go.yaml.in/yaml/v3 v3.0.4`。
- Codex proxy 0.157.1、server 0.158.0；Claude CLI 2.1.280，Agent SDK 0.3.280。沿用前序固定原生可执行路径。
- 全部资源/状态/CLI home/本地供应商位于各自 `/tmp/netcap-console-*`；只使用 `.dev/bin/netcap`，未替换 `bin/netcap`。
- 源码、构建、证据哈希及各原始目录见 [manifest](W02-05-package-manifest.json)。它记录本片归档时点，历史 library manifest 保持原样。
- 浏览器复跑：运行上述生产脚本 `--hold`，通过其 `token_file` 在本地登录，使用独立 Playwright CLI 会话。将脚本 `/*FIXTURE*/{}` 替换为 info 中的 `packages` 和 `source_root`，创建 `.dev/w0205-package/invalid.zip` 普通无效测试文件后执行 `run-code`。下载与截图写入 `.dev/w0205-package/`。认证文件仅为本地临时 mode 0600，未归档。

## 结构依据

本轮沿用已查询的官方资料：[Codex Skills](https://developers.openai.com/codex/skills/)、[OpenAI 插件包结构](https://developers.openai.com/plugins/build/plugins)、[Claude 插件参考](https://code.claude.com/docs/en/plugins-reference)。Claude 参考直连为 403，相关内容经 Context7 官方文档索引核对；YAML 以本地锁定 v3 的 `go doc` API 为准。当前官方通用根 manifest 与固定 CLI 已实测旧布局有差别，因此保留明确的未实现/未验证边界。

## 保留边界

1. 只检查声明结构与显式文件路径；动态 import、完整 CLI 合并、实际加载和权限效果未验证。二进制内容分类与通用根 manifest 扩展尚未完成，会阻止打包并给出原因，继续作为 W02.05 缺口。
2. 凭据检测覆盖已知账号/配置文件、已知密钥格式和敏感字段字面值，不能证明任意文本没有秘密。检测到问题时拒绝原始包，不自动删除或脱敏改写。环境变量引用保留，运行时值不读取、不打包。
3. 大小上限为 ZIP 40 MiB、资源 32 MiB / 2048 项。未执行最大合法体积的生产浏览器压力验收；HTTP 1 MiB 边界与超额解压已由测试覆盖。
4. 未增加历史回收或 abrupt crash 孤立副本清理；进程在文件完成但数据库提交前退出，可能留下未引用目录。不能把目录存在当作登记成功。
5. 未停止用户共享后台、调用真实供应商、迁移用户数据、修改原生安装、对外发送或发布；Windows 宿主、原生能力选择及真实角色任务不在本片通过范围。

下一步：继续 W02.05 的通用 manifest 与非文本资源可移植边界收口，再推进已限定路径的 W02.06；W 系列目标保持 active。
