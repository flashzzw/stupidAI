"""使用标准库在 Codex 共享 Unix socket 上实现 WebSocket JSON-RPC。"""

from __future__ import annotations

import base64
import hashlib
import json
import os
import queue
import socket
import struct
import subprocess
import threading
import time
from pathlib import Path

WS_GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11'
MAX_MESSAGE = 16 * 1024 * 1024


class RpcError(RuntimeError):
    pass


class RpcDisconnected(RpcError):
    pass


class _SocketReader:
    def __init__(self, sock: socket.socket, initial: bytes):
        self.sock, self.buffer = sock, initial

    def exact(self, count: int) -> bytes:
        while len(self.buffer) < count:
            chunk = self.sock.recv(min(65536, count - len(self.buffer)))
            if not chunk:
                raise RpcDisconnected('WebSocket 已关闭')
            self.buffer += chunk
        out, self.buffer = self.buffer[:count], self.buffer[count:]
        return out

    def frame(self) -> tuple[int, bytes, bool]:
        first, second = self.exact(2)
        fin, opcode, masked = bool(first & 0x80), first & 0x0F, bool(second & 0x80)
        length = second & 0x7F
        if length == 126:
            length = struct.unpack('!H', self.exact(2))[0]
        elif length == 127:
            length = struct.unpack('!Q', self.exact(8))[0]
        if length > MAX_MESSAGE:
            raise RpcDisconnected('WebSocket 消息超过大小限制')
        mask = self.exact(4) if masked else None
        payload = self.exact(length)
        if mask:
            payload = bytes(byte ^ mask[i % 4] for i, byte in enumerate(payload))
        return opcode, payload, fin


