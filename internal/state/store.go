//go:build unix

package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	_ "modernc.org/sqlite"
)

// Store has one writer, the Guard event loop. Each snapshot is an atomic
// transaction, so an operation's intent and its scheduling slot cannot diverge.
type Store struct {
	db   *sql.DB
	lock *os.File
	last string
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "guard.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("已有 Go 托管器使用此状态目录: %w", err)
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "state.sqlite")}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		f.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, lock: f}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS snapshots (id INTEGER PRIMARY KEY CHECK(id=1), body TEXT NOT NULL)`)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Load() (*State, error) {
	var body []byte
	err := s.db.QueryRow("SELECT body FROM snapshots WHERE id=1").Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	var v State
	if err = json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	if v.Version != 2 || v.Observed == nil {
		return nil, fmt.Errorf("旧版或损坏的状态格式；请为新版配置新的 state_dir，旧状态未修改")
	}
	s.last = string(body)
	return &v, nil
}
func (s *Store) Save(v *State) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	body := string(b)
	if body == s.last {
		return nil
	}
	_, err = s.db.Exec("INSERT INTO snapshots(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", body)
	if err == nil {
		s.last = body
	}
	return err
}
func (s *Store) Close() error {
	err := s.db.Close()
	_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	return errors.Join(err, s.lock.Close())
}
