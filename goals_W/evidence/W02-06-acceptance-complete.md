# W02.06 · Claude MCP 原生选择与认证隔离收口

2026-10-02。依据 W02.06 现有正文收口；不修改需求或扩大已验范围。用户运行 `python3 tests/claude_w0206_acceptance.py`，目录 `/tmp/netcap-w0206-acceptance-43htq8u1`。[汇总](W02-06-acceptance-complete-runner.json)的开发二进制 SHA256 与当前 `.dev/bin/netcap` 一致。固定 Claude 2.1.280；真实 CLI/生产控制台，隔离本地模拟供应商/MCP。

## 当前版本回归

[stdio](W02-06-acceptance-complete-stdio.json)39 项与[资源改配](W02-06-acceptance-complete-resources.json)97 项全部通过，两个子进程退出 0。代理直接读取原始结果，与汇总逐项相等，核对构建哈希；没有在代理受限命令环境重跑。

[落盘复核](W02-06-acceptance-complete-verification.json)确认 stdio 实际调用 A 1/B 2；资源流程实际调用共 14 次：A/B 各两次原 stdio、两次恢复继承，加六次 HTTP/SSE。所有调用认证与 cwd/workspace 归属正确。逐份读取供应商请求，认证匹配且所有可识别身份工具结果均回到各自配置；历史重复出现不计成新调用。

六份改配结果分别核对 A/B 的 shared v1 + additional r1 → shared v2 + additional r1 → 两项均恢复继承、显式引用为空；历史逐次追加，显式选择服务数 4→4→0，撤去两项显式引用后 identity 显示 project/inherit。运行时断言另外证明原生 UUID、消息历史、操作/版本绑定保持；最终落盘不是所有时序条件的替代。只归档脱敏投影、结果及原请求哈希，不复制凭据或完整模型请求。

## W02.06 条件逐项结论

| 既有条件 | 证据 | 结论 |
|---|---|---|
| 生产 MCP 选择、实际工具清单、单次权限 | 本次 stdio 39 项；[HTTP/SSE 动态头](W02-06-helper-user-full.md)各 45 项 | 通过；审批不授予下一次调用 |
| A/B cwd、凭据与状态隔离，结果回各自模型 | 本次两组落盘复核；[OAuth HTTP](W02-06-oauth-http-user-recovery.md)/[SSE](W02-06-oauth-sse-user-recovery.md)各 71 项 | 通过；本地模拟供应商，无真实账号外推 |
| 排除不暴露、不调用；保留其他继承 | 本次 stdio 排除/恢复；[来源矩阵](W02-06-sources-user-pass.md)44 项来源、13 项连接 | 通过；同名继承阻止确认禁用，不清空其他来源 |
| settings 覆盖、继承、strict 配置分别核对 | 上述 16 场景原生矩阵；本次恢复 project/inherit | 通过该固定版本的列明场景 |
| 原 ID 恢复，改配/不支持边界可见 | 本次 97 项资源与原 ID；[启停/故障](W02-06-apply.md)、[资源/丢回执](W02-06-resources.md)；执行差异及准备/应用界面 | 通过；仅关闭自有空闲 query，历史失败不重写 |
| 可操作界面及认证准备 | [启停浏览器](W02-06-apply.md)、[资源浏览器](W02-06-resources.md)、[准备/来源 UI](W02-06-auth-ui.md)，原生认证由两协议独立实测补证 | 分层条件通过；不把受控浏览器说成一次完整原生 UI 端到端运行 |
| 工程收尾 | 上轮 app/console 全包 race、认证 10 项/来源 7 项；[最近构建与诊断回归](W02-06-login-helper.md)；本轮源码/产物/引用核对 | 本轮没有代码或构建变更，不把历史测试列成本轮重跑 |

据[先前审计](W02-06-acceptance-audit.md)，所缺当前版本 stdio 与多资源回归现已补齐。W02.06 按其定义完成，累计 9/40；W02 工作包仍未完成，W02.07/08/09 的各自实施与联合验收未通过。

## 明确保留

- headersHelper 与 OAuth 登录侧目录组合仍拒绝，[原生证据](W02-06-login-helper.md)表明 cwd 包装不能保留凭据环境。此组合不算支持；原配置保持和明确说明不支持属于 W02.06 的既有边界处理要求，没有通过改任务文本或降低条件消除问题。
- 完整 user/project/受管/插件/账号来源与权限优先级、外部热更新、四配置联合矩阵继续归 W02.09；P11–P15 及 R22/R23/R25 不整体勾选。此前权限 deny/ask 的清单样本不是实际权限冲突执行。
- W01 六入口/工具结果/输入权、真实插件、协作与 Windows 条件全部保留。
- 用户首条宿主命令误写 .p，随后更正 .py 并提供真实宿主结果；W04.01 的参数/身份通过及错误解码问题另行记录。

进度将 W02.06 更新为 done，W02.07/08 前置现满足；可沿现有能力库继续原生 Skill/插件选择。整条 Goal 未完成；本次工具查询仍为 blocked，不用重建/缩小 Goal 改变状态。

旧证据与旧 bin/netcap 保留；[diff](W02-06-acceptance-complete-diff.patch)和[manifest](W02-06-acceptance-complete-manifest.json)记录本次归档与进度变更。未提交、对外调用或操作共享后台。
