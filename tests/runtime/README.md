# 三类 Runtime 最小协议实验

运行真实安装的 CLI/官方 SDK，供应商响应由本机 HTTP fixture 产生。不调用真实模型；不使用用户凭据、共享后台、hooks 或项目。不要把这些检查称为真实供应商验收。

```bash
make runtime-deps
make runtime-contracts
# 单项或显式二进制路径：
python3 tests/runtime/run.py --agent codex --codex /path/to/codex --codex-server /path/to/server-codex
python3 tests/runtime/run.py --agent claude --claude /path/to/claude
python3 tests/runtime/run.py --agent opencode --opencode /path/to/opencode
```

运行前校验 CLI 版本；SDK 由本目录 package-lock 固定。不修改系统安装。当前组合：Codex CLI 0.157.1 / 后台 0.158.0、Claude Code 2.1.280 / SDK 0.3.280、OpenCode 1.18.32、Node 24.13.0。升级时先更新测试期望，再重跑受影响契约，不把较新版本自动当成兼容。

每类在 A/B 两个本地端点上分别创建持久会话；完成两轮后重建客户端，读取历史并继续第三轮。检查实际请求中的模型、测试凭据、路由标记及恢复后的历史上下文。Codex 使用真实 CLI proxy/WebSocket；Claude 使用 SDK async input/result/磁盘历史；OpenCode 使用目标版本 `/doc`、`prompt_async` 的 204 接受回执、历史终态与 SSE，并在第二轮结束后重启自有 server。

每次输出 `/tmp/netcap-runtime-*/` 证据目录，含结果、请求、原生历史及进程日志。其中只有自动生成的测试消息和 `fixture-A/B` 凭据。所有子进程使用独立 HOME/profile/XDG/cwd，环境为白名单，不继承实际认证、代理或通知地址。清理仅作用于本次创建的进程。

## 配置策略 revision：m0-1

| 运行时 | 已执行实验的配置 | 普通工作会话策略 | 自动探测策略与准入限制 |
|---|---|---|---|
| Codex | 独立 CODEX_HOME；显式 provider/model；env_key 指向测试变量；on-request/read-only；关闭 fixture 的 apps/plugins/shell snapshot | 绑定实例已有 profile、权限和配置指纹；保留其规则 | 同 route 的独立 profile/cwd，最小工具；T07 验证日期工具和集成隔离前不启用自动探测 |
| Claude | 显式 executable、claude_code preset、`settingSources: [user,project,local]`、env、tools=[]、default 权限、persistSession=true | 同样显式声明所有来源；保留项目 CLAUDE.md 和所需 hooks；由实例策略决定唯一通知发送方 | 保持认证/model，使用独立 cwd/profile 和显式 sources；SDK 0.3.280 类型声明说明省略 sources 会加载全部来源，`[]` 关闭文件设置、project 控制 CLAUDE.md；实际规则/hook/MCP/托管设置边界待 T04 fixture，未验证前不得自动探测 |
| OpenCode | 独立 HOME/XDG 与 cwd；OPENCODE_CONFIG_CONTENT；每 route 自有 server；显式 provider/model/apiKey；--pure；禁止自动更新/模型目录抓取 | 相同不可变配置才能复用 server；每次 API 显式 directory | 独立目录/配置实例；目录级配置、规则、MCP 和权限合并待 T05 fixture，未验证前不得自动探测 |

CLI 可能存在不受环境/profile 控制的机器级策略；这里只证明当前隔离实验的路由与会话路径，不能据此承诺任意用户机器的全部配置隔离。普通工作会话不通过禁用全部项目规则解决通知重复。

## 能力与后续归属

| 能力 | Codex | Claude | OpenCode | 补验 |
|---|---|---|---|---|
| 创建、两轮、稳定原生 ID | supported | supported | supported | 后续 Adapter 回归 |
| 结束后历史/继续 | supported，重建 proxy | supported，重建 Node/CLI | supported，重启 server | 不等同中途恢复 |
| 双端点、模型与测试认证实际到达 | supported | supported | supported | T07 配置漂移/健康隔离 |
| 流式事件 | unverified（本项以历史确认终态） | supported | supported | T02/T06 统一投影 |
| 原生审批/问题/拒绝/过期 | unverified | unverified | unverified | T03/T04/T05，T06 统一 |
| 中途崩溃、丢回执、停止及子任务归属 | unverified | unverified | unverified | T03–T06 |
| 项目规则、hooks 与探测隔离 | unverified | unverified | unverified | T04/T05/T06，T07 准入 |
| native Goal 生命周期与队列清空 | unverified | unsupported（通用 runner） | unsupported（通用 runner） | T10 |
| 任意独立 CLI 热接管 | unverified | unsupported | unsupported | 仅观察，不授予控制权 |

`supported` 仅表示这组版本上的已执行契约。T00 旧 Goal 测试不能自动升级 T10 新 runner 能力。

协议依据：2026-09-28 实际取回的 [OpenAI App Server](https://learn.chatgpt.com/docs/app-server) 及本机 generate-json-schema；Context7 的官方 [Claude streaming input](https://code.claude.com/docs/en/agent-sdk/streaming-vs-single-mode)、[会话](https://code.claude.com/docs/en/agent-sdk/sessions)、[TypeScript](https://code.claude.com/docs/en/agent-sdk/typescript) 索引，并核对已安装 SDK 类型；[OpenCode server](https://opencode.ai/docs/server/)、官方仓库文档索引及目标运行实例 `/doc`。没有复制参考项目实现。
