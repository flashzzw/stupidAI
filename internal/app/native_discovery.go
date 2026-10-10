package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

type NativeDiscoveryRequest struct {
	ProjectID  string `json:"project_id"`
	InstanceID string `json:"instance_id"`
	Cursor     string `json:"cursor,omitempty"`
	Archived   bool   `json:"archived,omitempty"`
}

type NativePreviewRequest struct {
	ProjectID  string `json:"project_id"`
	InstanceID string `json:"instance_id"`
	NativeID   string `json:"native_id"`
}

type DiscoveredSession struct {
	runtime.NativeSession
	KnownSessions []string `json:"known_sessions"`
	Detail        string   `json:"detail"`
}

type NativeDiscoveryResult struct {
	Context        SessionContext      `json:"context"`
	BindingToken   string              `json:"binding_token,omitempty"`
	ReadOnly       bool                `json:"read_only"`
	InstanceID     string              `json:"instance_id"`
	Kind           string              `json:"kind"`
	Cwd            string              `json:"cwd"`
	StateNamespace string              `json:"state_namespace"`
	At             string              `json:"at"`
	Sessions       []DiscoveredSession `json:"sessions"`
	Next           string              `json:"next_cursor,omitempty"`
	Skipped        int                 `json:"skipped"`
	Detail         string              `json:"detail"`
}

type discoveryCursor struct{ Binding, Native string }

func (a *Application) discoveryBinding(request NativeDiscoveryRequest) (runtime.DiscoveryRequest, string, string, error) {
	var native runtime.DiscoveryRequest
	if a.closed {
		return native, "", "", fmt.Errorf("工作台已关闭")
	}
	for _, project := range a.config.Projects {
		if project.ID == request.ProjectID {
			native.Cwd = project.Cwd
		}
	}
	for _, instance := range a.config.Instances {
		if instance.ID == request.InstanceID {
			native.Instance = instance
		}
	}
	if native.Cwd == "" || native.Instance.ID == "" {
		return native, "", "", fmt.Errorf("请选择已登记的项目与执行配置")
	}
	if native.Instance.Kind != "codex" && native.Instance.Kind != "claude" {
		return native, "", "", fmt.Errorf("此执行后端尚未接入原生发现")
	}
	if err := a.requireNativeHistory(native.Instance.ID); err != nil {
		return native, "", "", err
	}
	resolved, err := filepath.EvalSymlinks(native.Cwd)
	if err != nil || resolved != native.Cwd {
		return native, "", "", fmt.Errorf("登记项目目录已变化，请重新核对")
	}
	namespace, err := runtime.StateNamespace(runtime.Scope{Instance: native.Instance})
	if err != nil {
		return native, "", "", err
	}
	fingerprint, err := nativeReadFingerprint(native.Instance)
	if err != nil {
		return native, "", "", err
	}
	native.Limit, native.Archived = 50, request.Archived
	binding := digest(struct {
		Request                runtime.DiscoveryRequest
		ProjectID              string
		Namespace, Fingerprint string
	}{native, request.ProjectID, namespace, fingerprint})
	if request.Cursor != "" {
		if len(request.Cursor) > 8192 {
			return native, "", "", fmt.Errorf("发现游标过长")
		}
		var cursor discoveryCursor
		data, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Binding != binding || cursor.Native == "" || len(cursor.Native) > 4096 {
			return native, "", "", fmt.Errorf("发现游标不属于当前配置/项目/筛选，或绑定已经变化")
		}
		native.Cursor = cursor.Native
	}
	return native, binding, namespace, nil
}

func (a *Application) DiscoverNative(ctx context.Context, request NativeDiscoveryRequest) (NativeDiscoveryResult, error) {
	return a.readNativeMetadata(ctx, request, "")
}

func (a *Application) PreviewNative(ctx context.Context, request NativePreviewRequest) (NativeDiscoveryResult, error) {
	if _, err := uuid.Parse(request.NativeID); err != nil {
		return NativeDiscoveryResult{}, fmt.Errorf("原生会话 ID 必须为 UUID")
	}
	return a.readNativeMetadata(ctx, NativeDiscoveryRequest{ProjectID: request.ProjectID, InstanceID: request.InstanceID}, request.NativeID)
}

