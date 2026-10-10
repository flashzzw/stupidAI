"""Loopback MCP OAuth server with PKCE, rotating refresh tokens and A/B users."""
import base64
import hashlib
import json
import queue
import secrets
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlencode, urlsplit


class OAuthMCP:
    def __init__(self, root, transport='http'):
        self.root = root
        self.transport = transport
        self.lock = threading.RLock()
        self.closed=threading.Event()
        self.sessions={}
        self.codes, self.tokens, self.refresh, self.clients = {}, {}, {}, {}
        self.events, self.calls = [], []
        self.ttl = 3
        self.reject_refresh = False
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_): pass

            def reply(self, status, value=None, headers=None):
                data = json.dumps(value).encode() if value is not None else b''
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(data)))
                for key, value in (headers or {}).items(): self.send_header(key, value)
                self.end_headers()
                if data: self.wfile.write(data)

            def do_GET(self):
                parsed = urlsplit(self.path)
                if parsed.path.startswith('/.well-known/oauth-protected-resource'):
                    return self.reply(200, {'resource': owner.endpoint, 'authorization_servers': [owner.url], 'scopes_supported': ['mcp:tools']})
                if parsed.path in ('/.well-known/oauth-authorization-server', '/.well-known/openid-configuration'):
                    return self.reply(200, {'issuer':owner.url,'authorization_endpoint':owner.url+'/authorize','token_endpoint':owner.url+'/token',
                        'registration_endpoint':owner.url+'/register','response_types_supported':['code'],'grant_types_supported':['authorization_code','refresh_token'],
                        'code_challenge_methods_supported':['S256'],'token_endpoint_auth_methods_supported':['none'],'scopes_supported':['mcp:tools']})
                if parsed.path == '/authorize':
                    params = {k:v[0] for k,v in parse_qs(parsed.query).items()}
                    profile = self.headers.get('X-Fixture-Account')
                    if profile not in ('A','B') or params.get('code_challenge_method') != 'S256': return self.reply(400, {'error':'fixture user and S256 required'})
                    with owner.lock:
                        code = secrets.token_urlsafe(24)
                        owner.codes[code] = {**params, 'profile':profile}
                        owner.events.append({'kind':'authorize','profile':profile,'pkce':True,'resource':params.get('resource')})
                    return self.reply(302, headers={'Location':params['redirect_uri']+'?'+urlencode({'code':code,'state':params.get('state','')})})
                if parsed.path == '/sse' and owner.transport=='sse':
                    token=self.identity()
                    if token is None:return
                    sid=secrets.token_urlsafe(24);messages=queue.Queue()
                    with owner.lock:owner.sessions[sid]=(token[0],messages)
                    self.send_response(200);self.send_header('Content-Type','text/event-stream');self.send_header('Cache-Control','no-cache');self.send_header('Connection','close');self.end_headers()
                    try:
                        self.wfile.write(('event: endpoint\ndata: /messages?sessionId='+sid+'\n\n').encode());self.wfile.flush()
                        while not owner.closed.is_set():
                            try:value=messages.get(timeout=.2)
                            except queue.Empty:continue
                            self.wfile.write(('event: message\ndata: '+json.dumps(value)+'\n\n').encode());self.wfile.flush()
                    except (BrokenPipeError,ConnectionResetError):pass
                    finally:
                        with owner.lock:owner.sessions.pop(sid,None)
                    return
                if parsed.path == '/mcp': return self.reply(405)
                return self.reply(404)

            def identity(self):
                bearer=self.headers.get('Authorization','').removeprefix('Bearer ')
                with owner.lock:token=owner.tokens.get(bearer)
                if not token or token[1]<=time.monotonic():
                    with owner.lock:owner.events.append({'kind':'challenge','profile':token[0] if token else None})
                    self.reply(401,{'error':'authorization required'},{'WWW-Authenticate':'Bearer resource_metadata="'+owner.url+'/.well-known/oauth-protected-resource"'})
                    return None
                return token

            def do_POST(self):
                raw = self.rfile.read(int(self.headers.get('Content-Length','0')))
                path = urlsplit(self.path).path
                if path == '/register':
                    params = json.loads(raw)
                    client = 'fixture-'+secrets.token_urlsafe(12)
                    with owner.lock: owner.clients[client] = params
                    return self.reply(201, {**params,'client_id':client,'client_id_issued_at':int(time.time()),'token_endpoint_auth_method':'none'})
                if path == '/token':
                    params = {k:v[0] for k,v in parse_qs(raw.decode()).items()}
                    with owner.lock:
                        if params.get('grant_type') == 'authorization_code':
                            original = owner.codes.pop(params.get('code'), None)
                            challenge = base64.urlsafe_b64encode(hashlib.sha256(params.get('code_verifier','').encode()).digest()).decode().rstrip('=')
                            if not original or challenge != original.get('code_challenge') or params.get('client_id') != original.get('client_id') or params.get('redirect_uri') != original.get('redirect_uri'):
                                return self.reply(400, {'error':'invalid_grant'})
                            profile = original['profile']
                        elif params.get('grant_type') == 'refresh_token':
                            profile = owner.refresh.pop(params.get('refresh_token'), None)
                            if not profile or owner.reject_refresh: return self.reply(400, {'error':'invalid_grant'})
                        else: return self.reply(400, {'error':'unsupported_grant_type'})
                        access, refresh = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
                        owner.tokens[access] = (profile,time.monotonic()+owner.ttl)
                        owner.refresh[refresh] = profile
                        owner.events.append({'kind':params['grant_type'],'profile':profile,'resource':params.get('resource')})
                    return self.reply(200, {'access_token':access,'token_type':'Bearer','expires_in':owner.ttl,'refresh_token':refresh,'scope':'mcp:tools'})
                if path not in ('/mcp','/messages'): return self.reply(404)
                token=self.identity()
                if token is None:return
                messages=None
                if path=='/messages':
                    sid=parse_qs(urlsplit(self.path).query).get('sessionId',[''])[0]
                    with owner.lock:session=owner.sessions.get(sid)
                    if not session or session[0]!=token[0]:return self.reply(403)
                    messages=session[1]
                request = json.loads(raw)
                if 'id' not in request: return self.reply(202)
                if request['method'] == 'initialize': result = {'protocolVersion':request['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'fixture-oauth','version':'1'}}
                elif request['method'] == 'tools/list': result = {'tools':[{'name':'whoami','description':'Return authenticated fixture identity','inputSchema':{'type':'object','properties':{}}}]}
                elif request['method'] == 'tools/call':
                    identity = {'profile':token[0],'workspace':self.headers.get('X-Netcap-Workspace',''),'oauth':True,'transport':owner.transport}
                    with owner.lock: owner.calls.append(identity)
                    result = {'content':[{'type':'text','text':json.dumps(identity)}]}
                else: result = {}
                response={'jsonrpc':'2.0','id':request['id'],'result':result}
                if messages is not None:messages.put(response);return self.reply(202)
                return self.reply(200,response)

            def do_DELETE(self): self.reply(200)

        self.server = ThreadingHTTPServer(('127.0.0.1',0),Handler)
        self.url = 'http://127.0.0.1:'+str(self.server.server_port)
        self.endpoint=self.url+('/mcp' if transport=='http' else '/sse')
        self.thread = threading.Thread(target=self.server.serve_forever,daemon=True)
        self.thread.start()

    def close(self):
        self.closed.set()
        self.server.shutdown();self.server.server_close();self.thread.join(timeout=5)
