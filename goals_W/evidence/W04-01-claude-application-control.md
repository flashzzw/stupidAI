# W04.01 / W01.05 · Claude 原查询跨控制进程崩溃接续

2026-10-11。工作台创建的 Claude SDK 查询与 Go 控制连接分开存活。Windows 控制通道断开、Go 在工具执行中或等待审批时被 SIGKILL 后，新 Go 可以核对并连接原 bridge；原审批、输入、工具输出和执行快照保持，不重复派发。人工接管跨重启及原输入完成保留，空闲后仍需显式恢复。

范围为 Windows PowerShell 5.1.19041.6157 → wsl.exe → Ubuntu-24.04 / zzw → 生产 Handler/Application/SQLite/Runtime → SDK 0.3.280 / Claude 2.1.280。使用独立 A/B profile、自有中文/空格/单引号工作目录和本地脚本供应商。Go/SDK/CLI 位于 bwrap 用户和挂载隔离中，PID/网络空间共享以便核对实际进程并访问自有 loopback 服务；受管策略和 Windows 注册表缺席夹具仅在该隔离中生效。外层 PowerShell 是真实 Windows 进程，未调用真实模型供应商。

最终开发构建 SHA256：`e514cb3e17561f683f2ed599b1f9eef3e57f3f107e921726541def4d96ba76b4`。旧 `bin/netcap` 保持 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。未修改 Web、SDK 依赖或锁文件。

## 修复与边界

- 先修复回复操作的生命周期：原请求已 submitted、原输入仍运行时，严格匹配原 native ID、输入、复合请求 ID、connection、工具和回执，保持 respond 为 running；原输入真实终结后才完成。缺失、不匹配或离线继续 unknown。九个子场景先红后绿。
- 原 Go 崩溃实验中，工具实际上完成并将完整 JSON 返回 Claude，但 stdin EOF 触发 Session.close，bridge 没有保存原生 result；工作台持续 uncertain。首轮探针误把 uncertain 当终态，修正为沿原操作观察 25 秒后，仍确认日记缺少终态。不能把这个结果描述成原工具没有完成或输出文件丢失。
- 最小回归用独立 Go 进程、生产 transport/main 和受控 Session，在 0.30 秒内复现“原查询被关闭、换成新 bridge”。修复后该路径通过，并分别覆盖未批准和已提交回复。真实 SDK/工具结论由下表单独验证。
- Go 沿用原 JSON 协议，启动自有 bridge 的私有 Unix 控制端点；0600 socket、目录 FD、SO_PEERCRED、PID/启动标识、端点 inode 和状态目录共同用于连接核对。长中文路径不截断。bridge 保留原实例锁、SDK 查询、事件读取与 pending 回调，失去连接不关闭查询。
- 第二控制器及尚在结束中的旧命令被拒绝；迟到回执留在原连接，不跨连接交付或重放。端点/进程/声明版本不匹配不创建替代 bridge。启动实例和 Go 登记的继承环境摘要在重连及复用时检查；环境漂移的最小失败已先红后绿，显示名称变化不改变执行绑定。这不是完整实际配置来源证明。
- 最终检查还复现了服务端 destroy 连接不触发读取器结束的问题：空连接及已接受命令结束后均没有释放记录。补齐 close 事件处理，两个 Node 通道回归先红后绿；在途命令仍须结束后才能交接。随后重跑三种宿主场景、独立审计和浏览器，仍为 77/156/44 项通过。
- 正常退出/空闲回收仍显式关闭自有 bridge；直接 stdio 入口仍按 EOF 关闭，旧 bridge 保留兼容路径。自有 bridge/CLI 本身崩溃、整机重启和长期无人重连仍有独立条件，本片不宣称全部故障恢复通过。

## 最终原生验证

入口：`python3 tests/wsl_claude_application_smoke.py --case CASE`，默认读取 `.dev/bin/netcap`。

| CASE | 结果 | 实际运行目录 |
|---|---|---|
| `host-disconnect` | 23 项通过：终止自有 Windows HTTP relay，原 Go/bridge/CLI/工具保持；新 relay 读取原操作并取得完整结果 | `/var/tmp/netcap-wsl-claude-ni0gpk4w` |
| `go-crash` | 26 项通过：工具运行时 Go 实际退出 -9，新 Go 连接原 A/B bridge；原 CLI/工具不换进程，原 send/respond 完成 | `/var/tmp/netcap-wsl-claude-p3c8tjk1` |
| `pending-go-crash` | 28 项通过：批准前 Go 实际退出 -9；核对后原请求/connection/快照不变，明确批准后才执行一次 | `/var/tmp/netcap-wsl-claude-wyrjlbbw` |

