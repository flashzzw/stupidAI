package app

import (
	"codex-netcap/internal/events"
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

// Persistence is an application transaction, not a generic repository. The
// seam lets failure tests prove that no runtime write precedes durable intent.
type Persistence interface {
	Load() (State, error)
	Save(State) error
	Close() error
}

type Store struct {
	db   *sql.DB
	lock *os.File
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	// Refuse a legacy directory instead of creating a competing ledger beside it.
	if _, err := os.Stat(filepath.Join(dir, "state.sqlite")); err == nil {
		return nil, fmt.Errorf("state_dir 含旧状态库；请选择独立目录")
	}
	lock, err := os.OpenFile(filepath.Join(dir, "console.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("已有控制台使用此状态目录: %w", err)
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "console.sqlite")}
	q := u.Query()
	for _, pragma := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, lock: lock}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		s.Close()
		return nil, err
	}
	if version < 0 || version > 5 {
		s.Close()
		return nil, fmt.Errorf("不支持的 console schema: %d", version)
	}
	tx, err := db.Begin()
	if err != nil {
		s.Close()
		return nil, err
	}
	defer tx.Rollback()
	for _, table := range []string{"sessions", "operations", "command_receipts", "domain_events", "observation_receipts", "route_probes", "route_health", "execution_profiles", "capability_library"} {
		if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS " + table + " (id TEXT PRIMARY KEY, body TEXT NOT NULL)"); err != nil {
			tx.Rollback()
			s.Close()
			return nil, err
		}
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS events (seq INTEGER PRIMARY KEY, body TEXT NOT NULL)",
		"CREATE TABLE IF NOT EXISTS metadata (id INTEGER PRIMARY KEY CHECK(id=1), sequence INTEGER NOT NULL)",
		"PRAGMA user_version=5",
	} {
		if _, err = tx.Exec(statement); err != nil {
			tx.Rollback()
			s.Close()
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Load() (State, error) {
	v := newState()
	for _, entry := range []struct {
		table string
		apply func([]byte) error
	}{
		{"capability_library", func(b []byte) error {
			var x CapabilityEntry
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.CapabilityLibrary[x.ID] = x
			return nil
		}},
		{"execution_profiles", func(b []byte) error {
			var x ExecutionProfile
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.ExecutionProfiles[x.Instance.ID] = x
			return nil
		}},
		{"sessions", func(b []byte) error {
			var x Session
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.Sessions[x.ID] = x
			return nil
		}},
		{"operations", func(b []byte) error {
			var x Operation
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.Operations[x.ID] = x
			return nil
		}},
		{"command_receipts", func(b []byte) error {
			var x CommandReceipt
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.Commands[x.ID] = x
			return nil
		}},
		{"events", func(b []byte) error {
			var x Event
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.Events = append(v.Events, x)
			return nil
		}},
		{"domain_events", func(b []byte) error {
			var x events.Envelope
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.DomainEvents[x.EventID] = x
			return nil
		}},
		{"observation_receipts", func(b []byte) error {
			var x ObservationReceipt
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.ObservationReceipts[x.ID] = x
			return nil
		}},
		{"route_probes", func(b []byte) error {
			var x ProbeRecord
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.Probes[x.ID] = x
			return nil
		}},
		{"route_health", func(b []byte) error {
			var x RouteHealth
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			v.RouteHealth[x.ID] = x
			return nil
		}},
	} {
		order := "id"
		if entry.table == "events" {
			order = "seq"
		}
		rows, err := s.db.Query("SELECT body FROM " + entry.table + " ORDER BY " + order)
		if err != nil {
			return v, err
		}
		for rows.Next() {
			var b []byte
			if err = rows.Scan(&b); err == nil {
				err = entry.apply(b)
			}
			if err != nil {
				rows.Close()
				return v, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return v, err
		}
	}
	err := s.db.QueryRow("SELECT sequence FROM metadata WHERE id=1").Scan(&v.Sequence)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return v, err
}

func (s *Store) Save(v State) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	put := func(table string, id any, value any) error {
		body, e := json.Marshal(value)
		if e != nil {
			return e
		}
		key := "id"
		if table == "events" {
			key = "seq"
		}
		_, e = tx.Exec("INSERT INTO "+table+"("+key+",body) VALUES(?,?) ON CONFLICT("+key+") DO UPDATE SET body=excluded.body", id, string(body))
		return e
	}
	for id, x := range v.Sessions {
		if err = put("sessions", id, x); err != nil {
			return err
		}
	}
	for id, x := range v.CapabilityLibrary {
		if err = put("capability_library", id, x); err != nil {
			return err
		}
	}
	for id, x := range v.ExecutionProfiles {
		if err = put("execution_profiles", id, x); err != nil {
			return err
		}
	}
	for id, x := range v.Operations {
		if err = put("operations", id, x); err != nil {
			return err
		}
	}
	for id, x := range v.Commands {
		if err = put("command_receipts", id, x); err != nil {
			return err
		}
	}
	for id, x := range v.DomainEvents {
		if err = put("domain_events", id, x); err != nil {
			return err
		}
	}
	for id, x := range v.ObservationReceipts {
		if err = put("observation_receipts", id, x); err != nil {
			return err
		}
	}
	for id, x := range v.Probes {
		if err = put("route_probes", id, x); err != nil {
			return err
		}
	}
	for id, x := range v.RouteHealth {
		if err = put("route_health", id, x); err != nil {
			return err
		}
	}
	for _, x := range v.Events {
		if err = put("events", x.Seq, x); err != nil {
			return err
		}
	}
	if len(v.Events) > 0 {
		if _, err = tx.Exec("DELETE FROM events WHERE seq < ?", v.Events[0].Seq); err != nil {
			return err
		}
	}
	_, err = tx.Exec("INSERT INTO metadata(id,sequence) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET sequence=excluded.sequence", v.Sequence)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error {
	err := s.db.Close()
	_ = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	return errors.Join(err, s.lock.Close())
}
