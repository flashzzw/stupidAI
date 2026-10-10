package runtime

// describeVersion reports only the CLI versions exercised by the protocol
// contracts. Declared config versions do not grant capabilities to other CLIs.
func describeVersion(kind, version string, commandErr error) Description {
	d := Description{Kind: kind, Version: version, Compatibility: "unverified", Capabilities: map[string]string{}}
	expected := map[string]string{"codex": "codex-cli 0.157.1", "claude": "2.1.280 (Claude Code)", "opencode": "1.18.32"}[kind]
	d.Detail = "已验证 CLI：" + expected + "；其他版本需先完成协议验证"
	if version == expected && commandErr == nil {
		d.Compatibility = "supported"
	}
	if commandErr != nil {
		d.Compatibility = "unavailable"
		d.Detail = "CLI 无法执行或版本检查超时，请检查可执行路径和权限"
	}
	state := "unverified"
	if d.Compatibility == "supported" {
		state = "supported"
	}
	for _, name := range []string{"create", "send", "history", "resume", "approval", "question", "interrupt", "recovery"} {
		d.Capabilities[name] = state
	}
	d.Capabilities["native_goal"], d.Capabilities["dynamic_mcp"], d.Capabilities["external_tui"] = "unsupported", "unverified", "unsupported"
	// This separate policy is promoted only after its owned-process and
	// configuration-isolation contracts have passed on the target versions.
	d.Capabilities["probe"] = "unverified"
	d.Capabilities["route_evidence"] = state
	if kind == "codex" {
		// Shared-daemon credential selection/rotation still needs the T07
		// per-thread route contract; an ordinary reply cannot yet prove it.
		d.Capabilities["route_evidence"] = "unverified"
		d.Capabilities["native_goal"] = "unverified"
		d.Capabilities["external_tui"] = state
		d.Detail += "；后台协议验收版本为 0.158.0，实际连接仍需核对；共享后台凭据路由证据尚待验证"
	}
	if kind == "claude" {
		d.Detail += "；SDK 0.3.280 在 bridge 握手时独立核对"
	}
	if kind == "opencode" {
		d.Capabilities["dynamic_mcp"] = "unsupported"
	}
	return d
}