三组共 77 项。每组仅一次 Bash/Python 工具执行、三次 A 本地模型请求；B 零模型请求。原工具完整中文 JSON 同时存在于本地完成文件、原生历史及模型收到的 tool_result。每组原输入和回复完成后仍保持人工接管；在途恢复被拒绝，空闲显式恢复幂等，下一条明确输入保留原 ID/快照。

等待审批的首轮断言读到了启动阶段的 `unverified`，身份和快照实际上相同。探针现保留该阶段，并等待生产核对恢复 pending 后验证及回复，不把临时 unknown 当终态。早期原生失败和修复后的中间正例均归档。

## 独立复核与页面

`python3 tests/wsl_claude_control_audit.py RUN_DIRECTORY --output AUDIT_JSON` 从原始 Windows 字节重新合并每个 child 的完整流，再解析 HTTP，避免逐块解码丢大帧。三组 156 项通过（49/53/54），核对 228 对 HTTP 请求/响应、SQLite 完整性及每组六个成功操作、原审批、日记中的两条输入/两个成功 result、单次工具及精确输出归属。所有帧逐值匹配实际 Handler 响应；每组 B 的原生状态归属独立。初次审计错用 relay 的 `receiver` 字段，按实际 `process` 契约修正，失败日志保留。

三组分别核对 9/10/10 个不同 Linux PID/启动标识均已退出，包括被第二 relay 覆盖的第一接收器、A/B bridge 和 A/B CLI；每组三个 Windows 子进程及宿主退出。原始身份、退出记录及本地服务收尾均保留。

浏览器入口 `tests/wsl_claude_control_browser.js` 使用实际 Handler 帧和原生回执，44 项通过：恢复中的审批禁用、核对后的原审批可答、回复仅提交一次、在途 send/respond 显示执行中、完成后仍人工接管、断连禁用，以及 1440/390 宽度的原 ID/状态展示。已查看桌面与移动端截图，没有水平溢出、页面异常或未映射请求。命令按钮检查的是原请求身份；原生执行由前述 Windows 测试证明。

浏览器运行环境缺少全局 URL，改用已有路径解析方式；随后发现回放夹具没有发出 SSE change，补上页面正常依赖的刷新信号后通过。两次失败记录保留，未为此修改产品 UI。早期只读输出复核也曾把带原生提醒的整个 tool_result 当作一份 JSON，后按原文逐行独立核对完整对象；没有重跑工具来消除该审计假设。

## 检查与交付

| 检查 | 实际结果 |
|---|---|
| Controller 生命周期/绑定与回复核对 | 21 个行为/子场景通过 race；含实际 Go SIGKILL、第二控制器、在途旧命令、长路径、六类连接拒绝、启动环境及直接 stdio EOF |
| 完整 race | `go test -race ./internal/runtime ./internal/app ./internal/console` 通过；后补的直接 stdio/连接销毁分别验证；最终 21 个相关 Go 行为/子场景重新通过定向 race |
| 原生标签 vet | `go vet -tags='nativeclaude nativecodex' ./internal/runtime ./internal/app ./internal/console ./cmd/netcap` 通过 |
| 通道关闭 | `node tests/claude_controller_channel_test.mjs` 两项通过，空闲销毁及在途命令结束后均释放原连接 |
| 构建 | `npm --prefix bridges/claude run build`、`go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap` 通过 |
| 脚本与差异 | Python AST、Node 语法、Go 格式及 diff 检查通过 |

复跑本地连接契约可用 `make claude-controller-contracts`（先按原项目入口安装 bridge 依赖）；原生验证仍用上方 Windows 探针。

源码、测试和本报告同步 Git；原始材料、失败记录、截图、源码/构建副本与逐文件哈希在[本地归档](W04-01-claude-application-control/manifest.json)，遵循[证据约定](README.md)，不提交本地配置、访问令牌或原生状态。既有证据保留。

W01.02/W01.04/W01.05/W04.01 保持 in_progress，整体仍 9 done / 8 in_progress / 23 pending，Goal active。SDK 工作台的控制进程恢复不等于原生 supervisor/worker、独立前台或输入独占通过；第二有效发行版/用户、桌面与休眠/重启、完整来源/动态权限及全部跨包硬要求保留。下一片继续原生后台的生产输入交接或第二有效宿主环境，沿已有证据推进，不重复无新条件的批准时序实验。
