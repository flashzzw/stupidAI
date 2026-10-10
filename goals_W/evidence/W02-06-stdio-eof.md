# W02.06 · stdio 提前 EOF 的输入通道对照

日期：2026-10-01。接续[来源诊断](W02-06-source-diagnostics.md)。本片只缩小故障定位范围，没有修复生产代码，W02.06 保持 in_progress。

## 原始观察与可重跑入口

固定 Claude 2.1.280、Agent SDK 0.3.280、Node 24.13.0。最初分开运行的[原生观察](W02-06-stdio-eof-native-before.json)与 [Node 对照](W02-06-stdio-eof-node-before.json)保留原始结果：同一个 Python MCP 服务，原生路径在 initialize 前收到 EOF；普通 Node `child_process.spawn` 能正确交换 initialize。

本片将相同服务整理成[最小复现脚本](../../tests/claude_mcp_stdio_diagnosis.py)，复用原有固定版本检查、隔离环境和[原生来源探测](../../tests/claude_mcp_sources_probe.mjs)，没有发送模型 prompt、监听服务或修改 CLI/SDK。

```bash
python3 tests/claude_mcp_stdio_diagnosis.py --output .dev/w0206-stdio-diagnosis/comparison.json
```

实际执行后退出 **1**，完整结果见 [comparison](W02-06-stdio-eof-comparison.json)及 [run](W02-06-stdio-eof-run.txt)。原生探测子进程退出 0 仅表示成功取得清单，不表示连接成功；外层仍保留 `native_connected` 断言。

| 检查 | 实际结果 | 可支持的结论 |
|---|---|---|
| 普通 Node 启动同一 Python 服务并交换 initialize | 通过 | 该服务能处理请求并回复，本环境的一般子进程输入通道可用 |
| 两路径 fd 0 属性 | 均 `blocking=true`、`mode=49152`（Unix socket） | 排除“原生给了非阻塞 stdin，Python 将暂无数据误判 EOF”的假设 |
| 未输入模型任务 | `model_results=0` | 本实验不覆盖模型/工具调用权限，也不需要真实供应商 |
| 原生 MCP 连接 | **失败**，`source=dynamic`、`status=failed`、空工具清单 | 服务收到 EOF 且未收到 initialize，`selection_matches=false`，整体 `expectations_met=false` |

两个进程的 fd 0 都是阻塞 Unix socket，但只有原生路径在请求前结束输入。故障范围缩小到原生启动/写入路径及其环境交互；现有证据不能确定具体关闭方或根因，不能据此断言 sandbox 或某个 CLI bug。当前没有 strace/gdb 证据，没有替换原生传输或放宽权限。

## 隔离、版本与剩余范围

原始目录：`/tmp/netcap-stdio-diagnosis-l05pzqvh`、`/tmp/netcap-stdio-node-control-0excvg0t`；本次组合重跑：`/tmp/netcap-stdio-comparison-0jrd2g8z`。临时 Git 项目、配置及服务均由探测创建；没有使用用户配置、调用真实供应商、停止共享后台、提交或发布。[进程检查](W02-06-stdio-eof-cleanup.json)未发现该重跑目录的存活进程。Python 语法检查通过，143 个既有 W02.06 证据文件哈希未改变。

本片未重跑 Go 构建/测试、浏览器、完整来源矩阵或远程 OAuth；生产未修改，不把历史验证冒充本次结果。下一步需有新的原生启动/写入端观测才能继续收敛根因；修复后仍须重跑连接、实际工具权限和来源冲突。SSE/OAuth/动态认证及其他既有硬要求全部保留。

归档版本与源码/证据哈希见 [manifest](W02-06-stdio-eof-manifest.json)，哈希只表示本次归档时点。
