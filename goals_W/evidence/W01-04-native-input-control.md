# W01.04 / W01.05 · 原生 attach 输入、审批共享与只读边界

2026-10-11。固定 Claude Code 2.1.280 的两个原生 `attach` 客户端能向同一个 worker 提交输入，并共享未提交草稿和审批选择。**这构成输入独占的失败证据**；41 项观察检查通过仍保留 `input_exclusivity=violated`、`goal_acceptance_met=false`。本片没有把原生后台接成生产写入入口。

| 实际路径 | 结果与边界 |
| --- | --- |
| 完整 UUID 与短作业选择 | `attach <完整 UUID>` 明确返回 `No job matching`、退出码 1；用清单短 ID 连接前后核对完整 UUID、worker PID/启动标识。生产 UUID 只读预览与原生 attach 的选择方式不同 |
| 两个 attach 同时在线 | 第一、第二客户端分别提交一次；第一端只输入 `NETCAP_NATIVE_INPUT=shared_`，第二端输入 `draft` 并回车，模型收到一次完整的 `NETCAP_NATIVE_INPUT=shared_draft`。原 worker 和完整 ID 保持 |
| 共享审批选择 | 第一端按实际界面的 `No` 位置移动选项，第二端回车；原工具请求留下拒绝回执，工具没有执行。未选择永久允许或 auto mode |
| 拒绝后的原上下文 | 原生拒绝结束当前轮次，不自动续发模型请求；下一条明确输入携带原 tool_use_id、错误标记和完整拒绝正文。CLI 附加的 token 提醒单独记录，不将整个内容块假定为逐字相等 |
| 工具运行时单端脱离 | 第二次 Bash 明确单次批准，只启动一次自有工具；第一端 Ctrl+Z 脱离后，第二端、原 worker 和原工具保持。释放测试栅栏后，完整 JSON 结果回到原模型上下文，工具不重放 |
| A/B 隔离与控制台关闭 | B 的独立原生会话只有 seed 主请求。关闭生产只读控制台后 A worker 和第二 attach 仍存活；最终只停止自有 profile，已采样的进程全部退出 |
| 生产读取与拒绝 | 空闲、审批等待、工具运行、工具完成四阶段都保持 `sampled / unknown / incomplete`。历史按原 ID 读取，20 次发送/恢复/接管/中断/审批命令全部 409；创建另因既有原生认证冲突被拒绝。读取未运行认证 helper、未增加模型输入或执行 Operation |

最终原生样本位于 `/var/tmp/nc-native-input-dfek7fwz`，本地归档的[结果](W01-04-native-input-control/final/result.json.gz)、[原始材料审计](W01-04-native-input-control/audit-final.json)与[浏览器结果](W01-04-native-input-control/browser-final.json)可交叉核对：

| 对象 | 完整身份 | PID / 启动标识 |
| --- | --- | --- |
| A 原生会话 | `d234c267-c5da-435c-8565-18ba1506caa2` | `1871195 / 14931958` |
| B 原生会话 | `9bde038d-412e-4eea-8270-839de5e18705` | `1871372 / 14932079` |
| A 两个 attach | 分别通过短作业选择连接 A | `1871501 / 14932163`、`1871556 / 14932306` |
| 原工具 | `OWNED_NATIVE_TOOL_OUTPUT`，仅 `run` 一次 | `1871750 / 14933048` |

A 共六条明确输入和一次工具结果续请求，B 一条 seed 主请求；包含原生标题/状态辅助请求后，本地供应商分别收到 25、4 次请求。审计从请求、主会话 JSONL、15 条测试按键记录、工具实际输出、各阶段进程身份、43 次 Handler 交换和 SQLite 重新推导上述结果，不用探针的通过布尔值代替原始证据。终端能力查询的自动答复不属于测试按键记录。四阶段读取不改变原只读绑定；SQLite 保留一条来源登记回执、零执行 Operation。