func (a *Application) readNativeMetadata(ctx context.Context, request NativeDiscoveryRequest, id string) (NativeDiscoveryResult, error) {
	a.mu.Lock()
	native, binding, namespace, err := a.discoveryBinding(request)
	a.mu.Unlock()
	if err != nil {
		return NativeDiscoveryResult{}, err
	}
	discovery, ok := a.runtime.(runtime.NativeDiscovery)
	if !ok {
		return NativeDiscoveryResult{}, fmt.Errorf("运行适配器没有原生发现接口")
	}
	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var page runtime.DiscoveryPage
	if id == "" {
		page, err = discovery.Discover(check, native)
	} else {
		var record runtime.NativeSession
		record, err = discovery.InspectNative(check, native, id)
		page.Sessions = []runtime.NativeSession{record}
	}
	if err != nil {
		return NativeDiscoveryResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, after, _, err := a.discoveryBinding(request)
	if err != nil || after != binding {
		return NativeDiscoveryResult{}, fmt.Errorf("发现期间项目或执行配置已变化，请重新查询")
	}
	if len(page.Sessions) > native.Limit || len(page.Next) > 4096 || page.Next != "" && page.Next == native.Cursor {
		return NativeDiscoveryResult{}, fmt.Errorf("原生列表超过分页限制或游标未推进")
	}
	out := NativeDiscoveryResult{ReadOnly: true, InstanceID: native.Instance.ID, Kind: native.Instance.Kind, Cwd: native.Cwd, StateNamespace: namespace,
		At: now(), Sessions: []DiscoveredSession{}, Detail: "本次仅发现/读取原生元数据，不读取完整对话、不加载会话、不创建执行、不取得输入权。原生创建来源不能证明当前独立前台/后台可接管。"}
	out.Context = a.sessionContext(Session{Origin: "native", Permission: "unverified", Scope: runtime.Scope{Instance: native.Instance, Cwd: native.Cwd, StateNamespace: namespace}})
	out.Context.Basis = "selected_configuration"
	seen := map[string]bool{}
	for _, record := range page.Sessions {
		cwd, cwdErr := filepath.EvalSymlinks(record.Cwd)
		_, idErr := uuid.Parse(record.NativeID)
		if cwdErr != nil || cwd != native.Cwd || idErr != nil || id != "" && record.NativeID != id {
			if id != "" {
				return NativeDiscoveryResult{}, fmt.Errorf("原生记录的身份或工作目录不属于所选项目")
			}
			out.Skipped++
			continue
		}
		if seen[record.NativeID] {
			return NativeDiscoveryResult{}, fmt.Errorf("原生列表出现重复会话 ID，请重新核对")
		}
		seen[record.NativeID] = true
		record.Cwd = cwd
		if title := []rune(record.Title); len(title) > 240 {
			record.Title = string(title[:240])
		}
		row := DiscoveredSession{NativeSession: record, KnownSessions: []string{}, Detail: "可另行读取原生接口可见历史；控制与恢复仍需对应接入路径验证，本页不能转为可发送会话。"}
		for _, session := range a.state.Sessions {
			if session.Scope.Instance.Kind == native.Instance.Kind && session.Scope.NativeID == record.NativeID && session.Scope.StateNamespace == namespace && session.Scope.Cwd == cwd {
				row.KnownSessions = append(row.KnownSessions, session.ID)
			}
		}
		sort.Strings(row.KnownSessions)
		out.Sessions = append(out.Sessions, row)
	}
	if page.Next != "" {
		data, _ := json.Marshal(discoveryCursor{Binding: binding, Native: page.Next})
		out.Next = base64.RawURLEncoding.EncodeToString(data)
	}
	if id != "" && len(out.Sessions) == 1 {
		out.BindingToken = digest([]any{binding, out.Sessions[0].NativeSession})
	}
	return out, nil
}
