package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type opencodePTYLease struct {
	ID    string `json:"id"`
	PID   int    `json:"pid"`
	Birth string `json:"birth"`
	Group int    `json:"group"`
}

// Native PTYs can outlive their server (e.g. a job ignoring terminal hangup).
// Their registry is memory-only, so persist the observed process identity and
// retain its reservation until the original process group has actually exited.
func (s *opencodeServer) reconcilePTYs(ptys []opencodePty) ([]Execution, error) {
	path := filepath.Join(s.root, "pty-leases.json")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var previous []opencodePTYLease
	if len(b) > 0 && json.Unmarshal(b, &previous) != nil {
		return nil, fmt.Errorf("OpenCode PTY 所有权记录损坏")
	}
	next := []opencodePTYLease{}
	present := map[string]bool{}
	for _, pty := range ptys {
		if pty.Status != "running" {
			continue
		}
		birth := processBirth(pty.PID)
		group, err := syscall.Getpgid(pty.PID)
		if err != nil || birth == "" || birth != processBirth(pty.PID) || group <= 1 {
			return nil, fmt.Errorf("OpenCode PTY %s 进程身份尚待核对", pty.ID)
		}
		present[pty.ID] = true
		next = append(next, opencodePTYLease{ID: pty.ID, PID: pty.PID, Birth: birth, Group: group})
	}
	retained := []Execution{}
	for _, lease := range previous {
		if present[lease.ID] {
			continue
		}
		if lease.ID == "" || lease.PID <= 1 || lease.Group <= 1 || lease.Birth == "" {
			return nil, fmt.Errorf("OpenCode PTY 所有权证据不完整")
		}
		alive, err := opencodeGroupAlive(lease.Group)
		if err != nil {
			return nil, err
		}
		if alive {
			next = append(next, lease)
			retained = append(retained, Execution{ID: lease.ID, Kind: "server_pty", Status: "unknown"})
		}
	}
	if len(b) == 0 && len(next) == 0 {
		return retained, nil
	}
	encoded, _ := json.Marshal(next)
	if !bytes.Equal(encoded, b) {
		if err := writeOpenCodeRecord(path, encoded); err != nil {
			return nil, err
		}
	}
	return retained, nil
}

func opencodeGroupAlive(group int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("PTY 进程组核对不完整")
		}
		end := strings.LastIndex(string(b), ") ")
		if end < 0 {
			return false, fmt.Errorf("PTY 进程状态不完整")
		}
		fields := strings.Fields(string(b)[end+2:])
		if len(fields) < 3 {
			return false, fmt.Errorf("PTY 进程状态不完整")
		}
		if fields[2] == strconv.Itoa(group) && fields[0] != "Z" {
			return true, nil
		}
	}
	return false, nil
}

func writeOpenCodeRecord(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
