# W02.08 · Claude 预解析与原生本地设置根目录差异

日期：2026-10-06。W02.07/W02.08 **in_progress**，整体 **9/40 done**，Goal active。

## 新结论

固定 SDK **0.3.280** 的 `resolveSettings({cwd, settingSources:['user','project','local']})` 和原生 CLI **2.1.280** 在仓库子目录及 worktree 启动时读取的本地设置来源不同。即使解决上一片的注册表/JSON 错误遗漏，也不能直接把此接口的返回值当作完整启动前配置。

原生在所测自有 POSIX 仓库中合并**主仓库根**与 **cwd** 的 `.claude/settings.local.json`；同名项由主仓库根覆盖，cwd 独有项保留。SDK 仅读取 cwd 那份。项目 `.claude/settings.json` 仍取当前工作目录；把 resolver 的 cwd 简单改成主仓库会丢失另一些实际来源，因此不作为修复。

| 启动位置 | SDK 的 local | 原生的 local | 预期一致性 |
|---|---|---|---|
| 主仓库根 | 主仓库根 | 主仓库根 | 通过 |
| 主仓库子目录 | cwd | 主仓库根 + cwd；根同名项优先 | **失败** |
| worktree 根 | worktree 根 | 主仓库根 + worktree 根 | **失败** |
| worktree 子目录 | cwd | 主仓库根 + cwd | **失败** |
| 非仓库普通目录 | cwd | cwd | 通过 |

worktree 子目录原生结果不含中间 worktree 根的独有测试键，因此“扫描所有祖先即视为已加载”同样不准确。以上结论限于本次布局；不同所有者、HOME 为仓库、裸仓库、外置 git dir、Windows 原生路径和显式项目根覆盖尚未验证。

## 实际启动影响

主仓库根 local 中声明了一个自有 SessionStart hook，只向对应测试目录写入 cwd 和测试环境标记。最终四个仓库场景均实际写入主仓库的 `repo` 标记，原生 `hook_started` / `hook_response` 的同一 hook ID、session ID 和成功退出 0 可核对。普通目录无该 hook。SDK 调用均没有执行它，三个存在来源差异的 SDK 结果也没有对应的主仓库设置。

这证明缺少的来源能够在原生初始化期间产生实际副作用，不能等初始化以后再把预解析结果补齐并称为启动前排除。**不是完整 hook stdin 载荷或插件选配验收**；最终标记脚本不读取 stdin，也不调用模型、工具、MCP 或其他进程。

首轮 hook 脚本试图从 stdin 解析 JSON，四例均报 JSONDecodeError、退出 1，没有写出标记，见 [失败摘要](W02-08-claude-settings-roots/hook-stdin-failure.json)。该轮没有捕获输入字节，原因未在本片定位；结果原样保留。最终将夹具限定为不依赖载荷的启动来源观察，不能据此宣称该载荷问题已解决。

## 验证、失败与隔离

- [初始两场景](W02-08-claude-settings-roots/probe.log)：8/9、退出 1，仓库子目录来源不一致。
- 五场景第一次扩展在普通目录与证据目录重名时提前退出，保留 `matrix.log`；这是夹具路径错误，没有完整结果。修正为独立 `cases/` 后，纯元数据五场景 18/21、退出 1，见 `matrix-final.log`。
- [最终原生结果](W02-08-claude-settings-roots/native-result.json)：**33/36、退出 1**。三项 `_same_markers` 失败，其余来源隔离、SDK 无 hook、真实 hook 标记和终态、零模型输入、原始来源不变均通过。没有把“诊断成功发现差异”改写为“原生/SDK 一致性通过”。
- [独立审计 36 项](W02-08-claude-settings-roots/audit.json)通过：以原始输入摘要、两侧 marker/source、实际标记文件及协议生命周期核对上述差异，不用执行器布尔值替代独立依据；不证明完整 preflight。
- Python AST 和 Node syntax 检查通过。生产实现、Web 和依赖未修改，未重复 Go、浏览器或构建测试。

最终原始目录 `/tmp/netcap-w0208-settings-roots-yatdbg_w`；各轮目录见 [清单](W02-08-claude-settings-roots/original-directories.json)。使用隔离 HOME/XDG/profile、自有 Git 仓库与一个 detached worktree；为建立 worktree 创建一次测试空提交，无项目提交或外发。所有 CLI 只接收 initialize/get_settings 控制请求，没有模型输入或模型结果，供应商是不可用本地端点。五个原生进程收尾 SIGTERM、退出 143；SDK 子进程均完成。测试 hook 有独立成功终态，不以 CLI 的 143 代替 hook 终态。

SDK 的注册表读回复为受控缺席，不调用 Windows；原生 CLI 使用自己的正常启动路径。此片不推断实际 Windows 受管策略为空，不重复独立宿主探测。归档只留测试标记、来源摘要及协议类型/生命周期，不复制完整 native settings、认证文件或 Git 状态库。

## 文档、复跑与下一步

[本轮官方文档查询](W02-08-claude-settings-roots/documentation.md)及固定 SDK 类型/实现核对表明：公开 resolver 没有 CLI 内部用于本地来源规范化的 canonicalGitRoot 参数。`startup()` 会实际启动 CLI 并完成初始化，也不能代替无副作用预检。生产代码没有引入任何 SDK 私有符号。

```bash
# 当前固定版本预期退出 1：原生与 SDK 的三处来源仍不一致。
python3 tests/claude_settings_roots_probe.py
node --check tests/claude_settings_roots_probe.mjs
python3 goals_W/evidence/W02-08-claude-settings-roots/audit.py
```

新增 [Python 对照](../../tests/claude_settings_roots_probe.py)、[SDK 观察器](../../tests/claude_settings_roots_probe.mjs)；[source.diff](W02-08-claude-settings-roots/source.diff)和 [manifest.json](W02-08-claude-settings-roots/manifest.json)绑定本次证据。**3379** 个旧 W 证据不变；开发二进制仍为 `758d3764aed997585d9c95ca86d68156ea511dd3de3a359a0f9139b7aed25855`，旧 bin 为 `e3c9ac0f8b215dad7c93ce34c13c0c91190ac68adc0210aa89bbbf98a1395fdf`。

下一片先建立与原生一致的 Git/cwd 本地来源根与文件身份绑定，再接来源不可读/无效的明确拒绝和安装插件事务。不能仅依赖 SDK 返回的 source.path 列表、不能把 cwd 改成主仓库来掩盖差异，也不以加载后检查替代启动前排除。可靠受管预检、helper/服务端来源、五项待回 WSL 请求、方法任务/Gate、原生丢确认恢复、输入独占、Windows/proxy/peer 与其他 hook 要求全部保留。
