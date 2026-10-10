package runtime

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type codexExecutorInspector interface {
	ExecutorEvidence(context.Context, Instance) (*ExecutorEvidence, error)
}

func (p *verifiedCodexConnection) ExecutorEvidence(ctx context.Context, instance Instance) (*ExecutorEvidence, error) {
	if err := p.peer.verifyNamespace(ctx, instance); err != nil {
		return nil, err
	}
	data, err := p.peer.readEnvironment(ctx, instance.Socket)
	if err != nil {
		return nil, err
	}
	image, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(p.peer.pid), "exe"))
	if err != nil {
		return nil, fmt.Errorf("无法核对本次连接的后台运行映像")
	}
	if err := p.peer.verify(instance.Socket); err != nil {
		return nil, err
	}
	// openCodexPeer accepts only peers with the current effective UID. Resolve
	// its local account name from that UID, never from the process's USER value.
	uid := strconv.Itoa(os.Geteuid())
	out := &ExecutorEvidence{Source: "codex.unix_peer", At: time.Now().UTC().Format(time.RFC3339Nano), PID: p.peer.pid, Birth: p.peer.birth, OS: "linux", UID: uid, Executable: image,
		Detail: "本次接入后台的进程与本地用户；不证明原始创建环境、当前输入独占或原终端草稿。"}
	if account, err := user.LookupId(uid); err == nil {
		out.User = account.Username
	}
	selected, envErr := parseCodexEnvironment(data, "WSL_DISTRO_NAME")
	if envErr == nil {
		value := selected["WSL_DISTRO_NAME"]
		if len(value) <= 256 && !strings.ContainsAny(value, "\r\n\x00") {
			out.WSLDistro = value
		}
	}
	return out, nil
}