浏览器使用当前构建与四阶段实际 Handler DTO，HTTP/SSE 为回放；没有再次运行 CLI 或发送模型输入。57 项检查覆盖完整/短 ID 分列、原生状态声明、历史读取、缺少输入/审批/恢复控件、观察停止后不再采样，以及 1440px/390px 布局。回放不单独证明原生运行行为。截图见[审批等待窄屏](W01-04-native-input-control/browser/approval-pending-390.png)与[工具完成桌面](W01-04-native-input-control/browser/tool-finished-1440.png)。

本片还修复了实际界面问题：无空格的会话标题在 390px 视口把文档撑到 502px，并将“详情”挤出屏幕。允许标题容器收缩、正文换行并保留按钮宽度后，文档宽度恢复为 390px；三条局部 CSS 与 `web/dist` 一并更新。原始尺寸与临时样式诊断见[布局证据](W01-04-native-input-control/native-title-diagnostic.json)，最终浏览器在构建产物上通过，没有依赖临时注入样式。

验证入口与范围：

- [原生探针](../../tests/claude_native_input_control_probe.py)：41 项通过；真实 `--bg`、两个 attach、默认权限、单次批准及生产 Application/Handler。
- [独立审计](../../tests/claude_native_input_control_audit.py)：122 项通过；只读现有材料，没有重发原生输入。
- [浏览器入口](../../tests/claude_native_input_control_browser.js)：57 项通过，无未映射接口、页面异常或控制台错误。
- `make dev-build`：Web 类型检查、Vite 与开发 Go 构建通过。Python 语法、JavaScript 语法、文档引用及差异检查通过。
- 本片没有修改 Go/bridge/依赖，没有重跑无关的完整 race 或 SDK 测试；前一片的通过记录不计入本片。没有真实供应商调用或新的 Windows 宿主验收。

```bash
make dev-build
python3 tests/claude_native_input_control_probe.py
python3 tests/claude_native_input_control_audit.py /var/tmp/<探针打印的目录名> --output /var/tmp/native-input-audit.json
```

探针依赖本机固定 CLI、bwrap、用户命名空间、PTY 和本地 IPC。外层创建自有 `/var/tmp` profile，使用独立 user/mount namespace、自有 `/etc/claude-code` 和 Windows 注册表缺席夹具；不隔离 PID/网络。模型路由、认证 helper 和工具均为本地脚本，CLI 不继承用户凭据、代理或 hooks。浏览器模板的 `__FIXTURE__` 使用结果中的身份、验收标志和 `frames`，`__ASSETS__` 指向 `web/dist` 文件，`__OUTPUT__` 指向自有截图目录；本次实例化入口保留在本地归档。

开发二进制 SHA-256：`5c86bc5339ed3a9c543b155bde4aed4553de8a366f39aebfc97f938e94a9cddb`。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。被测探针、构建、报告和原始材料的摘要见本地[归档清单](W01-04-native-input-control/manifest.json)。原始材料按显式白名单归档，由 `.gitignore` 排除；不复制整个原生 profile，排除控制台访问令牌及 daemon/session 私钥。

保留的失败与修正：

- 初步脚本命名 `concurrent.py` 遮蔽标准库，导入即失败，尚未启动 CLI；更名后取得首次双端输入反例。
- 正式探针将完整 UUID 的明确拒绝误等为界面超时；现记录原生退出与错误，再独立验证短选择对应的完整身份。
- 探针先假定拒绝会立即发送工具结果，后又假定下一请求的整个内容块与 JSONL 相等；原始材料分别证明原生中断行为和追加 token 提醒，修改的是夹具判据，没有放宽拒绝或原结果要求。
- 首次浏览器回放漏映射自动历史读取；补采四阶段真实历史响应。窄屏溢出是另一个真实产品问题，已先复现再修复。失败日志、源码版本和截图均留在本地归档。

W01.04/W01.05 保持 `in_progress`，整体仍为 **9/40 done**。本样本验证共享行为与只读边界，没有建立排他输入权；不能把前一片 SDK bridge 的单控制器规则推广到原生 `attach`。严格独立前台、worker 丢失前台工具回执后的完整恢复、完整配置/权限/冻结、第二有效 Windows/WSL 环境与其他首版条件继续保留。下一步需验证实际可用的原生连接管理/输入交接机制；只有明确的原生正例或用户接受的需求修订才能关闭对应硬要求。
