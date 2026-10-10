# W01.02 · 只读后台身份核对与原生接续实验

日期：2026-10-01。接续[来源登记](W01-02-binding.md)。本片修复 Codex 缓存连接复用时未重核状态目录的问题，并取得固定 0.158.0 的真实 stdio 接续对照。**来源仍为只读；原执行器接入、完整配置/权限继承和输入权交接没有完成。** W01.02 in_progress，累计 8/40 done，Goal active。

## 生产变化与原始失败

[回归红灯](W01-02-executor-namespace-red.txt)在发现、单项预览和历史读取三个 Runtime 入口复现：先成功读取自己的后台，再保持实例 ID、端点、项目和原生 ID，改用另一状态目录；原实现仍向缓存后台发出 RPC，接受结果，并保留连接。测试使用真实自有进程的环境，RPC 返回受控；已捕获的 socket inode 用普通文件代替，没有启动监听器。

进一步检查发现，新连接的 `internal/codex/proxy.go` 已核对 **非空** `initialize.codexHome`，所以不能笼统称原实现完全没有目录检查。缺口是缓存复用只检查 PID/启动标识/socket，以及初始握手允许省略该字段；原 `/proc` 环境核对仅在选用 `env:` 凭据时执行。

[Runtime 修复](../../internal/runtime/codex_peer.go)把状态目录核对独立出来，复用已配置 Unix 端点的同用户 peer 身份，不扫描其他进程。新连接在代理握手前后核对，缓存复用在业务 RPC 前核对；读取环境前后继续核对进程和端点。只选择 `CODEX_HOME` / `HOME`，读取有界、拒绝不完整/重复/相对路径/非目录/不匹配来源，支持真实目录相同的符号链接与 HOME 默认目录。连接内保存目录 inode，后续别名改指或同路径目录替换也会拒绝。

失败沿用 `failed`：关闭本工作台代理、移除缓存，将已有审批标为 `unverified`，不停止共享后台。原凭据校验继续要求非空指定 key/value 和常量时间比较；只读目录验证不要求凭据值，也不输出环境正文。现有应用与 HTTP 错误通道直接显示拒绝原因；UI 结构、来源登记和控制准入没有变化。[生产差异](W01-02-executor-production.patch)以本片开始时文件为基线。

## 真实原生协议对照

[重跑脚本](../../tests/codex_native_resume_probe.py)使用固定 Codex App Server 0.158.0 的 `app-server --stdio`，所有 HOME/profile/项目均为自有临时目录。供应商配置指向 `127.0.0.1:9`、凭据为合成值；没有 `turn/start`、模型执行、真实供应商请求或共享后台连接。除第一项外，`thread/inject_items` 只追加明确标记的合成用户历史，不生成执行轮次。

全局模型为 `gpt-5.4`、审批 `on-request`、沙箱 `read-only`；`thread/start` 显式指定 `gpt-6-astra`、`never`、`workspace-write`。接续只传原 ID 和 `excludeTurns:true`。比较字段为模型、供应商标识、cwd、运行目录、审批、sandbox、activePermissionProfile；没有把供应商标识当作完整 endpoint/认证证据。

| 场景 | 实际结果 | 结论边界 |
|---|---|---|
| [未落盘线程](W01-02-executor-resume-unmaterialized.json) | `thread/read` 报 idle / 可输入，`resume` 返回 `-32600 no rollout found`；预期拒绝断言通过，退出 0 | 空闲/可输入不保证已有可恢复 rollout |
| [同后台已加载线程](W01-02-executor-resume-loaded.json) | 原 ID 和列明设置保留；退出 0 | 仅此隔离线程和读取字段 |
| [取消订阅后](W01-02-executor-resume-unsubscribe.json) | 返回 unsubscribed，后续读取仍为 idle；原 ID/设置保留，退出 0 | 取消订阅不证明执行器已卸载；不能称为冷恢复 |
| [自有后台退出后重启](W01-02-executor-resume-restart.json) | 恢复前 `notLoaded`；恢复后原 ID、模型、审批保留，但 sandbox 从 `workspaceWrite` 变为 `readOnly`；继承断言失败，退出 1 | 这是只有合成历史、没有执行轮次的样本；不能外推为所有历史都丢权限，也不能用原 ID 成功宣称完整继承 |

