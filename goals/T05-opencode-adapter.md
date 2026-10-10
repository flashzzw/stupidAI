# T05 · 实现 OpenCode HTTP/SSE 会话 Adapter

工作目录：`/home/zzw/codex-netcap`。里程碑：M2。
技术前置：T03。

## 必须先读

从项目根目录读取 `goals/EXECUTION.md` 和 `goals/PROGRESS.md`，核对前置证据后执行本工作包。所有引用路径相对项目根目录，与本文件是否被复制成快照无关。
按需阅读 `MULTI_AGENT_DESIGN.md` 第 4、6、8.2、11.2 节 M2；T01 的 OpenCode schema/配置证据与设计 S2/S3/O3。

## 目标与范围

通过本机目标版本 OpenCode 的 HTTP/SSE 接入统一 Web，验证会话交互、断线核对、目录配置与进程所有权。

## 实施步骤

1. 使用 T01 记录的 schema 与版本生成或手写最小必要协议代码；实际 model/provider 参数由该版本定义，不复制旧文档摘要。
2. 管理自有 server 的生命周期和认证；仅在配置隔离已证明的条件下复用 server，外部 server 保持原所有者控制。
3. 完成会话创建、prompt、message/history/status、SSE、中止、permission/question 回复；把请求接受与真实结束分开。
4. 映射到 Application 的状态、PendingAction、证据化 Reconcile 和人工接管。SSE 断线后查询核对，不假设可以任意重放。
5. 检验同目录的权限和 MCP 配置作用范围；有独立配置需求时拆实例/进程，避免一个会话覆盖另一会话。
6. 注入连接中断、发送回执丢失、重复/迟到事件和原端先回复等场景，检查原生 message ID 与内部 Operation 的关联。
7. 用 Web 验证两轮、工具操作、审批/问题、停止、恢复、Unknown 和历史，记录实际资源开销。

## 完成条件

- [ ] 当前版本的真实 HTTP/SSE 流程与模拟供应商联调通过。
- [ ] SSE 断线和丢回执不会触发重复 prompt；证据不足保持 Unknown。
- [ ] 有效权限/问题回复准确，过期请求不重答；中止结果经过核对。
- [ ] 同目录会话配置互不污染，或明确采用隔离实例并说明代价。
- [ ] Web/Application 核心流程与 Codex 一致，原生能力差异明确显示。

本项技术前置是 T03，不依赖 T04 完成。任意独立 TUI 进程不因被发现就被当成可控会话。

## 收尾

按公共规则记录实际验证、未决事项和下一步。仅执行本工作包时，满足全部完成条件才完成当前原生 Goal；在 IMPLEMENT 总 Goal 内只更新本项进度并继续。
