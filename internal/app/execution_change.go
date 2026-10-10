package app

import (
	"fmt"
	"slices"
	"strings"

	"codex-netcap/internal/runtime"
	"github.com/google/uuid"
)

// A saved change is a proposal, not an effective execution version. Ordinary
// observation/reconnect must keep using Session.ExecutionSnapshot and Scope.
type ExecutionChange struct {
	RecoverySnapshot               *ExecutionSnapshot            `json:"recovery_snapshot,omitempty"`
	InstalledPluginChange          bool                          `json:"installed_plugin_change,omitempty"`
	AdoptCodexConfigurationSources bool                          `json:"adopt_codex_configuration_sources,omitempty"`
	CodexConfigurationChange       *CodexConfigurationChange     `json:"codex_configuration_change,omitempty"`
	HookSourceProof                *runtime.CodexHookSourceProof `json:"hook_source_proof,omitempty"`
	AdoptCodexHookSources          bool                          `json:"adopt_codex_hook_sources,omitempty"`
	CodexHookSourceChanges         []CodexHookSourceChange       `json:"codex_hook_source_changes,omitempty"`
	FailureDetail                  string                        `json:"failure_detail,omitempty"`
	AdoptLocalSettings             bool                          `json:"adopt_local_settings,omitempty"`
	LocalSettingsChanges           []LocalSettingsChange         `json:"local_settings_changes,omitempty"`
	ID                             string                        `json:"id"`
	CommandID                      string                        `json:"command_id"`
	BaseSnapshotID                 string                        `json:"base_snapshot_id"`
	Status                         string                        `json:"status"`
	Snapshot                       *ExecutionSnapshot            `json:"snapshot"`
	Changes                        []CapabilitySelectionChange   `json:"changes"`
	Boundary                       string                        `json:"boundary"`
	OperationID                    string                        `json:"operation_id,omitempty"`
	Baseline                       string                        `json:"baseline,omitempty"`
	RecoveryOperationID            string                        `json:"recovery_operation_id,omitempty"`
	Failure                        *runtime.ConfigurationFailure `json:"failure,omitempty"`
}

type CapabilitySelectionChange struct {
	Kind            string `json:"kind,omitempty"`
	Name            string `json:"name,omitempty"`
	SourceKey       string `json:"source_key,omitempty"`
	AssetID         string `json:"asset_id"`
	Server          string `json:"server"`
	Before          string `json:"before"`
	After           string `json:"after"`
	BeforeVersion   string `json:"before_version,omitempty"`
	AfterVersion    string `json:"after_version,omitempty"`
	BeforeHookTrust string `json:"before_hook_trust,omitempty"`
	AfterHookTrust  string `json:"after_hook_trust,omitempty"`
}

type ExecutionChangeRequest struct {
	AdoptCodexConfigurationSources bool                  `json:"adopt_codex_configuration_sources,omitempty"`
	AdoptCodexHookSources          bool                  `json:"adopt_codex_hook_sources,omitempty"`
	AdoptLocalSettings             bool                  `json:"adopt_local_settings,omitempty"`
	BaseSnapshotID                 string                `json:"base_snapshot_id"`
	PendingChangeID                string                `json:"pending_change_id"`
	Selections                     []CapabilitySelection `json:"selections"`
	PreviewHash                    string                `json:"preview_hash,omitempty"`
}

type ExecutionChangePreview struct {
	CodexConfigurationChange *CodexConfigurationChange `json:"codex_configuration_change,omitempty"`
	hookSourceProof          *runtime.CodexHookSourceProof
	CodexHookSourceChanges   []CodexHookSourceChange     `json:"codex_hook_source_changes,omitempty"`
	LocalSettingsChanges     []LocalSettingsChange       `json:"local_settings_changes,omitempty"`
	BaseSnapshotID           string                      `json:"base_snapshot_id"`
	PendingChangeID          string                      `json:"pending_change_id"`
	PreviewHash              string                      `json:"preview_hash"`
	NextRevision             int                         `json:"next_revision"`
	Changes                  []CapabilitySelectionChange `json:"changes"`
	Boundary                 string                      `json:"boundary"`
}

