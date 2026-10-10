# T10 · 实现原生与通用 Goal runner 及顺序清单

工作目录：`/home/zzw/codex-netcap`。里程碑：M4 Beta。
技术前置：T09。

## 必须先读

从项目根目录读取 `goals/EXECUTION.md` 和 `goals/PROGRESS.md`，核对前置证据后执行本工作包。所有引用路径相对项目根目录，与本文件是否被复制成快照无关。
按需阅读 `MULTI_AGENT_DESIGN.md` 第 7、11.2 节 M4、12；原生 Goal 参考 internal/guard/goals.go 与当前官方协议。

## 目标与范围

让文件清单在三类 agent 上逐项自动执行，使用 Codex native runner 与 Claude/OpenCode 通用 turn runner，交付串行自动化 Beta。

## 实施步骤

1. 在隔离 Codex 实例验证原生 Goal 创建/读取/暂停/完成、预算状态及轮次/队列清空；本项测试不能修改当前实施 Goal。
2. 实现 native runner：用短 objective 引用固定 Task 快照，绑定原生 Goal 并唯一负责推进；暂停/恢复沿用原目标和用量记录，不重复覆盖 objective。失败重探测前核对在途执行，不叠加通用继续循环。
3. 实现通用 turn runner：固定结构化报告或受约束报告产物的契约，关联 Attempt；每轮决定 continue/blocked/complete，完成声明仍经过 T09 验收。
4. 为通用 runner 配置可观察的轮数/无进展/时间约束。仅使用用户显式设置的 token budget，区分可测用量、估算和未知。
5. 导入顺序文件清单，校验 ID/快照/cwd/route；当前任务固定，重排只影响未开始项，删除或版本变更不改变在途执行。
6. 处理 blocked、暂停、预算/配额限制、Unknown、报告无效与验收失败；它们不解锁下一项。重启后先恢复原 Attempt/native Goal。
7. 三类各执行有可核验产物的 Task，再执行跨 agent 的两项串行清单；Web 验证顺序、结果与停止原因。

## 完成条件

- [ ] Codex 原生 Goal 契约在自动派发前通过；同一 Attempt 只有一种 runner。
- [ ] 三类 agent 都完成真实 Task，清单下一项仅在上项验收成功后开始。
- [ ] native complete 仍等待相关轮次/队列收敛；通用报告不是完成的唯一依据。
- [ ] 阻塞/暂停/限制/未知不被自动绕过，防空转约束可验证。
- [ ] 重启接续保留身份与进度，原生自行续跑期间持续占容量。
- [ ] Beta 的队列 UI、协议/状态测试和适用构建通过；不足以证明的真实供应商行为单列。

## 收尾

按公共规则记录实际验证、未决事项和下一步。仅执行本工作包时，满足全部完成条件才完成当前原生 Goal；在 IMPLEMENT 总 Goal 内只更新本项进度并继续。
