# W04.01 · 真实 Windows 启动与 WSL 错误编码修复

2026-10-02。用户运行修正文件名后的 `python3 tests/wsl_host_probe.py`，输出 `/tmp/netcap-wsl-host-5fdot5h8`。本轮代理读取并复核原始报告、Windows stdout 和请求文件，不声称在代理受限环境中启动了 Windows。

## 实际宿主成功与缺陷

[原始结果](W04-01-host-output-original.json)确认 Win32NT / PowerShell 5.1.19041.6157 进程真正发起 wsl.exe 请求，host_exit_code=0。当前 Ubuntu-24.04 / zzw / uid 1000 的正常目录、中文/空格/引号目录、重复启动、CLI 缺失四个接收端结果与请求匹配。八个参数逐值保留，包括空值、Unicode、引号、反斜线及 shell 字面字符；三次隔离 Claude --version 返回固定 2.1.280。

两个不存在的目标均原生退出 -1，但输出包含 NUL 和 Unicode replacement character。wsl.exe 的原生错误输出是 UTF-16LE，而原探测对 stdout/stderr 固定使用 UTF-8 StreamReader。原检查只要求非零退出且文本非空，因此原 expectations_met=true 不能证明错误可读、可定位。[更严格复核红灯](W04-01-host-output-red.txt)明确失败；[复核投影](W04-01-host-output-verification.json)保留逐场景事实。没有改写原始通过字段冒充新版验收。

## 修复及验证

PowerShell 使用 BaseStream.CopyToAsync 保存原始字节，按 base64 传回，避免提前有损解码。Python 根据 BOM 或可识别的 WSL UTF-16LE 错误码字节选择解码；其他输出按 UTF-8 严格解码。不能识别或含 NUL/替换字符即失败，保留原始 base64。缺失发行版/用户检查分别要求 WSL_E_DISTRO_NOT_FOUND / WSL_E_USER_NOT_FOUND，不把其他启动失败当作指定负例成功。

[解码回归](W04-01-host-output-tests.txt)4 项通过，覆盖正常 UTF-8/空输出、带/不带 BOM 的 UTF-16 中文原生错误、损坏字节拒绝、非目标错误与成功退出不能通过负例。最终 [Linux 接收端](W04-01-host-output-local.json)21 项通过，host_verified=false；Python AST 通过。本次没有执行新版 PowerShell，所以新字节捕获、真实中文解码和强化后的 25 项宿主检查仍待重跑。既有字符串已经有损，不能从旧记录恢复出原始中文后声称修复验证通过。

## 后续

在用户可用 WSL 终端重跑：

```bash
python3 tests/wsl_host_probe.py
```

无需重跑已完成的 MCP 验收。取得新 Evidence directory 后核对原始字节、两种编码、错误码与参数身份。W04.01 改为 in_progress：实际宿主启动已成立，仍欠新版错误报告、第二个有效发行版/用户、存活会话控制/断连重连。W04 包仍 in_progress；不将版本查询当作完整控制链路，不提前锁定底座。

同期 W02.06 已由[独立收口](W02-06-acceptance-complete.md)完成，累计 9/40。全部六入口/协作/插件/Windows 生命周期等硬要求保留。此片只改探测代码与进度，产品源码、构建、旧 bin/netcap 不变；未运行 Go/浏览器检查，未修改系统/用户配置或操作共享后台。变更见 [diff](W04-01-host-output-diff.patch)，归档时哈希见 [manifest](W04-01-host-output-manifest.json)。
