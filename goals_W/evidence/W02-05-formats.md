# W02.05 · 通用 manifest 与非文本资源收口

2026-09-30。本片完成[资源包切片](W02-05-package.md)保留的两项缺口：通用插件 manifest 的选择/覆盖规则，以及非文本能力资源的分类、诊断和完整搬运。结合[能力库首片](W02-05-library.md)的版本管理证据，W02.05 的库管理验收已完成；原生逐次选配、实际加载和权限效果继续在 W02.06–W02.08 验收。

## 实现与契约

- Codex 通用包以根 `plugin.json` 为身份、`skills/` 和 `mcp.json` 为规范入口。`extensions.com.openai`（包括空对象）整体替代 `.codex-plugin/plugin.json`；只有缺省时才使用后者。已替代的旧 overlay 和 `.mcp.json` 明确提示，不加入当前 Codex 组件清单。
- Codex 显式 hooks 替代 `hooks/hooks.json` 默认发现，支持路径、内联对象及数组。Claude 保留自己的默认/附加来源规则；两类来源在同一包中分别诊断，不用统一最后写入覆盖。
- 诊断新增 `plugin_sources`：身份入口与实际选用的静态配置来源。展开注册 MCP 标识映射、展示资源相对路径，以及通用 MCP transport；缺失文件、非法路径或错误 transport 可定位。注册标识和 CLI 数据目录变量是运行绑定，不复制账号或持久数据，不要求用户替原生 CLI 提供其内建变量。
- 诊断新增 `resources`，按内容标识图片、WASM、其他二进制及已支持压缩格式。字节和权限不变；不执行、不重编码资源。二进制本身不再被误判为凭据；未知格式仍明确为已知模式检查。
- 已知凭据检查继续作用于二进制中的可识别字面值和 UTF-16 文本，SQLite 文件可按内容识别。ZIP/gzip/USTAR tar 成员继续检查文件名、内容和嵌套容器；串接 gzip 保留各成员名称。解压累计上限 32 MiB、2048 成员、深度 3，不落盘解压，不执行内容。
- 原资源包格式 v1、库 schema 5 和版本事务保持兼容。界面合并重复的二进制提示，逐文件类型/检查结果仍可展开；不会将静态检查写成原生已生效。

## W02.05 验收核对

| 条件 | 本片及此前的可追溯依据 |
|---|---|
| 共享 Skill/MCP 与两类专属资产可管理，保存来源/兼容/引用/版本 | 前片生产登记与历史；本片重跑共享 Skill/MCP、Codex/Claude 插件 ZIP 往返，并补通用结构、双 Backend 来源与非文本资产 |
| 两种配置独立引用 | 两套隔离工作台；Codex/Claude 实际会话各自引用同一受管资源，状态命名空间不同，模型请求到各自本地供应商 |
| 更新、停用、移除不污染在途历史 | 重跑 package suite：在途 v1→v2/停用/移除、历史 ZIP、原 ID 重启与下一轮；旧快照和文件不变 |
| 导出不夹带账号/会话状态或已知凭据 | 原路径/运行连接不进 manifest；直接文件、二进制内可识别字面值、UTF-16、重命名 SQLite 与嵌套压缩账号目录拒绝；错误不返回秘密值 |
| 来源脚本、同名冲突、缺失文件、不可拆分组件有定位 | library/package 历史证据；本片 canonical/overlay 负例、路径/transport、MCP/app/hook 展开、整体组件边界及真实 UI 来源解释 |
| 静态结构不代替原生执行验收 | 所有生产诊断仍为 native unverified；单独隔离 probe 只证明三种通用包在固定 Codex 版本能安装并发现 Skill，没有模型调用或 hooks/MCP 加载结论 |

## 实际验证

