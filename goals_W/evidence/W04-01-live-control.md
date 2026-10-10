# W04.01 · Windows 存活控制、断连与原 ID 冷恢复

2026-10-07。Windows PowerShell 5.1.19041.6157 → wsl.exe → Ubuntu-24.04 / zzw，固定 Codex 后台0.158.0、Unix proxy 0.157.1。本片新增可重跑宿主控制探针；生产Application、界面和构建未改。

运行 `python3 tests/wsl_live_control_probe.py`。最终目录 `/tmp/netcap-wsl-live-ccncn1mf`，初版目录 `/tmp/netcap-wsl-live-84xknks_`。Linux侧测试驱动提供指令，Windows进程实际启动wsl.exe并读写其stdin/stdout字节；WSL接收器启动自有后台，原生proxy连接其Unix socket。没有把纯Linux stdio运行称为Windows控制，也没有预先选定生产桌面框架或通信协议。

最终[原生结果](W04-01-live-control/final/result.json.gz)33项通过：

- 中文、空格、单引号cwd的实际UID/用户名/发行版正确；后台与proxy的运行映像、PID/启动标识和隔离CODEX_HOME分别核对。所测模型/路由/沙箱/审批返回值与请求一致。
- 第一轮完成并逐值核对回答。第二轮已到本地供应商，响应被测试栅栏挂起；Windows调用其自有wsl.exe控制句柄的Kill，Windows进程退出-1，对应Linux proxy退出。
- 原后台PID/启动标识不变。新Windows控制连接读取到原会话与原inProgress轮次，不发送新输入、不调用resume；由新连接中断原轮次，第三轮完成。原第一轮条目不变，第二轮保持interrupted且没有伪造的助手成功回答。
- 自有后台正常停止、原socket移除后，由另一Windows启动重新建立后台。先读取原ID/三轮历史，再显式携带所测设置恢复；全部轮次与原设置保持，没有第四个模型请求或第二次thread/start。此处是正常退出后的冷恢复，未验证后台硬崩溃。
- 最终6个Windows子进程及PowerShell退出；2个WSL接收器、2个后台、3个proxy共7个Linux进程退出。本地供应商关闭；没有使用共享后台或真实模型供应商。

[独立审计](W04-01-live-control/audit.json)41项通过。审计从[Windows原始传输](W04-01-live-control/final/host-transcript.json.gz)解码base64、WebSocket握手及帧，逐条比对3条连接的请求和回执，与原生结果中的结构完全相同。总计67个Windows传输操作；只有1次thread/start、3次turn/start、1次turn/interrupt、1次冷thread/resume。三个本地请求的身份、认证匹配、完整用户输入及第三次请求保留首轮助手结果均直接核对。解码结果和审计源码一起归档。

初版23项通过后增加冷恢复、完整发行版字节读取、proxy映像及清理检查，最终33项通过。独立审计最初误把Windows子进程数写为7，实际为6（发行版清单1、后台接收器2、proxy3）；原失败日志保留，修正审计后通过。Linux进程确实为7。产品/原生流程没有失败被改记成功。CLI关于临时CODEX_HOME下不创建PATH别名的warning与Windows CLIXML模块初始化进度保留；不要求stderr为空，审计未发现Windows Error流。

语法检查及既有`wsl_host_probe_test.py`四项错误编码回归通过。没有生产修改，因此未重跑Go、bridge/Web构建或浏览器。最终全部基线生产源码/构建、既有测试和两个二进制不变；6081份前置证据不变。开发二进制SHA256为`cafea6e122ac2eb5b092a0f557ab32775d04a8d6941a4e16deaec1d30ba19dfa`。完整改动、构建标识、进程清理和日志见[manifest](W04-01-live-control/manifest.json)。未提交或推送。

W04.01保持in_progress，整体9/40 done、Goal active。宿主清单实际只有Ubuntu-24.04；第二有效发行版/用户尚未验收，不安装发行版或创建用户。此片仅覆盖Codex原生通路与文本供应商在途，未覆盖Claude宿主控制、真实工具在途、生产Application/界面、共享后台输入独占、宿主控制器整体崩溃及整机休眠/重启。完整权限/动态来源不由所测设置相等推导。后续可将已验证通路接到生产身份/控制入口；W04.02及最终底座决定的其他前置仍保留，W01/W02硬要求未缩减。
