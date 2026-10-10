# 原生日期调用回放

这些文件摘自 T07 已执行的真实 CLI + 本地模拟供应商实验，不是本轮新执行的 CLI 验收。原始目录为 `/tmp/netcap-console-q5pife31`，实验入口为 `tests/route_probe_evidence.py`，原始日志为 `.dev/logs/T07-native-date-marker.log`。

| 原文件 | SHA256 |
|---|---|
| `codex-date-native.json` | `c1c39170bfdbce93b1d573808b8bc60660f7c3ed46e611d59a6328381d4b52fa` |
| `codex-date-rollout.jsonl` | `f1c8e5bcf2728b8221cba4fd4815486983f53f4a7792be5bba4011bc55591c26` |
| `claude-date-native.json` | `6fe57958af6cb5a516421efad5dd92d7ec66709fe7abb8bae3749fb3e2552c6e` |
| `opencode-date-native.json` | `bfb2fcca2dd4849465564d269494a6b090f90bede3051cb896b6044dab6ddda6` |

版本沿用该实验：Codex 0.158.0、Claude Code 2.1.280 / SDK 0.3.280、OpenCode 1.18.32。Codex thread 和 Claude init 原始记录直接包含各自 CLI 版本；其余版本由该实验的固定运行配置记录。

摘取方式：

- Codex thread 保留身份、状态、路径、模型、版本和完整 turn/items；rollout 仅保留 session_meta 的身份/路径/版本以及原始 custom_tool_call/output。删除系统提示及无关事件；路径改为 `@PROFILE@`。
- Claude 保留 initialized、input、user、assistant、result 顺序和原生关联 ID。删除系统 init、命令生命周期、用量等不参与工具投影的字段；消息保留 role/content，工具结果保留原形状。
- OpenCode 保留全部 message/part、关联 ID、终态、工具输入/输出/metadata；删除无关路径、费用和 tokens 字段。
- 工具命令、原生输出、最终响应和关联 ID 未改写。测试加载时把三份记录的 UTC 输出/响应替换为测试当前时间，并把 Codex 路径定位到自有临时目录；epoch 元数据仍为原值。

`TestCapturedNativeDate*` 经现有 Codex Observe、Claude journal 投影和 OpenCode Observe 验证。OpenCode 使用内存 `http.RoundTripper`，不会打开监听或发送网络请求；Codex 的 control responses 和其他执行范围空列表均为 fixture。删除工具结果后的负例必须拒绝，仅有最终时间不能通过。

这些检查证明现有投影可读取上述历史工具形状；不证明新 probe 隔离、路由凭据、HTTP/SSE、peer 握手、后台退出或恢复，也不用于提升生产能力标记。

```sh
GOCACHE=/tmp/netcap-go-cache go test -race -count=1 ./internal/runtime -run TestCapturedNativeDate
```
