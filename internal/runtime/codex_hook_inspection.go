package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"codex-netcap/internal/codex"
)

func (c *Codex) InspectPluginHooks(ctx context.Context, instance Instance, pkg PluginHookPackage) (out PluginHookInspection, err error) {
	if instance.Kind != "codex" || instance.ID == "" || !filepath.IsAbs(instance.ServerExecutable) {
		return out, fmt.Errorf("读取插件 hook 定义需要 Codex 实例的绝对 server_executable 路径（0.158.0）")
	}
	if _, err = hookExpectationCounts(pkg); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "netcap-codex-hook-inspection-")
	if err != nil {
		return out, fmt.Errorf("不能建立独立的 hook 定义读取目录")
	}
	defer func() {
		// Copied resources may have read-only directory modes. Relax only this
		// invocation's owned tree so it can be removed after the child exits.
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr == nil && d.IsDir() {
				return os.Chmod(p, 0700)
			}
			return walkErr
		})
		if cleanupErr := os.RemoveAll(root); cleanupErr != nil && err == nil {
			out, err = PluginHookInspection{}, fmt.Errorf("独立读取的临时资源未能完整清理")
		}
	}()
	local := Instance{Kind: "codex", Profile: filepath.Join(root, "profile"), isolatedHome: filepath.Join(root, "home")}
	cwd := filepath.Join(root, "work")
	for _, dir := range []string{local.Profile, local.isolatedHome, cwd} {
		if err = os.Mkdir(dir, 0700); err != nil {
			return out, fmt.Errorf("不能建立独立的 hook 定义读取目录")
		}
	}
	p, err := inspectionPlugin(pkg, local.Profile)
	if err != nil {
		return out, err
	}
	source, err := os.OpenRoot(pkg.SourcePath)
	if err != nil {
		return out, fmt.Errorf("插件来源不可读取")
	}
	data, err := codexPluginFiles(source, p)
	source.Close()
	if err != nil {
		return out, fmt.Errorf("插件资源与本次读取的固定文件版本不一致")
	}
	profile, err := os.OpenRoot(local.Profile)
	if err != nil {
		return out, fmt.Errorf("独立 profile 不可读取")
	}
	defer profile.Close()
	rel, _ := filepath.Rel(local.Profile, p.CachePath)
	if err = writeCodexPluginCache(profile, rel, p, data); err != nil {
		return out, fmt.Errorf("插件完整副本准备或核对失败")
	}
	config := "model=\"gpt-6-astra\"\nmodel_provider=\"inspection\"\n[features]\napps=false\nplugins=true\nhooks=true\nplugin_sharing=false\nshell_snapshot=false\n[model_providers.inspection]\nname=\"Unused metadata reader\"\nbase_url=\"http://127.0.0.1:9/v1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n[plugins.\"" + p.NativeSource + "\"]\nenabled=true\n"
	if err = os.WriteFile(filepath.Join(local.Profile, "config.toml"), []byte(config), 0600); err != nil {
		return out, fmt.Errorf("不能保存独立读取配置")
	}
	env := probeEnv(local)
	// This real repository boundary prevents parent project configuration from
	// leaking in when TMPDIR is itself located inside a user's project.
	git := inspectionCommand(ctx, env, cwd, "git", "-c", "init.templateDir=", "init", "--quiet", cwd)
	err = git.Run()
	_ = git.Cancel()
	if err != nil {
		return out, fmt.Errorf("不能建立独立读取的项目边界")
	}
	version := inspectionCommand(ctx, env, cwd, instance.ServerExecutable, "--version")
	var versionOutput limitedInspectionOutput
	version.Stdout = &versionOutput
	err = version.Run()
	_ = version.Cancel()
	if err != nil || strings.TrimSpace(versionOutput.String()) != "codex-cli 0.158.0" {
		return out, fmt.Errorf("插件 hook 定义读取需要已验证的 app-server 0.158.0")
	}
	raw, err := readInspectionHooks(ctx, env, cwd, local.Profile, instance.ServerExecutable)
	if err != nil {
		return out, err
	}
	staged, err := profile.OpenRoot(rel)
	if err != nil {
		return out, fmt.Errorf("读取后的插件副本无法核对")
	}
	_, err = codexPluginFiles(staged, p)
	staged.Close()
	if err != nil {
		return out, fmt.Errorf("读取期间插件副本已变化，未返回旧定义")
	}
	out = PluginHookInspection{Scope: "package", Source: "codex.isolated.hooks/list", InstanceID: instance.ID,
		ServerVersion: "0.158.0", NativeSource: p.NativeSource, SourceHash: pkg.SourceHash,
		At: time.Now().UTC().Format(time.RFC3339Nano), Definitions: []PluginHookDefinition{}}
	var response struct {
		Data []struct {
			Cwd      string            `json:"cwd"`
			Warnings []json.RawMessage `json:"warnings"`
			Errors   []json.RawMessage `json:"errors"`
			Hooks    []struct {
				Key, Source, Matcher string
				Path                 string `json:"sourcePath"`
				Plugin               string `json:"pluginId"`
				Event                string `json:"eventName"`
				Type                 string `json:"handlerType"`
				Hash                 string `json:"currentHash"`
				Trust                string `json:"trustStatus"`
				Timeout              *int64 `json:"timeoutSec"`
				Order                *int64 `json:"displayOrder"`
				Async                *bool  `json:"async"`
				Enabled              *bool  `json:"enabled"`
				Managed              *bool  `json:"isManaged"`
			} `json:"hooks"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Data) != 1 {
		return PluginHookInspection{}, fmt.Errorf("原生 hook 定义响应缺失或无效")
	}
	entry := response.Data[0]
	if entry.Cwd != cwd || entry.Hooks == nil || entry.Warnings == nil || entry.Errors == nil || len(entry.Warnings)+len(entry.Errors) > 0 || len(entry.Hooks) > 2048 {
		return PluginHookInspection{}, fmt.Errorf("原生 hook 读取不完整或报告来源错误；未返回部分结果")
	}
	for _, h := range entry.Hooks {
		if h.Plugin != p.NativeSource {
			continue // Host policy sources are outside this package-only result.
		}
		relative, e := filepath.Rel(p.CachePath, h.Path)
		if e != nil || !filepath.IsAbs(h.Path) || filepath.Clean(h.Path) != h.Path || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || h.Source != "plugin" || h.Trust != "untrusted" || h.Enabled == nil || !*h.Enabled || h.Managed == nil || *h.Managed || h.Timeout == nil || h.Order == nil || h.Async == nil {
			return PluginHookInspection{}, fmt.Errorf("原生 hook 元数据不属于独立读取的包定义")
		}
		out.Definitions = append(out.Definitions, PluginHookDefinition{Key: h.Key, Path: filepath.ToSlash(relative), Event: h.Event,
			Type: h.Type, Matcher: h.Matcher, DefinitionHash: h.Hash, TimeoutSeconds: *h.Timeout, DisplayOrder: *h.Order, Background: *h.Async})
	}
	if err = ValidatePluginHookInspection(instance, pkg, out); err != nil {
		return PluginHookInspection{}, err
	}
	return out, nil
}

type limitedInspectionOutput struct{ buffer bytes.Buffer }

func (b *limitedInspectionOutput) String() string { return b.buffer.String() }

func (b *limitedInspectionOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 4096 {
		return 0, fmt.Errorf("版本输出超过限制")
	}
	return b.buffer.Write(p)
}

func inspectionCommand(ctx context.Context, env []string, cwd, executable string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env, cmd.Dir, cmd.Stderr = env, cwd, io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	return cmd
}

// This transport intentionally has no general call/approval surface. It sends
// only initialize, initialized and hooks/list, then drains and closes its own
// process. A malformed response, server request or execution event aborts it.
func readInspectionHooks(ctx context.Context, env []string, cwd, profile, executable string) (json.RawMessage, error) {
	cmd := inspectionCommand(ctx, env, cwd, executable, "app-server", "--stdio")
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("不能建立元数据输入通道")
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("不能建立元数据输出通道")
	}
	defer output.Close()
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("不能启动独立 hook 定义读取进程")
	}
	defer func() {
		// A normally exited parent may leave children that closed their stdio.
		// The process group belongs to this read even after its leader exits.
		_ = cmd.Cancel()
		if cmd.ProcessState == nil {
			_ = cmd.Wait()
		}
	}()
	limited := &io.LimitedReader{R: output, N: (16 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 32768), 4<<20)
	encoder := json.NewEncoder(input)
	count := 0
	read := func(id string) (json.RawMessage, error) {
		for scanner.Scan() {
			count++
			var message codex.Message
			if count > 512 || limited.N <= 0 || json.Unmarshal(scanner.Bytes(), &message) != nil || message.Error != nil || len(message.ID) > 0 && message.Method != "" || len(message.ID) == 0 && message.Method == "" || strings.HasPrefix(message.Method, "hook/") || strings.HasPrefix(message.Method, "turn/") || strings.HasPrefix(message.Method, "item/") || strings.HasPrefix(message.Method, "thread/") {
				return nil, fmt.Errorf("原生元数据协议出现错误或意外执行请求")
			}
			if len(message.ID) > 0 {
				if string(message.ID) != id || len(message.Result) == 0 {
					return nil, fmt.Errorf("原生元数据回执不匹配")
				}
				return message.Result, nil
			}
		}
		return nil, fmt.Errorf("原生元数据读取中断、超时或超过限制")
	}
	if err = encoder.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "netcap-hook-inspection", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}}); err != nil {
		return nil, fmt.Errorf("不能初始化原生元数据读取")
	}
	initialized, err := read("1")
	var identity struct {
		CodexHome string `json:"codexHome"`
		UserAgent string `json:"userAgent"`
	}
	if err != nil || json.Unmarshal(initialized, &identity) != nil || identity.CodexHome != profile || !strings.HasPrefix(identity.UserAgent, "netcap-hook-inspection/0.158.0 ") {
		return nil, fmt.Errorf("独立读取进程的 profile 无法核对")
	}
	if encoder.Encode(map[string]string{"method": "initialized"}) != nil || encoder.Encode(map[string]any{"id": 2, "method": "hooks/list", "params": map[string]any{"cwds": []string{cwd}}}) != nil {
		return nil, fmt.Errorf("不能请求原生 hook 定义")
	}
	result, err := read("2")
	if err != nil {
		return nil, err
	}
	_ = input.Close()
	// Drain after closing input so a large final notification cannot deadlock
	// shutdown. Any extra response is rejected; notifications remain bounded.
	for scanner.Scan() {
		count++
		var message codex.Message
		if count > 512 || limited.N <= 0 || json.Unmarshal(scanner.Bytes(), &message) != nil || len(message.ID) > 0 || message.Method == "" || message.Error != nil || strings.HasPrefix(message.Method, "hook/") || strings.HasPrefix(message.Method, "turn/") || strings.HasPrefix(message.Method, "item/") || strings.HasPrefix(message.Method, "thread/") {
			return nil, fmt.Errorf("元数据读取结束时出现意外原生事件")
		}
	}
	if scanner.Err() != nil || limited.N <= 0 || cmd.Wait() != nil || ctx.Err() != nil {
		return nil, fmt.Errorf("独立元数据进程未正常完成或已经超时")
	}
	return result, nil
}
