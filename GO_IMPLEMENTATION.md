# Go 后端实施记录

已完成用户本轮要求：自动临时日期探测 → 原生 Goal 文件清单 → 持久天气轮询。Web 延后，原 Python 入口保留。

## 已交付行为

- 新状态 `run` 自动探测；真实执行 `date -u +%FT%TZ` 并原样返回 UTC 才通过。临时 CLI 使用 `--ephemeral`，取消和超时清理自有进程组与输出文件。
- Goal 文件固定快照、独立工作目录和新会话；通过原生 `thread/goal/set` / `get` 执行和判断完成，顺序推进。调用失败暂停后重新探测，再恢复同一 Goal。
- 无 Goal 或清单完成后，以可 resume 会话执行原 `weather.py` 的城市轮询。脚本成功才推进城市，默认间隔 10 秒；不用无意义的 OK。
- 人工新会话交互或接管天气 ID 后持久暂停。显式恢复前重新探测；已交给人工的天气 ID 不再追加自动请求。
- 所有自动会话无审批、无沙箱。日期 CLI 使用 bypass 参数，Goal/天气 RPC 使用等价权限。供应商请求全部由 Codex CLI 发出。
- high demand / rate limit 不增加 Go 层退避；其他故障指数退避并加抖动。Codex 内部重试未改写。
- 本地 Unix socket、SQLite 原子状态与单实例锁保留。v2 明确拒绝旧 v1 状态，使用新目录，不删除旧数据。

## 验证结果（2026-09-28）

- `make build`：通过，`bin/netcap` 已更新。
- `make check`：通过，包含全部 Go race 测试和 `go vet ./...`。
- 真实 CLI + 本地模拟供应商集成：通过。共 23 次本地请求，证据目录 `/tmp/netcap-go-smoke-f7tfhpeu`。
  - 真实执行日期命令、天气 stub，持久会话城市顺序正确。
  - resume 天气 ID 接管、新人工线程、纯天气在途暂停、显式恢复与重启恢复均通过。
  - 两项 Goal 在不同 cwd / 新会话实际写出验收文件，并调用原生 Goal 完成工具；依次完成后开始天气。
  - 从真实 Codex 请求元数据确认全部 `danger-full-access` / `never`；日期探测 ID 没有保存 rollout。
- 真实共享后台只读 `doctor`：通过，proxy ready，CLI 0.157.1 / app-server 0.158.0；未向真实供应商发送自动化请求。
- Python 语法、JSON 示例、本地文档链接、原脚本 31 个城市元数据：通过。

联调修复了新线程首次消息前 `thread/turns/list` 返回 `is not materialized yet` 导致反复重连的问题。已增加失败复现与回归测试，仅确认新线程空闲时允许空历史；未知在途操作仍不重发。其他测试覆盖日期伪响应/错误、探测生命周期、Goal 阻塞与恢复、队列回执丢失、删除失败、人工混入轮次和先持久化后发送。

## 使用与边界

启动、Goal 配置和接管命令见 [GO_BACKEND.md](GO_BACKEND.md)。人工检测限同一个共享后台，不能看到未提交的输入；暂停 Goal 不强杀当前轮次。真实供应商空闲阈值、持续保活效果和压力错误尚未实测。

协议依据为 OpenAI Docs 官方页面、本机 schema 和 CLI 联调。上版源码备份 `/tmp/netcap-before-native-goal-20260928.tar`，不含用户状态或凭据。当前工作区没有 Git 仓库，差异核对使用备份与当前文件。
