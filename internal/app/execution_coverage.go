package app

// Coverage describes the saved inputs that ReadExecution can compare. It never
// promotes file equality or a cached native observation to effective config.
type ExecutionSourceCoverage struct {
	Scope string                `json:"scope"`
	Areas []ExecutionSourceArea `json:"areas"`
}

type ExecutionSourceArea struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func executionSourceCoverage(snapshot *ExecutionSnapshot) ExecutionSourceCoverage {
	out := ExecutionSourceCoverage{Scope: "unrecorded", Areas: []ExecutionSourceArea{}}
	if snapshot == nil {
		return out
	}
	// The API's coverage is bounded by this reader, regardless of a snapshot's
	// historical coverage label. Missing fields remain missing in old snapshots.
	out.Scope = "recorded_sources_only"
	add := func(id, name, status, detail string) {
		out.Areas = append(out.Areas, ExecutionSourceArea{ID: id, Name: name, Status: status, Detail: detail})
	}
	recorded := func(present bool) string {
		if present {
			return "recorded"
		}
		return "unrecorded"
	}
	addRecorded := func(id, name string, present bool, detail string) {
		if !present {
			detail = "此快照未记录该类来源；本次检查不能判断它是否变化，也不会补造历史版本。"
		}
		add(id, name, recorded(present), detail)
	}
	addRecorded("profile", "CLI 主配置文件", snapshot.ProfileSource.Path != "", "只比较保存的文件摘要；不代表原生合并后的配置。")
	if snapshot.Instance.Kind == "claude" {
		status := recorded(snapshot.SettingsSource != nil)
		if snapshot.SettingsSource == nil && snapshot.Instance.SettingsFile == "" {
			status = "not_selected"
		}
		add("settings", "Claude 自定义 settings", status, "仅覆盖本次显式选择的 settings 文件。")
		addRecorded("claude_local_settings", "Claude 项目/本地设置", snapshot.ClaudeLocalSettings != nil, "核对记录的文件与当前 Git/cwd 来源路径集合；未记录的旧快照不会补造来源。")
		if snapshot.ClaudeInstalledPluginSettings != nil {
			addRecorded("claude_installed_settings", "Claude 已安装插件设置", installedExecutionSnapshot(snapshot), "比较保存的安装登记、声明资源树与派生文件计划；原生加载及完整启动来源在准备、应用和输入时另行核对。")
		}
	}
	if snapshot.Instance.Kind == "codex" {
		addRecorded("codex_configuration_sources", "Codex 磁盘合并来源版本", snapshot.CodexConfigurationSources != nil, "此处核对已保存的配置来源文件；原生合并值、来源集合及字段来源在连接原生后另行核对。受管权限要求仍由原生权限流程处理。")
		addRecorded("codex_user_hooks", "Codex 用户 hook 文件", snapshot.CodexUserHooks != nil, "覆盖记录的 hooks.json，包括记录时不存在的状态；不表示已执行。")
		addRecorded("codex_project_hooks", "Codex 项目 hook 文件", snapshot.CodexProjectHooks != nil, "只核对已记录文件；空集合表示记录时未发现来源。来源集合与配置层需连接原生另行核对。")
	}
	status := "recorded"
	if len(snapshot.CapabilityRefs) == 0 {
		status = "not_selected"
	}
	add("capability_refs", "显式能力引用", status, "比较已引用版本的文件或目录摘要，目录内资源一并核对；未引用不代表原生未加载。")
	add("inherited_configuration", "完整 user/project/受管配置", "not_covered", "尚未汇总全部继承来源、合并优先级与外部热更新。")
	add("external_dependencies", "来源外的脚本与运行依赖", "not_covered", "已记录目录之外的引用脚本、可执行程序与进程环境未完整覆盖。")
	add("dynamic_capabilities", "账号与服务端动态能力", "not_covered", "凭据绑定检查不验证服务端工具、账号授权或动态能力保持不变。")
	add("effective_permissions", "完整权限合并结果", "not_covered", "已保存的权限模式与原生局部证据不能证明全部继承规则、权限冲突或 Gate 已核对。")
	return out
}