class AppServer:
    def __init__(self, home: Path, socket_path: Path):
        self.home, self.socket_path = home, socket_path
        self.sock: socket.socket | None = None
        self.incoming: queue.Queue = queue.Queue()
        self.events: queue.Queue = queue.Queue()
        self.next_id = 0
        self._request_lock = threading.Lock()
        self._send_lock = threading.Lock()

    @property
    def env(self) -> dict[str, str]:
        return {**os.environ, 'CODEX_HOME': str(self.home)}

    def _start_daemon(self) -> None:
        try:
            result = subprocess.run(['codex', 'app-server', 'daemon', 'start'],
                                    env=self.env, capture_output=True, text=True, timeout=30)
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise RpcDisconnected(f'无法启动共享服务：{exc}') from exc
        if result.returncode:
            raise RpcDisconnected(f'无法启动共享服务：{result.stderr.strip()[:300]}')

    def connect(self, auto_start: bool = True) -> None:
        self.close()
        if not self.socket_path.exists():
            if not auto_start:
                raise RpcDisconnected(f'共享服务不可用：{self.socket_path}')
            self._start_daemon()
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(15)
        try:
            try:
                sock.connect(str(self.socket_path))
            except OSError:
                sock.close()
                if not auto_start:
                    raise
                self._start_daemon()
                sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                sock.settimeout(15)
                sock.connect(str(self.socket_path))
            key = base64.b64encode(os.urandom(16)).decode('ascii')
            request = ('GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\n'
                       'Connection: Upgrade\r\nSec-WebSocket-Key: ' + key +
                       '\r\nSec-WebSocket-Version: 13\r\n\r\n')
            sock.sendall(request.encode('ascii'))
            header = b''
            while b'\r\n\r\n' not in header and len(header) < 16 * 1024:
                chunk = sock.recv(4096)
                if not chunk:
                    raise RpcDisconnected('共享服务关闭了握手连接')
                header += chunk
            head, separator, extra = header.partition(b'\r\n\r\n')
            if not separator or not head.startswith(b'HTTP/1.1 101 '):
                raise RpcDisconnected('共享服务拒绝 WebSocket 握手')
            fields = {}
            for line in head.split(b'\r\n')[1:]:
                if b':' in line:
                    name, value = line.split(b':', 1)
                    fields[name.strip().lower()] = value.strip()
            digest = hashlib.sha1((key + WS_GUID).encode('ascii')).digest()
            if fields.get(b'sec-websocket-accept') != base64.b64encode(digest):
                raise RpcDisconnected('共享服务 WebSocket 握手校验失败')
            sock.settimeout(None)
        except (OSError, RpcDisconnected) as exc:
            sock.close()
            raise RpcDisconnected(f'无法连接共享服务：{exc}') from exc
        self.sock = sock
        self.incoming = queue.Queue()
        self.events = queue.Queue()
        reader = _SocketReader(sock, extra)
        threading.Thread(target=self._reader_loop, args=(reader, self.incoming, self.events),
                         daemon=True).start()
        self.call('initialize', {'clientInfo': {'name': 'codex-netcap', 'version': '1.0.0'},
                                 'capabilities': {'experimentalApi': True}}, timeout=15)
        self._write({'method': 'initialized', 'params': {}})

    def _frame(self, opcode: int, payload: bytes) -> None:
        if not self.sock:
            raise RpcDisconnected('共享服务连接已断开')
        size = len(payload)
        if size > MAX_MESSAGE:
            raise RpcError('WebSocket 消息超过大小限制')
        if size < 126:
            header = bytes([0x80 | opcode, 0x80 | size])
        elif size <= 65535:
            header = bytes([0x80 | opcode, 0x80 | 126]) + struct.pack('!H', size)
        else:
            header = bytes([0x80 | opcode, 0x80 | 127]) + struct.pack('!Q', size)
        mask = os.urandom(4)
        body = bytes(byte ^ mask[i % 4] for i, byte in enumerate(payload))
        with self._send_lock:
            try:
                self.sock.sendall(header + mask + body)
            except OSError as exc:
                raise RpcDisconnected('共享服务连接已断开') from exc

    def _reader_loop(self, reader: _SocketReader, incoming: queue.Queue,
                     events: queue.Queue) -> None:
        fragments = bytearray()
        try:
            while True:
                opcode, payload, fin = reader.frame()
                if opcode == 8:
                    break
                if opcode == 9:
                    self._frame(10, payload)
                    continue
                if opcode == 10:
                    continue
                if opcode == 1:
                    fragments = bytearray(payload)
                elif opcode == 0:
                    fragments.extend(payload)
                else:
                    continue
                if len(fragments) > MAX_MESSAGE:
                    raise RpcDisconnected('WebSocket 消息超过大小限制')
                if not fin:
                    continue
                try:
                    item = json.loads(fragments.decode('utf-8'))
                except (UnicodeDecodeError, json.JSONDecodeError):
                    continue
                if isinstance(item, dict):
                    (incoming if 'id' in item else events).put(item)
                fragments.clear()
        except (OSError, RpcDisconnected):
            pass
        finally:
            incoming.put(None)

    def _write(self, message: dict) -> None:
        self._frame(1, json.dumps(message, ensure_ascii=False).encode('utf-8'))

    def call(self, method: str, params: dict | None = None, timeout: float = 20) -> dict:
        with self._request_lock:
            self.next_id += 1
            request_id = self.next_id
            self._write({'id': request_id, 'method': method, 'params': params or {}})
            deadline = time.monotonic() + timeout
            while True:
                try:
                    response = self.incoming.get(timeout=max(0.01, deadline - time.monotonic()))
                except queue.Empty as exc:
                    raise RpcDisconnected(f'{method} 响应超时；执行结果待核对') from exc
                if response is None:
                    raise RpcDisconnected(f'{method} 连接断开；执行结果待核对')
                if response.get('id') != request_id:
                    continue
                if 'error' in response:
                    error = response['error']
                    raise RpcError(f'{method}: {error.get("message", error)}')
                return response.get('result') or {}

    def drain_events(self) -> list[dict]:
        out = []
        while True:
            try:
                out.append(self.events.get_nowait())
            except queue.Empty:
                return out

    def close(self) -> None:
        if self.sock:
            try:
                self.sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            self.sock.close()
            self.sock = None