const mcpChangeBoundary = "当前版本的已加载 Codex 会话不能通过 resume 热切换 MCP。先准备应用并保持暂停，待原线程卸载后明确应用；工作台核对原 ID 和实际新选择，不会停止共享后台。保存仅登记待应用。Backend、供应商、状态归属变更需新尝试与交接。"
const claudeMCPChangeBoundary = "保存仅登记待应用。准备应用核对原历史并保持暂停；明确应用时关闭已确认空闲的自有 Claude 查询，再按原 ID 加载新选择并核对实际清单。其他会话继续运行，不重放模型输入。Backend、供应商、状态归属变更需新尝试与交接。"
const codexPluginChangeBoundary = "保存仅登记候选版本。准备应用只读核对原历史与空闲状态并保持暂停；明确应用时取消当前连接的原线程订阅，再按原 ID 加载新选择并核对已撤下来源。其他线程继续运行，不关闭共享后台，不重放模型输入。"

func checkChangeBase(session Session, request ExecutionChangeRequest) error {
	if session.Origin != "console" || session.ControlMode == "observed" || (session.Scope.Instance.Kind != "codex" && session.Scope.Instance.Kind != "claude") || session.ExecutionSnapshot == nil || session.Scope.NativeID == "" {
		return fmt.Errorf("改配需要已有启动快照和原生身份的普通 Codex/Claude 会话")
	}
	if request.BaseSnapshotID == "" || request.BaseSnapshotID != session.ExecutionSnapshot.ID {
		return fmt.Errorf("当前生效快照已变化，请重新预览")
	}
	id := ""
	if session.PendingExecutionChange != nil {
		if session.PendingExecutionChange.Status != "pending" {
			return fmt.Errorf("改配操作尚待核对，不能替换或撤销")
		}
		id = session.PendingExecutionChange.ID
	}
	if request.PendingChangeID != id {
		return fmt.Errorf("待应用选择已变化，请刷新后重新预览")
	}
	return nil
}