脚本在 RPC 完成以外检查具体语义；冷恢复结果为 `completed:true, passed:false`，未将它改写为通过。自有服务器未及时响应停止信号，fixture 最终终止其进程组，记录 exit_code=-9；全部自有子进程已退出。这不是正常原生退出或共享后台停止验收。

协议依据是之前从固定本地 CLI 生成的 schema，见[字段与哈希](W01-02-executor-schema-notes.json)；没有新获取的官方网页。`ThreadResumeResponse` 没有完整 provider endpoint，先前[线程路由对照](../../tests/codex_route_config_probe.py)也说明 `config/read` 不能代表每个线程的实际覆盖。本次未重跑该旧路由实验。stdio 真实 RPC 在本环境可运行，不能将监听权限限制表述为所有真实 Codex RPC 都无法验证。

## 本片验证

| 层次 | 结果 |
|---|---|
| 新增身份回归 | [29 个场景](W01-02-executor-runtime-race.txt)通过：3 个只读入口、18 个环境边界、8 个自有进程预检/复检；包含匹配、别名、目录替换、进程启动标识变化、取消，以及旧审批失效。初始 peer 的 Unix 握手由 fixture 代替，没有真实监听验收 |
| Codex 相关回归 | [最终定向 race](W01-02-executor-codex-race.txt)通过：17 个顶层测试及 70 个子测试，含凭据/路由、审批、恢复、发现、历史和新身份边界；顶层和子测试数不是独立场景数之和 |
| 应用/HTTP 回归 | [全包 race 日志](W01-02-executor-packages-race.txt)：app 和 console 通过；同命令的 runtime 因既有 OpenCode `httptest.NewServer` 监听被拒而失败，整体退出 1。未把这一命令计为全通过 |
| 静态检查/构建 | [全包 vet](W01-02-executor-go-vet.txt)退出 0；[开发构建](W01-02-executor-build.txt)通过，含 Web 类型检查/Vite 与 `.dev/bin/netcap`；最终 Web 资源保持 `index-N7Fv3q5U.js` / `index-orGpbq3d.css` |
| 原生 stdio | 上表三项预期检查通过，冷恢复设置保留检查失败。均未经过生产 Unix proxy、HTTP 或浏览器 |
| 未执行 | 真实生产 socket 握手、新连接完整集成、供应商/工具执行、浏览器重跑、独立前台、共享后台控制、Windows；本片没有这些层次的新增通过结论 |

```bash
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime -run 'TestCodex|TestFreshUnmaterialized' -count=1 -v
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/runtime ./internal/app ./internal/console
GOCACHE=/tmp/netcap-w0206-auth-go-cache go vet ./...
GOCACHE=/tmp/netcap-w0206-auth-go-cache make dev-build
python3 tests/codex_native_resume_probe.py --mode unmaterialized --output .dev/w0102-executor/resume-unmaterialized.json
python3 tests/codex_native_resume_probe.py --mode loaded --output .dev/w0102-executor/resume-loaded.json
python3 tests/codex_native_resume_probe.py --mode unsubscribe --output .dev/w0102-executor/resume-unsubscribe.json
python3 tests/codex_native_resume_probe.py --mode restart --output .dev/w0102-executor/resume-restart.json
```

最后一项当前返回 1 是保留设置断言的真实失败；全包 race 的环境失败见上表。产物、来源与证据哈希见 [manifest](W01-02-executor-manifest.json)，只代表此次归档时点。[清理检查](W01-02-executor-cleanup.json)核对 412 个旧证据未变，原生脚本记录的自有进程均已退出；没有提交或对外操作。

## 后续

继续 W01.02 的原执行器接入，分别建立已加载与冷恢复路径的原权限、供应商配置与输入权证据。只有原生 ID、元数据和 `resume` 成功不足以放行控制；本片没有用当前新建路由覆盖外部原线程。W01 六入口/工具结果、W02.06 EOF/认证、W03.02 用户反馈及 Windows 缺口继续保留。