| 入口 | 结果 |
|---|---|
| `go test -race ./...` | [通过](W02-05-formats-go-race.txt)。未受影响包可使用 Go 缓存；之后新增 UTF-16 与运行数据变量用定向 race 复核 |
| 格式/资源定向 Go 测试 | [实现后通过](W02-05-formats-tests.txt)；[最终定向 race](W02-05-formats-targeted-final.txt)覆盖三种 manifest、空对象替代、默认 hooks 替换、缺失资源、transport、PNG/WASM/未知二进制往返、ZIP/gzip/tar、串接成员、路径穿越/解压预算/深度、UTF-16 和 CLI 数据变量 |
| `go vet ./...`、`make dev-build` | [vet](W02-05-formats-go-vet.txt)与[最终构建](W02-05-formats-build.txt)通过，构建包括 TypeScript 检查 |
| `python3 tests/capability_plugin_format_probe.py` | [原生发现记录](W02-05-formats-native-discovery.json)：根 manifest、内联扩展、旧 overlay 回退三种包都能安装并发现 Skill；0 模型请求 |
| `python3 tests/capability_formats_smoke.py --netcap .dev/bin/netcap --hold --info .dev/w0205-formats/fixture-info.json` | [新增 6 项](W02-05-formats-native.json)及[资源包回归 10 项](W02-05-formats-package-regression.json)通过；[最终资产诊断](W02-05-formats-library.json) |
| `tests/capability_formats_browser.js`，固定 Playwright CLI | [12 项通过](W02-05-formats-browser.json)：来源解释、替代提示、资源类型、两种宽度、超过 1 MiB 的 ZIP 下载/导入、固定来源、重载与存储边界 |
| `tests/capability_package_browser.js` | [17 项回归通过](W02-05-formats-package-browser-regression.json) |
| `python3 tests/native_mcp_selection_smoke.py --netcap .dev/bin/netcap` | [28 项通过](W02-05-formats-mcp-regression.json) |
| `python3 tests/execution_apply_smoke.py --netcap .dev/bin/netcap` | [18 项通过](W02-05-formats-apply-regression.json) |

新增生产样本有 PNG、最小 WASM、1.5 MB 确定性二进制测试数据和内嵌 ZIP。通过生产导出/导入接口逐文件核对字节与权限；两类真实 CLI 的会话引用和恢复保持原绑定，没有运行携带的失败脚本或把资源指令送入模型。两个浏览器下载使用独立产物名称，分别与生产 ZIP 逐字节比对。

截图：[预览顶部 768](W02-05-formats-export-768.png)、[资源与确认 768](W02-05-formats-export-resources-768.png)、[预览顶部 1440](W02-05-formats-export-1440.png)、[资源与确认 1440](W02-05-formats-export-resources-1440.png)。已查看截图；长预览内部可滚动，确认操作可达，没有横向溢出。最终浏览器无运行时异常；console 为 0 errors / 0 warnings。

## 失败与修正

[首次测试](W02-05-formats-initial-tests.txt)复现了原实现未处理 canonical root、overlay 选择、显式 hooks 替代以及拒绝普通二进制的问题，修复后原测试通过。截图发现逐文件重复提示占用过多空间，界面合并后重新构建，并重跑生产与浏览器检查。另一个仅在测试产物中的问题是两套浏览器脚本共用下载文件名，导致比对错文件；改用独立文件名，最终两个下载都与各自生产 ZIP 一致。

## 结构依据、版本与范围

官方依据沿用本轮已读取的 [OpenAI 插件结构](https://developers.openai.com/plugins/build/plugins)和 [Codex Skills](https://developers.openai.com/codex/skills/)，Claude 路径规则经 Context7 查询[官方插件参考](https://code.claude.com/docs/en/plugins-reference)。Go 内容类型识别以当前 `go doc net/http.DetectContentType` 为准；它是内容标识，不是格式有效性证明。

固定 Codex proxy 0.157.1 / server 0.158.0、Claude 2.1.280、SDK 0.3.280、Go 1.26.0，开发构建 `.dev/bin/netcap`。所有 CLI home、资源、供应商和会话状态在独立 `/tmp/netcap-console-*`。原始目录、最终源码/构建/证据哈希见 [manifest](W02-05-formats-manifest.json)；前两片 manifest 保持原样。

复跑浏览器时保持上述 fixture `--hold`，使用其本地 token_file 登录独立 CLI 命名会话，把脚本的 `/*FIXTURE*/{}` 替换为 info 中的 `packages` 和 `source_root`。原资源包浏览器脚本的临时输出目录可替换为 `.dev/w0205-formats/`；两个下载文件名需独立，创建普通无效 ZIP 样本用于错误路径。认证文件 mode 0600，收尾删除，只关闭自有浏览器/fixture。

保留限制：静态检查不能证明所有动态依赖、语义内容或任意隐藏秘密；未知编码和其他容器格式仍只做已知模式检查。未做最大合法体积的浏览器压力验收；历史回收和 abrupt-crash 孤立副本清理仍未增加。原生 hooks/MCP/Skill 的实际启停、安装版本恢复和完整合并分别归 W02.06–09；未调用真实供应商、停止用户后台、迁移用户数据、发布或外发，也未执行 Windows 宿主与真实角色任务。

下一项 W02.06；W 系列目标保持 active。