func (a *Application) prepareExecutionChange(session Session, request ExecutionChangeRequest) (ExecutionChangePreview, *ExecutionSnapshot, error) {
	preview := ExecutionChangePreview{BaseSnapshotID: request.BaseSnapshotID, PendingChangeID: request.PendingChangeID, Changes: []CapabilitySelectionChange{}, Boundary: mcpChangeBoundary}
	if session.Scope.Instance.Kind == "claude" {
		preview.Boundary = claudeMCPChangeBoundary
	}
	if err := checkChangeBase(session, request); err != nil {
		return preview, nil, err
	}
	if session.PendingExecutionChange != nil && session.PendingExecutionChange.InstalledPluginChange {
		return preview, nil, fmt.Errorf("请先应用或撤销已安装插件候选，再调整其他选择")
	}
	if err := checkApplicableExecutionCandidate(session.PendingExecutionChange); err != nil {
		return preview, nil, fmt.Errorf("请先撤销当前待应用方案再调整其他选择：%w", err)
	}
	if err := verifyExecutionBinding(session); err != nil {
		return preview, nil, err
	}
	verifyRoute := a.verifyRouteIdentity
	if request.AdoptCodexConfigurationSources {
		verifyRoute = a.verifyConfigurationAdoptionRoute
	}
	if err := verifyRoute(session); err != nil {
		return preview, nil, err
	}
	if request.AdoptCodexConfigurationSources && (request.AdoptCodexHookSources || request.AdoptLocalSettings || session.Scope.Instance.Kind != "codex") {
		return preview, nil, fmt.Errorf("配置来源采纳不能同时采纳 hook 文件或 Claude 本地设置")
	}
	if (len(request.Selections) == 0 && !request.AdoptLocalSettings && !request.adoptsCodexSources()) || len(request.Selections) > 16 {
		return preview, nil, fmt.Errorf("需要 1–16 项能力选择")
	}
	pluginChange := a.codexPluginChange(session, request.Selections)
	if pluginChange || request.adoptsCodexSources() {
		preview.Boundary = codexPluginChangeBoundary
	}
	candidate := queryCopy(session.ExecutionSnapshot)
	if session.Scope.Instance.Kind == "claude" || pluginChange {
		var err error
		candidate, preview.Changes, err = a.resourceExecutionCandidate(session, request.Selections)
		if err != nil {
			return preview, nil, err
		}
	} else {
		seen := map[string]bool{}
		for _, pick := range request.Selections {
			if seen[pick.AssetID] || pick.Version != "" || (pick.Mode != "enabled" && pick.Mode != "disabled") {
				return preview, nil, fmt.Errorf("MCP 选择重复或模式无效")
			}
			seen[pick.AssetID] = true
			index := slices.IndexFunc(candidate.MCPSelections, func(s runtime.MCPSelection) bool { return s.AssetID == pick.AssetID })
			if index < 0 {
				return preview, nil, fmt.Errorf("首片只能调整原快照已选择的 MCP；新增能力和继承切换尚未接入")
			}
			if !slices.ContainsFunc(candidate.MCPSelections, func(s runtime.MCPSelection) bool { return s.AssetID == pick.AssetID && s.Mode != pick.Mode }) {
				continue
			}
			// Enabling remains subject to the current registry, but disabling a
			// previously selected asset is possible even after its entry is removed.
			if pick.Mode == "enabled" {
				asset, ok := a.asset(pick.AssetID)
				ref := slices.IndexFunc(candidate.CapabilityRefs, func(r CapabilityReference) bool { return r.Asset.ID == pick.AssetID })
				if !ok || asset.Disabled || ref < 0 || digest(asset) != digest(candidate.CapabilityRefs[ref].Asset) {
					return preview, nil, fmt.Errorf("能力 %s 已停用、移除或登记版本变化，不能以旧版本重新启用", pick.AssetID)
				}
			}
			for index, current := range candidate.MCPSelections {
				if current.AssetID == pick.AssetID && current.Mode != pick.Mode {
					preview.Changes = append(preview.Changes, CapabilitySelectionChange{AssetID: pick.AssetID, Server: current.Server, Before: current.Mode, After: pick.Mode})
					candidate.MCPSelections[index].Mode = pick.Mode
				}
			}
		}
	}
	if request.AdoptLocalSettings {
		var err error
		preview.LocalSettingsChanges, err = captureLocalSettingsAdoption(session, candidate)
		if err != nil {
			return preview, nil, err
		}
		preview.Boundary += " 本次还将采纳列出的当前本地文件版本；原配置与旧历史保留，候选启动失败或结果未知时保持暂停并核对，不自动回滚或重放。"
	}
	if request.AdoptCodexHookSources {
		var err error
		preview.CodexHookSourceChanges, err = a.captureCodexHookSourceAdoption(session, candidate)
		if err != nil {
			return preview, nil, err
		}
		preview.hookSourceProof, err = a.captureCodexHookSourceProof(session, candidate)
		if err != nil {
			return preview, nil, err
		}
		preview.Boundary += " 本次采纳列出的用户/项目 hook 文件与来源关系；保留原生信任配置，不自动信任新 hash。原连接或已保存的原配置依据无法核对时拒绝；重开后还需确认原线程未加载。失败或丢回执保持未知，不自动回滚。"
	}
	if request.AdoptCodexConfigurationSources {
		var err error
		preview.CodexConfigurationChange, err = a.captureCodexConfigurationAdoption(session, candidate)
		if err != nil {
			return preview, nil, err
		}
		preview.hookSourceProof, err = a.captureCodexHookSourceProof(session, candidate)
		if err != nil {
			return preview, nil, err
		}
		preview.Boundary += " 本次明确采纳列出的配置来源版本；仅已记录的非空 reasoning effort 可改变。准备保持暂停，明确应用经原 ID 退订、显式覆盖及回读后才晋升；回执丢失保持未知。恢复需要原文件还原及原线程未加载。"
	}
	if len(preview.Changes) == 0 && len(preview.LocalSettingsChanges) == 0 && len(preview.CodexHookSourceChanges) == 0 && preview.CodexConfigurationChange == nil {
		return preview, nil, fmt.Errorf("选择与当前生效快照相同，没有待应用差异")
	}
	if err := verifyExecutionChangeSources(session, candidate); err != nil {
		return preview, nil, err
	}
	slices.SortFunc(preview.Changes, func(a, b CapabilitySelectionChange) int {
		return strings.Compare(a.AssetID+"/"+a.Server, b.AssetID+"/"+b.Server)
	})
	candidate.RestoredFrom = ""
	candidate.PreviousID = session.ExecutionSnapshot.ID
	candidate.Revision = max(1, session.ExecutionSnapshot.Revision) + 1
	candidate.ID, candidate.CreatedAt = "", ""
	preview.NextRevision = candidate.Revision
	preview.PreviewHash = digest(candidate)
	return preview, candidate, nil
}

