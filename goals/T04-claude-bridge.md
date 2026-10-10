# T04 · 实现 Claude SDK bridge 与受控会话

工作目录：`/home/zzw/codex-netcap`。里程碑：M2。
技术前置：T03。

## 必须先读

从项目根目录读取 `goals/EXECUTION.md` 和 `goals/PROGRESS.md`，核对前置证据后执行本工作包。所有引用路径相对项目根目录，与本文件是否被复制成快照无关。
按需阅读 `MULTI_AGENT_DESIGN.md` 第 4.3、6、10、11.2 节 M2 和第 14 节 F4；按需查 T3 ClaudeAdapter 与 Cindy manager。

## 目标与范围

通过固定版本官方 Claude Agent SDK 的薄 bridge，为现有 Web 提供完整的 Claude 会话路径，验证配置继承、交互和故障恢复。

## 实施步骤

1. 使用 T01 的版本/策略记录，定义 Go↔bridge 版本化消息契约与握手；保持调度和路由选择在 Go，bridge 仅管理 SDK query、输入、事件与控制。
2. 显式构造 executable、cwd、systemPrompt、settingSources、env 和权限策略；并发 query 不通过改全局 process.env 切账号。
3. 完成 streaming input、原生 session ID 记录、持久历史恢复、中止、待审批/问题回调和自有进程回收。
4. 映射到共用 Runtime、PendingAction、证据化 Reconcile 与转人工流程；无法恢复的原生请求明确过期或受限，不能伪造成功。
5. 用测试 CLAUDE.md、hook 与 fake sender 分别验证普通工作会话和探测配置。保留所需项目规则，排除探测无关集成并确认实际请求 route。
6. 注入 bridge 退出、CLI 异常、连接失效和中止回执先于子任务结束等场景；验证跨实例事件与认证隔离。
7. 通过现有 Web 演示两轮、工具交互、继续、停止和 Unknown；记录 bridge/CLI 资源开销。

## 完成条件

- [ ] 固定版本依赖、typecheck、build 和生命周期测试通过，可重复启动 bridge。
- [ ] 两轮交互、原生历史恢复、有效审批/问题和中止路径通过。
- [ ] 普通会话配置继承与探测隔离有 fixture 证据，hook 与 SDK 不重复投递测试通知。
- [ ] bridge/CLI 异常不遗留被误当空闲的执行；不能确认时转 Unknown 并可人工处置。
- [ ] Claude 接入通过既有 Web/Application 契约，Codex 相关回归保持成立。

T05 与本项同样只依赖 T03；本项无需等待 OpenCode 实现，也不能因此改写其他 agent 的原生语义。

## 收尾

按公共规则记录实际验证、未决事项和下一步。仅执行本工作包时，满足全部完成条件才完成当前原生 Goal；在 IMPLEMENT 总 Goal 内只更新本项进度并继续。
