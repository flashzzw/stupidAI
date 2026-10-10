# W04.01 · Windows 发起 WSL 启动探测入口

2026-10-02。用户独立 WSL 终端已有 PowerShell 版本启动正例，本片准备真正由 Windows 进程发起 WSL 请求的最小探测。当前代理环境仍拒绝 Windows interop；宿主验收未通过，W04.01/W04 保持 blocked，累计 8/40 done。

## 实现

[Python 驱动与 Linux 接收端](../../tests/wsl_host_probe.py)和[PowerShell 发起端](../../tests/wsl_host_probe.ps1)沿现有隔离环境与固定 Claude 2.1.280：

- 从当前 WSL 明确选择发行版/用户，不猜测默认发行版。Windows PowerShell 通过 ProcessStartInfo/UseShellExecute=false 启动 wsl.exe，显式传 --distribution、--user、--cd、--exec。
- JSON/base64 仅传测试配置，Windows 原生参数使用 Python list2cmdline 编码，无 cmd.exe 或拼接 shell 执行。空参数、空格、中文、引号、反斜线及 shell 字面文本都要求逐值往返相等。
- Linux 接收端报告实际 uid/user/distro/cwd/argv，只在隔离配置运行 Claude --version；不提交模型、启动会话、监听或修改用户配置。正常目录、特殊目录、重复启动、CLI 缺失、发行版不存在及用户不存在共六种请求。
- Windows 记录平台、PowerShell 版本、PID、子进程退出码与有界等待；只结束本次创建的超时 wsl.exe 进程，不执行发行版 terminate/shutdown 或操作共享 daemon。超时/输出不完整不能通过，也不宣称所有远端进程生命周期已经核对。

微软[官方资料](W04-01-host-launch-docs.json)说明 Windows 发起、明确发行版/用户、--exec 不经默认 shell，以及 --cd 设置目录。此为最小实验通路，不预先锁定产品底座/协议。

## 本轮实际结果

| 层次 | 结果 | 依据 |
|---|---|---|
| Linux 接收端 | `--local-check` 退出 0、21 项通过；含三个版本查询、缺失 CLI、身份/路径/参数 | [结果](W04-01-host-launch-local.json)、[输出](W04-01-host-launch-local.txt) |
| Windows 启动 | 初始宿主尝试退出 1，PowerShell 启动前报 UtilBindVsockAnyPort socket failed 1 | [结果](W04-01-host-launch-host.json)、[错误](W04-01-host-launch-host-stderr.txt) |
| 静态 | Python AST 通过；环境中没有 pwsh，未做 PowerShell 解析器检查 | 当前源码检查 |
| 原生构建/浏览器 | 未执行；本片无产品代码/二进制变更 | 保留原产物 |

Linux 本地检查直接运行接收端，不走 Windows quoting；其 `host_verified=false`，不能代替 Windows 六场景。PowerShell 进程、Windows 参数解析与错误发行版/用户的实际结果都待可用环境验证。宿主失败后新增保留 stdout 和简化超时信息；最终 Linux 检查通过，没有重复同一受阻宿主矩阵。

## 运行与剩余范围

在用户此前前置通过的 WSL 终端，从仓库根目录运行：

```bash
python3 tests/wsl_host_probe.py
```

提供 Evidence directory 即可核对。`--local-check` 只供 Linux 接收端调试。默认模式只有真实 Windows 回执与六场景断言均成立才通过。

即使该探测通过，也仅覆盖当前发行版/用户的宿主启动与错误报告；第二个有效发行版/用户、存活 CLI 会话控制、连接中断/重连、在途工作及 Windows 生命周期仍待 W04.01/W01/W07 实测。重复启动不是存活会话接管，版本查询不是控制验收。

W02.06 的两个原生回归入口仍待用户运行，当前共享目录只有上一轮已结束的 EPERM 失败，不据此声称有可等待任务。新入口独立于其结果。

[diff](W04-01-host-launch-diff.patch)与[manifest](W04-01-host-launch-manifest.json)记录源码/进度变更和归档时哈希。旧证据和旧 bin/netcap 保持；未提交、对外通信、调整系统权限或停止共享后台。
