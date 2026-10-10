# W04.01 / W01.02 · Windows生产工作台与真实工具恢复

2026-10-08。真实Windows PowerShell 5.1.19041.6157经wsl.exe启动Ubuntu-24.04 / zzw中的既有Go serve、Codex后台和HTTP中继。固定后台0.158.0、proxy 0.157.1；开发二进制SHA256为`cafea6e122ac2eb5b092a0f557ab32775d04a8d6941a4e16deaec1d30ba19dfa`。全部使用隔离profile、状态、中文/空格/单引号cwd、本地模拟供应商和自有工具。

入口：`python3 tests/wsl_application_smoke.py`，默认读取`.dev/bin/netcap`。最终目录`/tmp/netcap-wsl-application-v29i7971`；持久工作目录`.dev/w0401-application-work`。测试通过Windows进程的stdin/stdout字节通道发出控制命令，HTTP中继在目标WSL中读取私有令牌并访问真实Handler；Windows不直接承载Go进程或HTTP服务。复用上一片Windows字节传输和原生proxy，不使用fake Runtime或模拟Handler。

[最终原生结果](W04-01-application-control/final/result.json.gz)29项通过：

- Windows启动的后台/工作台运行映像、实际cwd和UID正确。原生只读登记/观察取得生产`codex.unix_peer`证据，PID、启动标识、可执行路径、UID/用户名与发行版对应实际自有后台。观察不改变Session，发送、恢复、接管和中断均被拒绝，不创建操作或模型请求。
- 工作台创建的独立会话启动一个真实Python工具。工具受原生workspace沙箱约束，等待本地测试栅栏；其命名空间PID为2，宿主PID、命名空间、启动标识、UID、cwd和脚本路径独立绑定。
- 工具运行时先人工接管，再由Windows控制通道对自有Go进程组发送SIGKILL，实际退出-9。原后台和工具在Go退出期间及新Go连接后保持原宿主PID/启动标识；操作仍running，原native ID/turn ID、回执和执行快照不变。工具不重做，模型输入不重发；未完成时恢复命令被拒绝。
- 放开栅栏后，同一工具的完整JSON输出进入原会话的模型请求，并保存于原生rollout。原操作变为succeeded，人工接管/暂停仍保留；空闲后显式恢复成功，重复恢复命令复用回执，下一轮仍使用原native ID。
- 最终4个Windows子进程及宿主退出；2个接收器、1个HTTP中继、1个proxy、1个原生后台、2个Go进程和1个工具共8个记录的Linux进程退出，本地供应商关闭。只发生1次实际工具执行、4次本地模型请求。

[独立审计](W04-01-application-control/audit.json)55项通过。从[Windows字节记录](W04-01-application-control/final/host-transcript.json.gz)解码43对HTTP请求/响应、194个传输操作，逐份比对关键生产回执与观察；检查原生rollout的一次custom_tool_call及相同call_id的完整输出，并确认其与本地供应商收到的两块内容逐值一致。第二块是工具JSON，第一块是原生执行时间说明。直接只读打开SQLite，完整性检查通过：2个Session、4个成功操作，原工具回执/快照保持；外部记录仍无路由和控制权限，被拒命令未持久化。解码材料、持久化表和原生日志均归档，私有访问令牌未进入输出。

本次修正的是探针假设，生产代码未改：

- 重新复现PermissionError，栈指向`/proc/2/cwd`。自有后台子进程观测证明sandbox PID 2映射到另一宿主PID；没有给产品放宽权限，也没有对宿主PID 2发信号。修正后只按自有后台后代、精确脚本、命名空间和启动标识取得唯一宿主身份。原失败与最小进程观测保留。
- 工具已开始时，生产操作可能暂为acknowledged；原断言误要求同步成为running。修正为等待生产发布running后再注入崩溃，没有忽略操作状态。修正后的首轮27项通过，再补直接进程样本和Windows子进程退出断言，最终29项通过。
- 审计初次把所有工具输出块都当JSON，遇到原生时间说明块失败。核对原文后分别严格验证说明块与完整JSON，最终55项通过。

中断前的2026-10-07临时目录和基线已不存在，旧HTTP状态码失败仅有会话记录，不补造落盘证据；本轮从持久草稿重新复现。新的基线复制曾与两行诊断编辑交错，已还原那两行并核对原SHA256，使before副本与记录哈希一致，详见归档说明。该过程没有改生产源码或旧证据。

Python语法及最终完整原生运行通过。没有生产/界面改动，未重跑Go测试、构建或浏览器。6136份前置证据、基线生产源码/构建、已有其他测试和两个二进制不变；无提交、推送或真实供应商调用。源码、日志、diff、清理与哈希见[manifest](W04-01-application-control/manifest.json)。

W01.02/W01.05/W04.01继续in_progress；整体9/40 done、Goal active。这是现有Application/Runtime/Handler的宿主与工具正例，不证明任意外部会话接管、输入独占、完整权限/配置或全部故障边界。生产会话的普通消息投影没有展示此次code-mode工具全部细节，本片通过原生日志与模型请求核对输出，不宣称工具详情界面已验收。Claude supervisor/worker、第二有效发行版/用户、桌面界面及休眠/整机重启仍待补；下一步优先复验环境已恢复的Claude后台控制与原工具结果连续性，其余W01/W02硬要求和底座门槛保留。
