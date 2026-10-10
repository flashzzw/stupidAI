package runtime

import "encoding/json"

type opencodeSession struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	ParentID  string `json:"parentID"`
	Metadata  struct {
		Operation string `json:"netcapOperation"`
	} `json:"metadata"`
	Model struct {
		ID       string `json:"id"`
		Provider string `json:"providerID"`
	} `json:"model"`
}

type opencodePart struct {
	EventID   string `json:"-"`
	ID        string `json:"id"`
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Text      string `json:"text"`
	Tool      string `json:"tool"`
	State     struct {
		Status   string          `json:"status"`
		Input    json.RawMessage `json:"input"`
		Output   string          `json:"output"`
		Error    string          `json:"error"`
		Metadata struct {
			Exit      *int `json:"exit"`
			Truncated bool `json:"truncated"`
		} `json:"metadata"`
	} `json:"state"`
}

type opencodeMessage struct {
	Info struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionID"`
		Role      string `json:"role"`
		ParentID  string `json:"parentID"`
		Finish    string `json:"finish"`
		// User summaries are objects; assistant compaction summaries are true.
		Summary json.RawMessage `json:"summary"`
		Error   *struct {
			Name string          `json:"name"`
			Data json.RawMessage `json:"data"`
		} `json:"error"`
		Time struct {
			Created   int64  `json:"created"`
			Completed *int64 `json:"completed"`
		} `json:"time"`
		Model struct {
			ModelID    string `json:"modelID"`
			ProviderID string `json:"providerID"`
		} `json:"model"`
		ModelID    string `json:"modelID"`
		ProviderID string `json:"providerID"`
	} `json:"info"`
	Parts []opencodePart `json:"parts"`
}

type opencodeStatus struct {
	Type string `json:"type"`
}
type opencodeRequest struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionID"`
	Permission string          `json:"permission"`
	Patterns   []string        `json:"patterns"`
	Always     []string        `json:"always"`
	Metadata   json.RawMessage `json:"metadata"`
	Tool       struct {
		MessageID string `json:"messageID"`
		CallID    string `json:"callID"`
	} `json:"tool"`
	Questions []struct {
		Text     string `json:"question"`
		Header   string `json:"header"`
		Multiple bool   `json:"multiple"`
		Custom   *bool  `json:"custom"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

type opencodePty struct {
	ID     string `json:"id"`
	PID    int    `json:"pid"`
	Status string `json:"status"`
	Cwd    string `json:"cwd"`
}
