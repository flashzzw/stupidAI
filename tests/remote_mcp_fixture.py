"""Owned loopback MCP HTTP and SSE endpoints; no external accounts or traffic."""
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import queue
import threading
from urllib.parse import parse_qs, urlsplit
from uuid import uuid4


class RemoteMCP:
    def __init__(self, root):
        self.root = Path(root)
        self.requests = []
        self.calls = []
        self.accept_auth = True
        self.sessions = {}
        self.closed = threading.Event()
        self.lock = threading.Lock()
        owner = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'

            def log_message(self, *_): pass

            def reply(self, status, value=None, headers=None):
                data = json.dumps(value).encode() if value is not None else b''
                self.send_response(status)
                self.send_header('Content-Length', str(len(data)))
                self.send_header('Content-Type', 'application/json')
                for k, v in (headers or {}).items(): self.send_header(k, v)
                self.end_headers()
                if data: self.wfile.write(data)

            def identity(self):
                profile = self.headers.get('X-Netcap-Profile', '')
                valid = owner.accept_auth and profile in ('A', 'B') and self.headers.get('Authorization') == 'Bearer fixture-mcp-' + profile
                identity = {'profile': profile if profile in ('A', 'B') else 'unknown',
                    'workspace': self.headers.get('X-Netcap-Workspace', ''), 'credential_matches': valid}
                with owner.lock:
                    owner.requests.append({'method': self.command, 'path': urlsplit(self.path).path, **identity})
                if not valid:
                    # 403 deliberately has no OAuth challenge/discovery: this is
                    # a header-auth fixture, not an external OAuth integration.
                    self.reply(403, {'error': 'fixture authentication failed'})
                    return None
                return identity

            def do_GET(self):
                identity = self.identity()
                if identity is None: return
                if urlsplit(self.path).path != '/sse':
                    self.reply(405)
                    return
                sid = str(uuid4())
                messages = queue.Queue()
                with owner.lock: owner.sessions[sid] = (identity, messages)
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.send_header('Cache-Control', 'no-cache')
                self.send_header('Connection', 'close')
                self.end_headers()
                try:
                    self.wfile.write(f'event: endpoint\ndata: /messages?sessionId={sid}\n\n'.encode())
                    self.wfile.flush()
                    while not owner.closed.is_set():
                        try: message = messages.get(timeout=.25)
                        except queue.Empty: continue
                        self.wfile.write(('event: message\ndata: ' + json.dumps(message) + '\n\n').encode())
                        self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError): pass
                finally:
                    with owner.lock: owner.sessions.pop(sid, None)

            def do_POST(self):
                size = int(self.headers.get('Content-Length', '0'))
                data = self.rfile.read(size)
                identity = self.identity()
                if identity is None: return
                request = json.loads(data)
                route = urlsplit(self.path)
                transport = 'http' if route.path == '/http' else 'sse'
                messages = None
                if route.path == '/messages':
                    sid = parse_qs(route.query).get('sessionId', [''])[0]
                    with owner.lock: session = owner.sessions.get(sid)
                    if session is None or session[0] != identity:
                        self.reply(403)
                        return
                    messages = session[1]
                elif route.path != '/http':
                    self.reply(404)
                    return
                method = request.get('method')
                if 'id' not in request:
                    self.reply(202)
                    return
                if method == 'initialize':
                    result = {'protocolVersion': request['params']['protocolVersion'], 'capabilities': {'tools': {}},
                        'serverInfo': {'name': 'fixture-remote-identity', 'version': '1'}}
                elif method == 'tools/list':
                    result = {'tools': [{'name': 'whoami', 'description': 'Return the authenticated remote account and requested workspace',
                        'inputSchema': {'type': 'object', 'properties': {}}}]}
                elif method == 'tools/call':
                    returned = {**identity, 'transport': transport}
                    with owner.lock:
                        owner.calls.append(returned)
                        with (owner.root / 'remote-calls.jsonl').open('a') as output: output.write(json.dumps(returned) + '\n')
                    result = {'content': [{'type': 'text', 'text': json.dumps(returned)}]}
                else: result = {}
                response = {'jsonrpc': '2.0', 'id': request['id'], 'result': result}
                if messages is not None:
                    messages.put(response)
                    self.reply(202)
                else: self.reply(200, response)

            def do_DELETE(self):
                if self.identity() is not None: self.reply(200)

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f'http://127.0.0.1:{self.server.server_port}'

    def close(self):
        self.closed.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)
