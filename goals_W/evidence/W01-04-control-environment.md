# W01.04 / W04.01 · 控制链路环境阻塞

日期：2026-10-02（Asia/Shanghai）。本片尝试继续 Claude 原生后台控制，并检查 Windows 宿主入口。两条路径均在环境前置失败；没有通过后台控制或 Windows→WSL 验收。W01.04 已完成的[原生清单采样](W01-04-native-status.md)仍有效，但不提供输入权或完整执行证据。

## 实际结果

| 检查 | 实际结果 | 原始记录 |
|---|---|---|
| 固定 Claude 2.1.280 启动自有后台 | `--bg` 退出 1；supervisor 在 `control.sock` 监听处报告 `EPERM`，立即退出 | [启动/清单/状态](W01-04-control-environment-background.json)、[daemon 日志](W01-04-control-environment-daemon.txt) |
| 启动失败后的原生状态 | `agents --json --all` 退出 0、返回 `[]`；`daemon status` 退出 1、`not running`，roster 不存在。这不算控制成功 | 同上 |
| Unix 匿名 socket 写入 | 自有 socketpair 的 `send` 被拒，errno 1 / `EPERM` | [探测 JSON](W01-04-control-environment-probe.json)、[输出](W01-04-control-environment-probe.txt) |
| Unix 及本地 TCP 监听 | 自有 Unix bind/listen 与 127.0.0.1 临时 TCP bind/listen 均被拒，errno 1 / `EPERM` | 同上 |
| Windows interop | WSL 声明及 PowerShell/wsl.exe 文件存在；只读版本命令退出 1，PowerShell 未启动，WSL 报 `UtilBindVsockAnyPort:307: socket failed 1` | [宿主文件检查](W01-04-control-environment-host.json)、[直接启动输出](W01-04-control-environment-windows-readonly.txt)、探测 JSON |

原生日志同时提到未发现认证 token，但本次明确的终止原因是 `bg_manager_failed` 与监听 `EPERM`；没有据此宣称认证已满足。文件存在和 WSL 环境声明均不能证明 Windows 宿主可执行。

## 隔离与版本

CLI 使用 `/home/zzw/.local/share/claude/versions/2.1.280`；[帮助输出](W01-04-control-environment-help.txt)和 [manifest](W01-04-control-environment-manifest.json)固定本次接口及二进制哈希。后台启动使用独立根目录 `/tmp/netcap-w0104-bg-n24t072j`，profile 为其 `claude` 子目录、cwd 为其 `work` 子目录，合成 apiKeyHelper 与不可用本地端点 `http://127.0.0.1:1`。未发送模型提示，未调用真实供应商，未停止共享后台。

新增 [workbench_environment_probe.py](../../tests/workbench_environment_probe.py)只检查自有 socket、临时目录及只读 PowerShell 版本启动；所有自有 socket 自动关闭，临时 socket 目录自动清理。探测不启动 CLI、不修改已有 daemon。它按真实结果返回：全部前置通过才退出 0，否则退出 1；本次 `prerequisites_met: false`、退出 1。

## 验证与收尾

[验证记录](W01-04-control-environment-validation.json)：Python AST 语法检查通过；环境探测、原生后台启动、Windows 只读启动均失败。本片没有修改产品代码或构建，因此未重跑 Go 测试、浏览器或构建，历史通过不计作本轮执行。

[收尾检查](W01-04-control-environment-cleanup.json)核对 725 个旧证据不变、上一片记录的产品源码/构建哈希不变及旧 `bin/netcap` 不变；按隔离 profile/cwd 检查自有残留进程。临时启动记录保留为诊断材料，未对其他后台执行清理。变更与归档时点哈希分别见 [diff](W01-04-control-environment-diff.patch)和 [manifest](W01-04-control-environment-manifest.json)。未提交、发布或发送外部消息。

## 恢复入口与未完成条件

在允许所需本地 IPC、监听及 Windows interop 的运行环境中，从仓库根目录执行：

```bash
python3 tests/workbench_environment_probe.py --output /tmp/netcap-environment.json
```

当前环境无需反复重跑同一失败矩阵；本工具不修改运行权限。探测通过只代表基础前置，随后依次恢复实际验收：

1. 按 [stdio 启动诊断](W02-06-stdio-startup.md)重跑原生 MCP 最小对照，再补 W02.06 原生登录、SSE、动态认证、原 ID 重试、工具权限和来源冲突矩阵。
2. 重跑隔离的 A/B supervisor/worker 控制、原 ID 退出恢复与显式交接；W01.05 单独核对在途工具次数、结果归属和审批，不能用 roster idle 清除未知占用。W01.02 的生产 serve/proxy/peer/认证及存活共享后台故障链路也需实测。
3. 按 [W04.01](../W04-platform-plugin.md#w04-01)从真实 Windows 进程发起 WSL 控制，核对发行版、用户、cwd、空格/非 ASCII 路径、参数、退出/重连及失败报告。只读版本启动不能替代这些样本。

本轮按任务前置核对了其他路径：W01.03 严格独立前台仍有既有控制接口缺口；W01.06 依赖前述身份/控制/工具条件。W02.07/08 等待 W02.06，W02.09 等待 W02.04–08。W03.02 等待真实用户试用反馈，W03.01/03 已完成。W04.02–05 依赖宿主、候选和前序硬要求；W05 依赖 W04.05，W06 依赖 W05，W07 依赖底座及业务闭环。没有通过新增模拟层或改变需求绕过这些前置。

W01.04 与 W04.01 更新为工程 `blocked`，累计仍为 8/40 done。严格六入口、输入权、工具结果连续性、真实插件/协作及 Windows 硬要求全部保留。工程状态与原生 Goal 状态分开；本片不将整个 Goal 标为完成或暂停。
