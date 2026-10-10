# 需求覆盖与最终验收

需求来源：[需求分析 §5/11/12](../docs/requirements-analysis.md)；产品验收来源：[产品设计 §15](../docs/product-design.md)。本表将 27 个需求条目（R01–R26 加 R02b）和 16 个产品场景落到子任务，映射只表示责任，不表示验收通过。

## 使用方式

实施结果与状态只在 [PROGRESS](PROGRESS.md)维护。到 W07.06 时，为下列每项建立最终证据记录：对应产物/版本、场景、实际结果（通过/失败/未执行）、限制及记录链接；责任子任务提供证据，不以旧工作包状态代替复核。

单项样本只覆盖其明确条件。六入口、八组合、四配置及继承/隐含资源需展开逐项矩阵；W01 的能力评估结果不能替代 P02。需求范围发生调整时记录用户决定并更新上游，保留原失败证据。

## 需求责任表

| 条目 | 需求 | 负责子任务 | 最终核对重点 |
|---|---|---|---|
| R01 | 环境入口 | [W04.01](W04-platform-plugin.md#w04-01)、[W07.01](W07-desktop-delivery.md#w07-01) | 真实 Windows 发行版/用户/目录/CLI 选择及不可用诊断 |
| R02 | 原生工具 | [W01.02](W01-session-continuity.md#w01-02)、[W01.03](W01-session-continuity.md#w01-03)、[W01.04](W01-session-continuity.md#w01-04)、[W02.01](W02-execution-capabilities.md#w02-01) | 所选 CLI、配置及执行位置可核对，不静默替换 |
| R02b | 已有任务接续 | [W01.03](W01-session-continuity.md#w01-03)、[W01.04](W01-session-continuity.md#w01-04)、[W01.06](W01-session-continuity.md#w01-06) | 六入口分别验；独立前台、预托管、观察和操作分开 |
| R03 | 目标与计划 | [W05.01](W05-goal-verification.md#w05-01)、[W06.03](W06-lead-collaboration.md#w06-03) | 目标/约束/计划持久且有版本，外部变更不静默改写 |
| R04 | 持续推进 | [W06.02](W06-lead-collaboration.md#w06-02)、[W06.03](W06-lead-collaboration.md#w06-03)、[W06.06](W06-lead-collaboration.md#w06-06) | 有可执行步骤时持续，阻塞/约束/无进展时有界停止 |
| R05 | 角色协作 | [W06.03](W06-lead-collaboration.md#w06-03)、[W06.04](W06-lead-collaboration.md#w06-04) | 真实发现驱动分工，独立审查，失败不解锁依赖 |
| R06 | 中断恢复 | [W01.05](W01-session-continuity.md#w01-05)、[W05.04](W05-goal-verification.md#w05-04)、[W07.02](W07-desktop-delivery.md#w07-02) | 原 Attempt/执行连续，断连/崩溃/丢回执不盲重做 |
| R07 | 人工控制 | [W01.06](W01-session-continuity.md#w01-06)、[W05.04](W05-goal-verification.md#w05-04) | 暂停调度与中止执行分离，接手后自动操作让路 |
| R08 | 状态与提醒 | [W06.02](W06-lead-collaboration.md#w06-02)、[W07.03](W07-desktop-delivery.md#w07-03) | 待处理、运行与交付可辨认，通知合并去重 |
| R09 | 验证与交付 | [W05.02](W05-goal-verification.md#w05-02)、[W05.03](W05-goal-verification.md#w05-03)、[W05.05](W05-goal-verification.md#w05-05) | 实际改动和有效 Gate 决定交付，失败/未执行单列 |
| R10 | 运行约束 | [W06.01](W06-lead-collaboration.md#w06-01)、[W06.02](W06-lead-collaboration.md#w06-02) | 约束可执行、未知用量可见，无限重试受控 |
| R11 | 插件扩展 | [W04.03](W04-platform-plugin.md#w04-03)、[W04.04](W04-platform-plugin.md#w04-04) | 一个真实 netcap 功能贯通平台与插件 |
| R12 | 桌面体验 | [W07.01](W07-desktop-delivery.md#w07-01)、[W07.02](W07-desktop-delivery.md#w07-02)、[W07.04](W07-desktop-delivery.md#w07-04) | Windows 日常入口、返回原工作与可用性 |
| R13 | 插件管理 | [W04.04](W04-platform-plugin.md#w04-04) | 安装/启停/升级失败/卸载保留数据并解释影响 |
| R14 | 角色与 CLI 选择 | [W06.04](W06-lead-collaboration.md#w06-04) | 四角色各用两类 CLI，八组合行为实际成立 |
| R15 | 专家能力组合 | [W02.05](W02-execution-capabilities.md#w02-05)、[W02.07](W02-execution-capabilities.md#w02-07)、[W06.01](W06-lead-collaboration.md#w06-01)、[W06.04](W06-lead-collaboration.md#w06-04) | Profile/Skill/Gate 可复用且切换 CLI 后仍生效 |
| R16 | 能力与兼容性 | [W01.02](W01-session-continuity.md#w01-02)、[W01.06](W01-session-continuity.md#w01-06)、[W02.01](W02-execution-capabilities.md#w02-01) | 版本/连接/来源及读写能力分别显示，拒绝无依据提升 |
| R17 | 输入控制权 | [W01.06](W01-session-continuity.md#w01-06)、[W04.03](W04-platform-plugin.md#w04-03)、[W06.02](W06-lead-collaboration.md#w06-02) | 终端、平台、Lead 与插件不争抢和重复推进 |
| R18 | 工作区与产物保留 | [W05.04](W05-goal-verification.md#w05-04)、[W06.05](W06-lead-collaboration.md#w06-05)、[W07.05](W07-desktop-delivery.md#w07-05) | 冲突隔离/串行，未知占用保留，停止不删成果 |
| R19 | 验证证据有效性 | [W05.02](W05-goal-verification.md#w05-02)、[W05.03](W05-goal-verification.md#w05-03)、[W06.05](W06-lead-collaboration.md#w06-05) | Gate 绑定产物版本，修复/集成后重新验证 |
| R20 | 插件与平台协作 | [W04.03](W04-platform-plugin.md#w04-03)、[W04.04](W04-platform-plugin.md#w04-04) | 唯一身份/动作/账本责任，禁用/重启不双重调度 |
| R21 | 多执行配置并发 | [W02.01](W02-execution-capabilities.md#w02-01)、[W02.09](W02-execution-capabilities.md#w02-09) | Codex A/B 与 Claude A/B 同时执行和恢复不串用 |
| R22 | 每次执行选配能力 | [W02.04](W02-execution-capabilities.md#w02-04)、[W02.06](W02-execution-capabilities.md#w02-06)、[W02.07](W02-execution-capabilities.md#w02-07)、[W02.08](W02-execution-capabilities.md#w02-08)、[W02.09](W02-execution-capabilities.md#w02-09) | 新建/派发/继续有选择，继承/隐含/不可覆盖和实际排除准确 |
| R23 | 共享与专属能力管理 | [W02.05](W02-execution-capabilities.md#w02-05)、[W02.06](W02-execution-capabilities.md#w02-06)、[W02.07](W02-execution-capabilities.md#w02-07)、[W02.08](W02-execution-capabilities.md#w02-08) | 资产来源/版本/兼容性可查，共享定义不串用认证状态 |
| R24 | 两种插件分离 | [W02.08](W02-execution-capabilities.md#w02-08)、[W04.04](W04-platform-plugin.md#w04-04) | 宿主/组件/位置/生效范围分开，启停互不暗中联动 |
| R25 | 有效配置可追溯 | [W02.02](W02-execution-capabilities.md#w02-02)、[W02.04](W02-execution-capabilities.md#w02-04)、[W02.09](W02-execution-capabilities.md#w02-09)、[W06.04](W06-lead-collaboration.md#w06-04) | 前后快照/实际来源/权限/漂移及角色 Gate 关联 |
| R26 | Harness 运行纪律 | [W06.02](W06-lead-collaboration.md#w06-02)、[W06.06](W06-lead-collaboration.md#w06-06) | 按需上下文、有界恢复、唯一调度、独立 Gate 和语义交接 |

## 产品场景表

| 场景 | 用户操作 | 证据来源子任务 | 通过条件的关键边界 |
|---|---|---|---|
| P01 | Windows 选择 WSL 项目 | [W04.01](W04-platform-plugin.md#w04-01)、[W07.01](W07-desktop-delivery.md#w07-01) | 宿主与真实执行环境一致 |
| P02 | 双 CLI 六种入口 | [W01.03](W01-session-continuity.md#w01-03)、[W01.04](W01-session-continuity.md#w01-04)、[W01.05](W01-session-continuity.md#w01-05)、[W01.06](W01-session-continuity.md#w01-06) | 严格实时接入未通过则保持未满足 |
| P03 | 四角色 × 两类 CLI | [W06.04](W06-lead-collaboration.md#w06-04) | 八组合、混合/同 CLI 团队及方法约束 |
| P04 | 跨模块目标与动态分工 | [W05.01](W05-goal-verification.md#w05-01)、[W06.03](W06-lead-collaboration.md#w06-03) | 真实发现和依赖，非预写协作剧本 |
| P05 | Reviewer 打回与修复 | [W05.02](W05-goal-verification.md#w05-02)、[W05.03](W05-goal-verification.md#w05-03)、[W06.03](W06-lead-collaboration.md#w06-03)、[W06.05](W06-lead-collaboration.md#w06-05) | 失败保留，新产物和集成版本重新验证 |
| P06 | 断连/重启/丢回执 | [W01.05](W01-session-continuity.md#w01-05)、[W05.04](W05-goal-verification.md#w05-04)、[W07.02](W07-desktop-delivery.md#w07-02) | 原执行、在途工具、产物和控制权连续 |
| P07 | 暂停/接手/取消/回终端 | [W01.06](W01-session-continuity.md#w01-06)、[W05.04](W05-goal-verification.md#w05-04) | 后续调度和当前执行分离，取消不自动复活 |
| P08 | 平台插件生命周期 | [W04.03](W04-platform-plugin.md#w04-03)、[W04.04](W04-platform-plugin.md#w04-04) | 真实功能、失败可见、无重复与数据丢失 |
| P09 | 约束与通知 | [W06.02](W06-lead-collaboration.md#w06-02)、[W07.03](W07-desktop-delivery.md#w07-03) | 未知用量真实，达到约束有原因，通知不重复 |
| P10 | 关闭后回到工作并审查 | [W05.05](W05-goal-verification.md#w05-05)、[W07.02](W07-desktop-delivery.md#w07-02)、[W07.04](W07-desktop-delivery.md#w07-04) | 原目标、有效证据、窄窗与键盘操作 |
| P11 | 四配置并发和恢复 | [W02.01](W02-execution-capabilities.md#w02-01)、[W02.09](W02-execution-capabilities.md#w02-09) | 路由、目录、同名/同 ID、认证及端点不串用 |
| P12 | 同角色两次不同能力 | [W02.04](W02-execution-capabilities.md#w02-04)、[W02.06](W02-execution-capabilities.md#w02-06)、[W02.07](W02-execution-capabilities.md#w02-07)、[W02.08](W02-execution-capabilities.md#w02-08)、[W02.09](W02-execution-capabilities.md#w02-09)、[W06.04](W06-lead-collaboration.md#w06-04) | 继承/隐含来源和排除有实际依据 |
| P13 | 跨 CLI/供应商共享能力 | [W02.05](W02-execution-capabilities.md#w02-05)、[W02.06](W02-execution-capabilities.md#w02-06)、[W02.07](W02-execution-capabilities.md#w02-07) | 实际脚本/工具兼容，MCP 认证/状态独立 |
| P14 | 分别启停两类插件 | [W02.08](W02-execution-capabilities.md#w02-08)、[W04.04](W04-platform-plugin.md#w04-04) | 所属宿主和关联组件正确，原会话保留 |
| P15 | 更新/热变更/旧配置恢复 | [W02.04](W02-execution-capabilities.md#w02-04)、[W02.05](W02-execution-capabilities.md#w02-05)、[W02.09](W02-execution-capabilities.md#w02-09)、[W05.03](W05-goal-verification.md#w05-03) | 版本、漂移和生效边界；不静默替换原执行 |
| P16 | 大资产库/压缩/失败/交接 | [W02.05](W02-execution-capabilities.md#w02-05)、[W06.02](W06-lead-collaboration.md#w06-02)、[W06.06](W06-lead-collaboration.md#w06-06) | 按需加载、约束保留、恢复不叠加、Gate 有效 |

## 不能混用的证据

| 证据层次 | 可以证明 | 不能单独证明 |
|---|---|---|
| 文档/配置登记 | 计划、声明与来源引用 | 原生实际加载、权限隔离或真实角色行为 |
| fake / 本地状态测试 | 状态流转、幂等和竞争规则 | 实际 CLI、Windows 生命周期或模型判断 |
| 真实 CLI + 本地模拟供应商 | 协议、身份、配置、实际工具与故障边界 | Lead 真实理解和自主分工、真实供应商质量 |
| 浏览器走查 | 正常/失败交互、布局和状态展示 | Windows 宿主控制和整机休眠恢复 |
| Windows 宿主实验 | 发行版/用户/目录及宿主控制生命周期 | 未运行的业务/协作/Gate 场景 |
| 真实目标与独立 Gate | 当前产物、角色方法、真实打回与交付 | 其他版本/目标、所有后端组合自动通过 |

W07.06 汇总本表后再判断首版。对外发布、真实用户迁移或外部通知的未授权步骤保留独立交付说明，不把本地演练表述成已上线。
