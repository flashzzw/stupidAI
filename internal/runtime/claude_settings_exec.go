package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// RunClaudeSettingsFileExec handles the bridge's private exec entry. It keeps
// the inherited file description locked across exec, so the SDK's child PID
// becomes the actual CLI and a parent crash cannot release its settings file.
// Ordinary command lines are left to the application's normal dispatcher.
func RunClaudeSettingsFileExec(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "__claude-settings-exec-v1" {
		return false, nil
	}
	fail := func() (bool, error) { return true, fmt.Errorf("派生设置使用绑定无效，未启动原生 CLI") }
	if len(args) < 3 || len(args[1]) > 8192 || !filepath.IsAbs(args[2]) {
		return fail()
	}
	var expected FileSource
	if json.Unmarshal([]byte(args[1]), &expected) != nil || expected.Kind != "file" || expected.Mode != 0600 ||
		!filepath.IsAbs(expected.Path) || filepath.Clean(expected.Path) != expected.Path || !claudeSettingsFileSHA.MatchString(expected.SHA256) {
		return fail()
	}
	id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(expected.Path), ".netcap-settings-"), ".json")
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id || filepath.Base(expected.Path) != ".netcap-settings-"+id+".json" {
		return fail()
	}
	f := os.NewFile(3, "claude-settings-consumer")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || fileIdentity(info).UID != uint32(os.Geteuid()) {
		return fail()
	}
	if err := syscall.Flock(3, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return fail()
	}
	// Validate after acquiring the use lock: a recovering parent may have
	// retired the name between the bridge's open and this child taking over.
	dir, resolved, _, err := claudeSettingsParent(filepath.Dir(expected.Path))
	if err != nil {
		return fail()
	}
	dir.Close()
	if expected.ResolvedPath != filepath.Join(resolved, filepath.Base(expected.Path)) {
		return fail()
	}
	named, err := os.Lstat(expected.Path)
	if err != nil || !named.Mode().IsRegular() || fileIdentity(named) != fileIdentity(info) || named.Mode().Perm() != 0600 {
		return fail()
	}
	actual, _, err := readClaudeSettingsFile(expected.Path, false)
	if err != nil || actual != expected {
		return fail()
	}
	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
	if err != nil {
		return fail()
	}
	if _, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, flags & ^unix.FD_CLOEXEC); err != nil {
		return fail()
	}
	if err := syscall.Exec(args[2], args[2:], os.Environ()); err != nil {
		return true, fmt.Errorf("派生设置已核对，原生 CLI 启动失败")
	}
	return true, nil
}
