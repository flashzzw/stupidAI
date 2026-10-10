"""托管器自己的状态。Codex 会话与 Goal 的真实状态仍以 App Server 为准。"""

from __future__ import annotations

import fcntl
import hashlib
import json
import os
import re
import sqlite3
import time
from pathlib import Path

from .config import Config

SCHEMA = '''
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY, ord INTEGER NOT NULL, source TEXT NOT NULL,
    snapshot TEXT, sha256 TEXT, cwd TEXT NOT NULL, budget INTEGER,
    thread_id TEXT, batch TEXT, status TEXT NOT NULL DEFAULT 'pending',
    goal_status TEXT, pause_reason TEXT, paused_version INTEGER,
    tries INTEGER NOT NULL DEFAULT 0, updated_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS watches (
    thread_id TEXT PRIMARY KEY, cwd TEXT, enabled INTEGER NOT NULL DEFAULT 1,
    initialized INTEGER NOT NULL DEFAULT 0, last_human REAL NOT NULL,
    last_completed REAL NOT NULL, last_auto REAL NOT NULL DEFAULT 0,
    activity_version INTEGER NOT NULL DEFAULT 0, updated_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS sends (
    id TEXT PRIMARY KEY, thread_id TEXT NOT NULL, kind TEXT NOT NULL,
    prompt TEXT NOT NULL, status TEXT NOT NULL, queue_id TEXT, turn_id TEXT,
    activity_version INTEGER NOT NULL, created_at REAL NOT NULL,
    updated_at REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS sends_by_thread ON sends(thread_id, status);
CREATE TABLE IF NOT EXISTS seen_items (
    thread_id TEXT NOT NULL, item_id TEXT NOT NULL,
    PRIMARY KEY (thread_id, item_id)
);
CREATE TABLE IF NOT EXISTS event_log (
    at REAL NOT NULL, topic TEXT NOT NULL, detail TEXT NOT NULL
);
'''
SECRET = re.compile(r'(?i)(?:Bearer\s+|api[_-]?key\s*[=:]\s*|sk-)[A-Za-z0-9._-]{8,}')


