# W04.01 · 宿主错误字节捕获修复实测通过

2026-10-02。用户运行 `python3 tests/wsl_host_probe.py`，目录 `/tmp/netcap-wsl-host-ool4jx3i`。真实 Windows PowerShell 5.1.19041.6157 → wsl.exe → Ubuntu-24.04/zzw 请求；代理直接核对落盘文件，不是在代理受限执行环境重跑。

[结果](W04-01-host-output-verified-result.json)host_verified=true、host_exit_code=0，六场景 25 项全部通过。正常/特殊目录、重复启动、CLI 缺失的身份与八个参数逐值匹配；三次固定 CLI 版本正确；不存在的发行版/用户分别退出 -1 并给出 WSL_E_DISTRO_NOT_FOUND / WSL_E_USER_NOT_FOUND。

代理从[Windows 原始回执](W04-01-host-output-verified-wire.json)独立解码 base64：Linux stdout/relay stderr 为 UTF-8，原生错误 stdout 为 UTF-16LE，逐字等于归一化报告；没有 NUL 或 replacement character。中文完整显示“不存在具有所提供名称的分发。”与“找不到用户。”。详见[直接复核](W04-01-host-output-verified-verification.json)及[请求](W04-01-host-output-verified-requests.json)。这补齐[前片](W04-01-host-output.md)的真实宿主字节捕获/错误解码验收，原乱码和宽松检查失败记录保留。

代理复核最初额外假设 host stderr 必须为空，该断言失败。读取[stderr 原文](W04-01-host-output-verified-host-progress.txt)后确认它仅包含两条 CLIXML progress：Preparing modules for first use，状态 Completed；没有错误流条目。修正的是复核假设，没有修改产品或丢弃 stderr。所有原始字节继续保留。

当前 Python/PowerShell/回归测试源码与上片修复 manifest 哈希一致，开发/旧二进制未变。本轮仅读取、归档及更新进度，没有代码或构建变化，没有重跑 Go/浏览器/本地测试；前片四项解码和 21 项 Linux 检查不冒称本轮重跑。

W04.01 仍 in_progress：验证范围是当前有效发行版/用户、启动参数及错误报告；第二有效发行版/用户、存活 CLI 会话控制、连接中断/重连及在途工作仍待验证。版本查询和 repeated launch 不能替代控制链路，也不提前解锁 W04.02 或锁定底座。整体 9/40，W02.06 收口保持，其余硬要求不变。

[diff](W04-01-host-output-verified-diff.patch)与[manifest](W04-01-host-output-verified-manifest.json)记录归档时点变更与哈希。旧证据保留；未操作共享后台、系统配置或真实供应商。