// Preview has no state/native side effects and follows the same validation as
// save. The hash and both base IDs prevent stale browser tabs overwriting a plan.
func (a *Application) PreviewExecutionChange(id string, request ExecutionChangeRequest) (ExecutionChangePreview, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.state.Sessions[id]
	if !ok {
		return ExecutionChangePreview{}, fmt.Errorf("会话不存在")
	}
	preview, _, err := a.prepareExecutionChange(session, request)
	return preview, err
}

func (a *Application) changeExecution(command Command, hash string) (CommandReceipt, error) {
	receipt := CommandReceipt{ID: command.ID, Hash: hash, SessionID: command.SessionID}
	allowed := Command{ID: command.ID, Kind: command.Kind, SessionID: command.SessionID, ExecutionChange: command.ExecutionChange}
	if command.ExecutionChange == nil || digest(command) != digest(allowed) {
		return receipt, fmt.Errorf("改配命令只接受原会话的 execution_change；其他执行绑定需新尝试与交接")
	}
	next := clone(a.state)
	session, ok := next.Sessions[command.SessionID]
	if !ok {
		return receipt, fmt.Errorf("会话不存在")
	}
	request := *command.ExecutionChange
	if err := checkChangeBase(session, request); err != nil {
		return receipt, err
	}
	if command.Kind == "prepare_execution" || command.Kind == "apply_execution" {
		return a.startExecutionChange(next, session, command, receipt)
	}
	if command.Kind == "discard_execution" {
		if session.PendingExecutionChange == nil || len(request.Selections) > 0 || request.PreviewHash != "" || request.AdoptLocalSettings || request.adoptsCodexSources() {
			return receipt, fmt.Errorf("没有匹配的待应用选择，或撤销命令含多余选择")
		}
		return a.persistExecutionChange(next, session, receipt, nil)
	} else {
		preview, candidate, err := a.prepareExecutionChange(session, request)
		if err != nil {
			return receipt, err
		}
		if request.PreviewHash == "" || request.PreviewHash != preview.PreviewHash {
			return receipt, fmt.Errorf("改配预览不匹配，请重新预览后保存")
		}
		change := &ExecutionChange{BaseSnapshotID: preview.BaseSnapshotID,
			Status: "pending", Snapshot: candidate, Changes: preview.Changes, Boundary: preview.Boundary, AdoptLocalSettings: request.AdoptLocalSettings, LocalSettingsChanges: preview.LocalSettingsChanges, AdoptCodexHookSources: request.AdoptCodexHookSources, CodexHookSourceChanges: preview.CodexHookSourceChanges, HookSourceProof: preview.hookSourceProof, AdoptCodexConfigurationSources: request.AdoptCodexConfigurationSources, CodexConfigurationChange: preview.CodexConfigurationChange}
		return a.persistExecutionChange(next, session, receipt, change)
	}
}

// Both ordinary selections and native settings observations use one durable
// candidate/receipt transaction. No native I/O happens while committing it.
func (a *Application) persistExecutionChange(next State, session Session, receipt CommandReceipt, change *ExecutionChange) (CommandReceipt, error) {
	if change == nil {
		session.PendingExecutionChange = nil
		session.ExecutionHold = false
		receipt.Status = "discarded"
	} else {
		change.ID, change.CommandID, change.Status = uuid.NewString(), receipt.ID, "pending"
		change.BaseSnapshotID = session.ExecutionSnapshot.ID
		change.Snapshot.ID, change.Snapshot.CreatedAt = uuid.NewString(), now()
		change.Snapshot.PreviousID = session.ExecutionSnapshot.ID
		change.Snapshot.Revision = max(1, session.ExecutionSnapshot.Revision) + 1
		if err := planInstalledSettingsArtifact(session.Scope, change.Snapshot); err != nil {
			return receipt, err
		}
		session.PendingExecutionChange = change
		receipt.Status, receipt.PendingChangeID = "pending", change.ID
	}
	// Do not change Generation, status, pause, or scope: an in-flight operation
	// still owns its original execution and must be allowed to settle normally.
	session.UpdatedAt = now()
	next.Sessions[session.ID], next.Commands[receipt.ID] = session, receipt
	if err := a.commit(next, "execution."+receipt.Status, session.ID); err != nil {
		return CommandReceipt{}, err
	}
	a.startInstalledSettingsRetirement()
	return receipt, nil
}