class State:
    def __init__(self, config: Config):
        self.config = config
        config.state_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(config.state_dir, 0o700)
        self.conn = sqlite3.connect(config.state_dir / 'guard.sqlite3', timeout=10,
                                    isolation_level=None)
        self.conn.row_factory = sqlite3.Row
        self.conn.executescript(SCHEMA)
        os.chmod(config.state_dir / 'guard.sqlite3', 0o600)
        self.lock_file = None

    def acquire_lock(self) -> None:
        self.lock_file = (self.config.state_dir / 'guard.lock').open('a+')
        try:
            fcntl.flock(self.lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            self.lock_file.close()
            self.lock_file = None
            raise RuntimeError('同一 Codex 配置目录已有托管器在运行') from exc
        self.lock_file.seek(0)
        self.lock_file.truncate()
        self.lock_file.write(str(os.getpid()))
        self.lock_file.flush()

    def close(self) -> None:
        self.conn.close()
        if self.lock_file:
            fcntl.flock(self.lock_file, fcntl.LOCK_UN)
            self.lock_file.close()

    def execute(self, sql: str, params: tuple = ()) -> sqlite3.Cursor:
        return self.conn.execute(sql, params)

    def one(self, sql: str, params: tuple = ()) -> dict | None:
        row = self.execute(sql, params).fetchone()
        return dict(row) if row else None

    def all(self, sql: str, params: tuple = ()) -> list[dict]:
        return [dict(row) for row in self.execute(sql, params).fetchall()]

    def get(self, key: str, default: str = '') -> str:
        row = self.one('SELECT value FROM settings WHERE key=?', (key,))
        return row['value'] if row else default

    def put(self, key: str, value: object) -> None:
        self.execute('INSERT INTO settings(key,value) VALUES(?,?) '
                     'ON CONFLICT(key) DO UPDATE SET value=excluded.value',
                     (key, str(value)))

    def log(self, topic: str, detail: str) -> None:
        # 只记录状态摘要；调用者不得传任务正文、对话或凭据。
        detail = SECRET.sub('[已隐藏凭据]', detail)
        self.execute('INSERT INTO event_log(at,topic,detail) VALUES(?,?,?)',
                     (time.time(), topic, detail[:500]))
        print(f'{topic}: {detail}', flush=True)

    def sync_jobs(self) -> None:
        now = time.time()
        configured = {job.id for job in self.config.jobs}
        for old in self.jobs():
            if old['id'] not in configured and old['status'] not in ('pending', 'complete'):
                raise ValueError(f'正在执行的任务 {old["id"]} 已从配置移除；请恢复配置后处理该任务')
        for order, job in enumerate(self.config.jobs):
            old = self.job(job.id)
            if old and (old['source'] != str(job.file) or old['cwd'] != str(job.cwd)):
                raise ValueError(f'已登记任务 {job.id} 的文档或工作目录改变；请使用新 id')
            if not old or old['status'] == 'pending':
                if not job.file.is_file() or not job.file.read_text(encoding='utf-8').strip():
                    raise ValueError(f'Goal 文档不存在或为空：{job.file}')
            self.execute('INSERT OR IGNORE INTO jobs(id,ord,source,cwd,budget,status,updated_at) '
                         'VALUES(?,?,?,?,?,?,?)',
                         (job.id, order, str(job.file), str(job.cwd), job.token_budget, 'pending', now))
            if old and old['status'] == 'pending':
                self.execute('UPDATE jobs SET ord=?,budget=? WHERE id=?',
                             (order, job.token_budget, job.id))

    def job(self, job_id: str) -> dict | None:
        return self.one('SELECT * FROM jobs WHERE id=?', (job_id,))

    def jobs(self) -> list[dict]:
        return self.all('SELECT * FROM jobs ORDER BY ord,id')

    def current_job(self) -> dict | None:
        configured = {j.id for j in self.config.jobs}
        for job in self.jobs():
            if job['id'] in configured and job['status'] != 'complete':
                return job
        return None

    def job_by_thread(self, thread_id: str) -> dict | None:
        return self.one('SELECT * FROM jobs WHERE thread_id=?', (thread_id,))

    def update_job(self, job_id: str, **fields: object) -> None:
        fields['updated_at'] = time.time()
        keys = list(fields)
        self.execute('UPDATE jobs SET ' + ','.join(f'{key}=?' for key in keys) + ' WHERE id=?',
                     tuple(fields[key] for key in keys) + (job_id,))

    def snapshot_job(self, job: dict, batch: str) -> dict:
        source = Path(job['source'])
        body = source.read_bytes()
        digest = hashlib.sha256(body).hexdigest()
        target = self.config.state_dir / 'snapshots' / f'{job["id"]}-{batch}.md'
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        with target.open('xb') as stream:
            os.chmod(target, 0o600)
            stream.write(body)
            stream.flush()
            os.fsync(stream.fileno())
        self.update_job(job['id'], snapshot=str(target), sha256=digest,
                        batch=batch, status='preparing')
        return self.job(job['id'])

    def watch(self, thread_id: str, cwd: str | None = None, enable: bool = True) -> None:
        now = time.time()
        self.execute('INSERT OR IGNORE INTO watches(thread_id,cwd,enabled,last_human,last_completed,updated_at) '
                     'VALUES(?,?,?,?,?,?)', (thread_id, cwd, int(enable), now, now, now))
        if enable:
            self.execute('UPDATE watches SET enabled=1,cwd=COALESCE(?,cwd),updated_at=? WHERE thread_id=?',
                         (cwd, now, thread_id))

    def unwatch(self, thread_id: str) -> None:
        self.execute('UPDATE watches SET enabled=0,updated_at=? WHERE thread_id=?',
                     (time.time(), thread_id))

    def watched(self) -> list[dict]:
        return self.all('SELECT * FROM watches WHERE enabled=1 ORDER BY thread_id')

    def watch_row(self, thread_id: str) -> dict | None:
        return self.one('SELECT * FROM watches WHERE thread_id=? AND enabled=1', (thread_id,))

    def update_watch(self, thread_id: str, **fields: object) -> None:
        fields['updated_at'] = time.time()
        keys = list(fields)
        self.execute('UPDATE watches SET ' + ','.join(f'{key}=?' for key in keys) + ' WHERE thread_id=?',
                     tuple(fields[key] for key in keys) + (thread_id,))

    def seen(self, thread_id: str, item_id: str) -> bool:
        result = self.execute('INSERT OR IGNORE INTO seen_items(thread_id,item_id) VALUES(?,?)',
                              (thread_id, item_id))
        return result.rowcount == 0

    def human(self, thread_id: str, item_id: str) -> bool:
        if self.seen(thread_id, item_id):
            return False
        self.execute('UPDATE watches SET last_human=?,activity_version=activity_version+1,updated_at=? '
                     'WHERE thread_id=? AND enabled=1', (time.time(), time.time(), thread_id))
        self.log('human', f'thread={thread_id} item={item_id}')
        return True

    def send(self, send_id: str, thread_id: str, kind: str, prompt: str, activity_version: int) -> None:
        now = time.time()
        self.execute('INSERT INTO sends(id,thread_id,kind,prompt,status,activity_version,created_at,updated_at) '
                     'VALUES(?,?,?,?,?,?,?,?)',
                     (send_id, thread_id, kind, prompt, 'intent', activity_version, now, now))

    def update_send(self, send_id: str, **fields: object) -> None:
        fields['updated_at'] = time.time()
        keys = list(fields)
        self.execute('UPDATE sends SET ' + ','.join(f'{key}=?' for key in keys) + ' WHERE id=?',
                     tuple(fields[key] for key in keys) + (send_id,))

    def unresolved_sends(self, thread_id: str | None = None) -> list[dict]:
        sql = "SELECT * FROM sends WHERE status IN ('intent','queued','uncertain','started')"
        params = ()
        if thread_id:
            sql += ' AND thread_id=?'
            params = (thread_id,)
        return self.all(sql + ' ORDER BY created_at', params)

    def recent_sends(self, thread_id: str) -> list[dict]:
        return self.all('SELECT * FROM sends WHERE thread_id=? ORDER BY created_at DESC LIMIT 20',
                        (thread_id,))

    def summary(self) -> dict:
        return {
            'codex_home': str(self.config.home),
            'socket': str(self.config.socket),
            'service_socket_exists': self.config.socket.exists(),
            'service': self.get('service', 'unknown'),
            'paused': self.get('paused', '0') == '1',
            'heartbeat_at': self.get('heartbeat_at', ''),
            'provider': self.get('provider', 'unknown'),
            'provider_at': self.get('provider_at', ''),
            'jobs': [{key: job[key] for key in ('id','status','goal_status','thread_id','pause_reason','tries')}
                     for job in self.jobs()],
            'watches': [{key: row[key] for key in ('thread_id','cwd','initialized','last_human','last_completed','last_auto')}
                        for row in self.watched()],
            'unresolved_sends': [{key: row[key] for key in ('id','thread_id','kind','status','queue_id')}
                                 for row in self.unresolved_sends()],
        }
