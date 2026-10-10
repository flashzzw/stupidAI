# W02.06 · 用户 WSL 终端原生 MCP 最小对照通过

2026-10-02。用户在独立 WSL 终端执行 `python3 tests/claude_mcp_stdio_diagnosis.py --output /tmp/netcap-stdio-comparison.jso`；`.jso` 是实际输出文件名，不影响 JSON 内容或验收。代理随后直接读取该文件及原始隔离目录，未在代理执行环境重跑连接。

固定 Claude 2.1.280：原生与 Node 对照退出码均为 0，四项检查为真。原生 trace 实际包含 initialize、tools/list；inventory 中 identity 为 connected，来源 dynamic，工具清单为 identity。selection_matches 为真、verification_error 为空、model_results 为 0，未出现 initialize 前 EOF。

[结果](W02-06-stdio-user-pass.json)、[原生 trace](W02-06-stdio-user-pass-native-trace.jsonl)、[Node trace](W02-06-stdio-user-pass-node-trace.jsonl)、[snapshot](W02-06-stdio-user-pass-snapshot.json)已归档。代理核对输出与隔离目录 comparison.json 一致，两侧 trace 和原生 snapshot 与各自原始文件一致，stderr.log 均为空。原始运行来源是用户终端，文件检查来源是代理；当前源码哈希仅标记归档时版本，不证明用户启动瞬间的全部文件内容。

此结果关闭“该用户终端中原生 MCP 最小初始化是否成功”的疑问，不表示当前代理执行限制已解除，也不覆盖 tools/call、OAuth、远程认证、完整权限冲突、原 ID 恢复或 supervisor/worker 控制。此前失败证据原样保留；W02.06 保持 in_progress，8/40 done 不变。

下一项使用已有 `python3 tests/claude_mcp_sources_probe.py` 核对 16 种来源/策略启动场景，目标补验此前失败的连接清单检查。该脚本使用隔离配置和本地 stdio 服务，不发送模型输入、不调用真实供应商、不修改共享后台；本轮尚未执行。它不验证工具实际权限执行。用户只需提供运行输出中的 Evidence directory，代理即可从共享文件系统读取 result.json 并继续核对。

本轮仅归档与更新进度，没有修改产品代码、构建或执行 Go/浏览器测试。原始文件、源码及进度归档时哈希见 [manifest](W02-06-stdio-user-pass-manifest.json)，进度变更见 [diff](W02-06-stdio-user-pass-diff.patch)。
