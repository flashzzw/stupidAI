package runtime

import (
	"context"
	"fmt"
)

// SkillContext records native preparation, never model delivery or method use.
// Frames are historical; a saved configuration change need not have a new frame.
type SkillContext struct {
	NativeID      string              `json:"native_id"`
	Cwd           string              `json:"cwd"`
	Source        string              `json:"source"`
	At            string              `json:"at"`
	Complete      bool                `json:"complete"`
	RolloutSHA256 string              `json:"rollout_sha256"`
	Frames        []SkillContextFrame `json:"frames"`
}

type SkillContextFrame struct {
	InputEvidence        *SkillInputEvidence       `json:"input_evidence,omitempty"`
	TurnID               string                    `json:"turn_id"`
	RecordedAt           string                    `json:"recorded_at"`
	DescriptionsReported bool                      `json:"descriptions_reported"`
	Descriptions         []SkillContextDescription `json:"descriptions"`
	Loads                []SkillBodyLoad           `json:"loads"`
}

type SkillContextDescription struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

type SkillBodyLoad struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	ContentSHA256 string `json:"content_sha256"`
	BodySHA256    string `json:"body_sha256,omitempty"`
}

type SkillContextReader interface {
	ReadSkillContext(context.Context, Scope) (SkillContext, error)
}

func (a *Adapters) ReadSkillContext(ctx context.Context, scope Scope) (SkillContext, error) {
	r, err := a.adapter(scope.Instance.Kind)
	if err != nil {
		return SkillContext{}, err
	}
	reader, ok := r.(SkillContextReader)
	if !ok {
		return SkillContext{}, fmt.Errorf("此后端尚无原生 Skill 上下文读取入口")
	}
	return reader.ReadSkillContext(ctx, scope)
}
