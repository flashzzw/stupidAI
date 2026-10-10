# W02.06 · OAuth 登录侧目录与 helper 凭据环境

2026-10-02。上一片 HTTP/SSE 动态头 90 项通过后，本片核对剩余组合缺口。W02.09 定义完整有效配置/权限联合验收；本片聚焦 W02.06 的 helper OAuth 准备拒绝，不改变任务或硬要求，不将组合判为支持。

## 原生证据

官方 [MCP 文档](https://code.claude.com/docs/en/mcp)经 Context7 查询明确：项目 `.mcp.json`/插件的 headersHelper 会移除名称含 KEY/TOKEN/SECRET 等凭据环境变量，SDK/命令行动态来源不采用相同清理规则。[查询结果](W02-06-login-helper-docs.json)是当前文档；固定 2.1.280 的行为由下面探测单独验证。

新增 [claude_mcp_login_helper_probe.py](../../tests/claude_mcp_login_helper_probe.py)，复用现有 SDK helper 观测入口，独立 A/B 配置各比较 dynamic、原样 sidecar、cwd wrapped sidecar。工作目录包含空格/单引号，相对 helper 读取项目资源，合成凭据只记录是否存在/匹配，不保存值。测试在自有项目中显式设置信任以隔离信任拒绝，不改变用户配置；不补回 CLI 清理的凭据。

[六场景结果](W02-06-login-helper-native.json)与[输出](W02-06-login-helper-native.txt)：26 项观测断言通过，进程退出 0。dynamic 的 cwd/资源/配置正确且凭据存在；原样 sidecar 没有 helper marker；wrapped 恢复原 cwd/资源与配置目录，但 A/B 凭据均不存在。因此 `cwd_wrapper_preserves_dynamic_credentials=false`，**简单 cwd 包装不能支持原绑定的 OAuth helper 组合**。代理直接执行此探测，随后核对各场景原始 result.json/helper.json 与汇总一致。

探测只访问不可用本地端点以触发 helper，全部 model_results=0。没有模型提示、监听服务、OAuth 登录或真实供应商调用；不以连接状态证明认证成功。退出 0 的含义是复现了上述不兼容性。不能据此断言没有其他原生登录入口。

## 产品与验证

生产 `prepare_mcp_login` 保留对 helper 的拒绝，但错误说明同时指出工作目录与原生凭据环境规则变化，不再只归因 cwd；普通准备、私有目录与幂等行为保持。CONSOLE.md 同步原生来源差异。

[回归红灯](W02-06-login-helper-red.txt)证明原错误遗漏凭据环境原因；修正后 [Application race](W02-06-login-helper-tests.txt)两项准备/绑定/私有/持久/失败清理测试通过。该命令的 console 筛选没有匹配测试，不能算 console 用例覆盖。相关 app/console [vet](W02-06-login-helper-vet.txt)、[开发构建](W02-06-login-helper-build.txt)和 Python AST 检查通过。构建含 Web typecheck/Vite；本轮没有浏览器交互验收，Web 源码未改。

## 恢复与边界

```bash
python3 tests/claude_mcp_login_helper_probe.py
GOCACHE=/tmp/netcap-w0206-auth-go-cache go test -race ./internal/app -run '^TestMCPLogin' -count=1
```

后续需验证保持原生来源、cwd 与凭据规则的登录入口，取得真实登录与原执行配置复用证据后才能扩展生产组合支持；不通过重新注入被清理的变量或默默更换凭据来源完成。用户终端已有独立 HTTP/SSE OAuth 和 helper 正例继续有效。W02.06 in_progress、8/40 done，Goal active；W01/W04 及完整权限联合验收保持未完成。

旧 `bin/netcap` 与上片哈希一致，上一片证据附件哈希一致；开发二进制只通过 make dev-build 更新。源码与进度变更见 [diff](W02-06-login-helper-diff.patch)，归档时点哈希见 [manifest](W02-06-login-helper-manifest.json)。没有提交、调用真实供应商或停止共享后台。
