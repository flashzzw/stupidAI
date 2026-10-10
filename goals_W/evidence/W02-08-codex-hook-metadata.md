# W02.08 · 隔离的 hook 元数据准备与通用格式缺口

2026-10-05，固定 Codex app-server 0.158.0。**旧格式隔离读取的 47 项契约通过；四格式完整预期和通用格式最小对照仍失败。** 本片只验证准备方式及原生格式边界，没有接入产品读取/选配，也没有执行 hook。W02.08 in_progress，9/40 done，Goal active。

## 已成立的准备方式

[元数据探针](../../tests/codex_hook_metadata_probe.py)先在独立 reference profile 中创建一个旧格式 Skill/MCP/hooks 混合插件：两个 SessionStart handler（一个声明为 async）和一个 PreToolUse handler。原生清单取得三个完整 key/hash，在 reference profile 中明确保存测试信任；另创建一个禁用测试插件的线程，只注入标明为合成的历史，随后关闭自有后台。没有发送模型输入。

接着分别创建两个新的 inspection profile，只复制完整插件资源与生成配置，不复制 reference 的配置、信任、认证或历史。两个读取进程仅调用 initialize、initialized 通知和 hooks/list，之后关闭。47 项检查证明：

- 三个 handler 的 key、pluginId、原生 hash、事件、匹配、类型、期限、顺序及 async 元数据与 reference 一致；包含 PLUGIN_ROOT、CLAUDE_PLUGIN_ROOT、PLUGIN_DATA 的样本。
- sourcePath 通过各自缓存根下的相对路径匹配，不将临时缓存绝对路径当作原加载路径。
- inspection 中全部为 untrusted；reference 的 trusted 状态没有被复制或改变。准备结果不能当作原会话的有效策略。
- 原 profile、数据库、合成历史和来源文件的文件哈希前后保持；两个读取进程没有创建或恢复原线程。
- 混合包的命令标记文件没有产生，未请求 turn、shell 或工具调用，也没有 hook 通知。三个最终自有后台均退出 0。

最终正例目录 `/tmp/netcap-w0208-hook-metadata-cylbksoq`；[结果](W02-08-codex-hook-metadata/legacy/result.json)保留逐项检查及原 profile 全部文件的前后哈希。原生协议、stderr 和所复制的来源文件均在 `legacy/`。这证明独立读取可以提供**包定义元数据**，没有证明 hook 执行、异步行为、用户/项目/受管策略或选配已生效。

## 完整预期仍失败

同样加入通用根 plugin.json、extensions.com.openai 内联配置和旧 overlay 回退后，完整预期需要 12 个 handler，固定版本只返回旧格式的 3 个。最终运行停在 reference 的目录对照，8/9 检查通过、整体退出 1；尚未进入三类通用格式的隔离复制阶段。[完整失败结果](W02-08-codex-hook-metadata/full-failed/result.json)，原始目录 `/tmp/netcap-w0208-hook-metadata-6xn5aknz`。

进一步用[最小探针](../../tests/codex_portable_hook_diagnosis.py)移除 Skill、MCP、变量和 async，每个包只保留一个 `true` SessionStart hook。两个旧格式控制分别使用默认路径和显式自定义路径；三个通用格式控制分别使用默认路径、官方示例形式的内联路径数组、旧 manifest 回退，均含根 plugin schema。

| 最小样本 | hooks/list 是否报告该定义 |
|---|---|
| 旧格式默认路径 | 是 |
| 旧格式自定义路径 | 是 |
| 通用根默认路径 | 否 |
| 通用根内联路径数组 | 否 |
| 通用根旧 overlay 回退 | 否 |

原生未报告 warning/error，5/8 检查通过、整体退出 1。没有将缺席改为成功或证明“该包没有 hook”。[最小失败结果](W02-08-codex-hook-metadata/minimal-failed/result.json)，原始目录 `/tmp/netcap-w0208-hook-portable-minimal-4dmndc5y`。

另在混合包上比较 plugin_sharing 开/关，两者的 plugin/read 都能返回通用包 MCP 名称，却不报告 hooks，目录结果相同，见 [sharing 对照](W02-08-codex-hook-metadata/earlier/sharing-comparison/result.json)。这些是固定样本的原生发现缺口，不是对所有版本/格式的永久能力结论。[当前官方插件说明](https://developers.openai.com/plugins/build/plugins)描述了这些 hook 入口，暂不能据此替代固定版本实测。生产当前仍拒绝选中的 hook 组件，没有扩大支持声明。

最初夹具的通用 MCP 漏掉必需的 type，原生 loader 明确记录 `missing field type`；补齐后又发现各包 MCP 同名告警，改用各自名字。修正这两个夹具问题后，通用 hooks 仍缺席；上面的最小对照完全不含 MCP。旧失败、loader 诊断和各次结果保存在 `earlier/`、`diagnostics/` 与 `logs/`，不合并为一个通过计数。

## 重跑与后续

```bash
# 保留四格式完整预期；当前固定版本退出 1。
python3 tests/codex_hook_metadata_probe.py
# 明确只验旧格式，当前 47 项通过，不代表通用格式通过。
python3 tests/codex_hook_metadata_probe.py --legacy-only
# 独立最小对照，保留三个通用样本的发现预期；当前退出 1。
python3 tests/codex_portable_hook_diagnosis.py
```

两个 Python 文件的 AST 检查通过。生产 Go/Web 和构建未修改，未重复 race、浏览器或构建。[审计](W02-08-codex-hook-metadata/audit.json)、[差异](W02-08-codex-hook-metadata/changes.diff)和[归档时点哈希](W02-08-codex-hook-metadata/manifest.json)核对来源、原 profile、旧证据和两个二进制，未提交。

下一片按已成立的旧格式契约接生产包元数据准备，必须核对声明与原生返回条目，拒绝不完整结果；通用 hook 缺席继续保留失败并待更具体的原生条件，不转换成另一种格式冒充原包。当前样本在 /tmp 独立根下；生产入口还需覆盖嵌套项目隔离、读取期间版本变化、异常进程清理及受管来源影响。成功 hook 的 WSL 对照及既有三个终端结果仍待返回，不重复请求。完整选配/信任/版本绑定、实际执行归属、受管与异步、方法/Gate、Windows/proxy 和输入独占等硬要求均保留。
