import limacharlie
import flask
import zlib
import json
import hmac
import hashlib
import time
import sys
import threading
import os
import inspect
from typing import Dict, List, Optional, Any, Union, Callable
from .messages import *
from .schema import *

def _acceptsACLArgument(handler: Callable[..., Any]) -> bool:
    '''Report whether a request handler can receive the ACL block as a fifth
    positional argument: it declares at least five positional parameters
    (excluding a bound self) or accepts *args.'''
    try:
        sig = inspect.signature(handler)
    except (TypeError, ValueError):
        return False
    nPositional = 0
    for p in sig.parameters.values():
        if p.kind == inspect.Parameter.VAR_POSITIONAL:
            return True
        if p.kind in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD):
            nPositional += 1
    return nPositional >= 5

# Default cap on the request body as it arrives on the wire (compressed size when
# gzipped). The platform's request entry point accepts at most a 10 MiB form field and
# a sender compresses what it sends, so 16 MiB leaves room for the envelope (config,
# event data) around the largest payload.
DEFAULT_MAX_BODY_BYTES = 16 << 20
# Default cap on the request body once decompressed (also applies to a body that is
# not compressed). A gzip stream can expand by orders of magnitude, so this is what
# bounds the memory one request can make the extension allocate.
DEFAULT_MAX_DECODED_BODY_BYTES = 64 << 20

# Printed in place of a value that may be a secret. Fixed: never a prefix, a
# length or a hash of the value.
REDACTED_MARKER = '[REDACTED]'

def _redactBySchema(values: Any, schema: Optional[SchemaObject]) -> Any:
    '''Return a copy of values (a dict of parameters or config fields) for a log:
    a field the schema declares Secret-typed, or does not declare, has its value
    replaced by REDACTED_MARKER; an object field is redacted by its own schema.
    Without a schema, every value is replaced.'''
    if not isinstance(values, dict):
        return None if values is None else REDACTED_MARKER
    out = {}
    for k, v in values.items():
        element = None if schema is None else schema.Fields.get(k, None)
        if v is None:
            out[k] = None
        elif element is None or element.DataType == SchemaDataTypes.Secret:
            out[k] = REDACTED_MARKER
        elif element.ObjectSchema is not None:
            if element.IsList and isinstance(v, list):
                out[k] = [_redactBySchema(i, element.ObjectSchema) for i in v]
            else:
                out[k] = _redactBySchema(v, element.ObjectSchema)
        else:
            out[k] = v
    return out

# The shortest value _scrubSecrets replaces: a shorter one cannot be a
# credential, and replacing it would mangle unrelated text.
_MIN_SCRUBBED_SECRET_LEN = 4

def _collectSecrets(values: Any, schema: Optional[SchemaObject], out: List[str]) -> None:
    '''Append to out the values of values (a dict) that the schema declares
    Secret-typed, in lists and object fields too.'''
    if not isinstance(values, dict) or schema is None:
        return
    for k, v in values.items():
        element = schema.Fields.get(k, None)
        if element is None or v is None:
            continue
        for item in (v if isinstance(v, list) else [v]):
            if element.DataType == SchemaDataTypes.Secret:
                if isinstance(item, str) and len(item) >= _MIN_SCRUBBED_SECRET_LEN:
                    out.append(item)
            elif element.ObjectSchema is not None:
                _collectSecrets(item, element.ObjectSchema, out)

def _scrubSecrets(text: str, secrets: List[str]) -> str:
    '''text with every occurrence of a value of secrets replaced by
    REDACTED_MARKER, longest value first.'''
    for v in sorted(set(secrets), key=len, reverse=True):
        if len(v) >= _MIN_SCRUBBED_SECRET_LEN:
            text = text.replace(v, REDACTED_MARKER)
    return text

class _BodyTooLarge(Exception):
    pass

class _BadBody(Exception):
    pass

def _gunzipChunks(raw: bytes, limit: int):
    '''Yield the decompressed content of a (possibly multi-member) gzip stream in
    bounded chunks, raising _BodyTooLarge as soon as more than limit bytes would be
    produced, so a small body that expands hugely never gets materialised.'''
    if not raw:
        raise _BadBody("empty gzip body")
    total = 0
    pending = raw
    chunkSize = 1 << 16
    while pending:
        d = zlib.decompressobj(wbits = 16 + zlib.MAX_WBITS)
        try:
            chunk = d.decompress(pending, chunkSize)
            while True:
                total += len(chunk)
                if total > limit:
                    raise _BodyTooLarge()
                if chunk:
                    yield chunk
                if d.eof:
                    break
                tail = d.unconsumed_tail
                if not chunk and not tail:
                    # No output and no input left, yet the member has not ended.
                    raise _BadBody("truncated gzip body")
                chunk = d.decompress(tail, chunkSize)
        except zlib.error as e:
            raise _BadBody(f"invalid gzip body: {e}")
        pending = d.unused_data

def _readBounded(stream, limit: int) -> bytes:
    '''Read a stream to EOF, raising _BodyTooLarge as soon as more than limit bytes
    are seen. A WSGI stream may return fewer bytes than asked for before EOF, so a
    single read(limit + 1) could truncate a legitimate body: read until it returns
    nothing.'''
    parts = []
    total = 0
    while True:
        chunk = stream.read(min(1 << 16, limit + 1 - total))
        if not chunk:
            return b"".join(parts)
        total += len(chunk)
        if total > limit:
            raise _BodyTooLarge()
        parts.append(chunk)

class Extension(object):
    
    def __init__(self, name: str, secret: str, max_body_bytes: int = DEFAULT_MAX_BODY_BYTES, max_decoded_body_bytes: int = DEFAULT_MAX_DECODED_BODY_BYTES):
        self._name: str = name
        self._secret: str = secret
        # Caps on the request body (wire size, and size once decompressed), enforced
        # before the signature is checked. Keep them as low as the largest request the
        # extension legitimately receives.
        self._maxBodyBytes: int = max_body_bytes if max_body_bytes > 0 else DEFAULT_MAX_BODY_BYTES
        self._maxDecodedBodyBytes: int = max_decoded_body_bytes if max_decoded_body_bytes > 0 else DEFAULT_MAX_DECODED_BODY_BYTES
        self._lock: threading.Lock = threading.Lock()
        self.viewSchemas: List[SchemaView] = []
        self.configSchema: SchemaObject = SchemaObject()
        self.requestSchema: RequestSchemas = RequestSchemas()
        self.requiredEvents: List[str] = []

        self._isLogRequest: bool = os.environ.get("LOG_REQUEST", "") != ""
        self._isLogResponse: bool = os.environ.get("LOG_RESPONSE", "") != ""

        self._app: flask.Flask = flask.Flask(self._name)

        self.wh_clients: Dict[str, limacharlie.WebhookSender] = {}  # equivalent to whClients in Go
        self.wh_clients_lock: threading.Lock = threading.Lock()  # For thread-safe access to wh_clients

        @self._app.post('/')
        def _handler() -> tuple[str, int]:
            sig = flask.request.headers.get('lc-ext-sig', None)
            if not sig:
                return json.dumps({}), 200
            try:
                data, isVerified = self._readBody(sig)
            except _BodyTooLarge:
                return json.dumps(Response(error = "request body too large").toJSON()), 413
            except _BadBody as e:
                return json.dumps(Response(error = str(e)).toJSON()), 400
            if self._isLogRequest:
                self.log(f"request: {self._requestForLog(data)}")
            if not isVerified:
                resp = json.dumps(Response(error = "invalid signature").toJSON())
                return resp, 401
            try:
                data = json.loads(data)
            except:
                pass
            else:
                try:
                    resp = self._extRequestHandler(data)
                except Exception as e:
                    # An exception's text can carry the request's secrets (a
                    # handler or an SDK echoing its credential).
                    text = _scrubSecrets(str(e), self._secretValues(data))
                    resp = Response(error = text)
                    self.logCritical(f"exception: {text}")
                status = 200
                if resp.error:
                    status = 500 if resp.is_retriable else 400
                resp = json.dumps(resp.toJSON())
                if self._isLogResponse:
                    self.log(f"response: {_scrubSecrets(resp, self._secretValues(data))}")
                return resp, status
            resp = json.dumps(Response(error = "invalid request").toJSON())
            if self._isLogResponse:
                self.log(f"response: {resp}")
            return resp, 400

        self.init()

    def getApp(self) -> flask.Flask:
        return self._app

    def _readBody(self, signature: str) -> tuple:
        '''Read the request body, bounded, and return (decoded body, signature ok).

        The body is untrusted until the signature is verified, so what is held in memory
        is bounded by the caps before authentication rather than by what the sender chose
        to send: the wire body is capped; a gzip body is first decompressed straight into
        the signature check (nothing retained, itself capped) and is only materialised
        once the signature matched.'''
        isGzip = flask.request.headers.get('Content-Encoding', '') == 'gzip'
        # An uncompressed body is its own decoded form.
        wireLimit = self._maxBodyBytes if isGzip else self._maxDecodedBodyBytes
        declared = flask.request.content_length
        if declared is not None and declared > wireLimit:
            raise _BodyTooLarge()
        raw = _readBounded(flask.request.stream, wireLimit)
        if not isGzip:
            return raw, self._verifyOrigin(raw, signature)
        if self._secret is None:
            return b"".join(_gunzipChunks(raw, self._maxDecodedBodyBytes)), True
        mac = hmac.new(self._secret.encode(), digestmod = hashlib.sha256)
        for chunk in _gunzipChunks(raw, self._maxDecodedBodyBytes):
            mac.update(chunk)
        if isinstance(signature, bytes):
            signature = signature.decode()
        if not hmac.compare_digest(mac.hexdigest(), signature):
            return b"", False
        return b"".join(_gunzipChunks(raw, self._maxDecodedBodyBytes)), True

    def _requestForLog(self, data: Union[str, bytes]) -> str:
        '''Render a request body for the LOG_REQUEST debug log. Only metadata
        (protocol version, idempotency key, section names, oid, ident, action,
        event name) and the values the extension's schema declares non-Secret
        (request parameters, config fields) are shown; every other value, the
        org JWT included, is REDACTED_MARKER. A body that is not a JSON object
        is not logged, only its size.'''
        try:
            msg = json.loads(data)
            if not isinstance(msg, dict):
                return f"<not a JSON object, {len(data)} bytes, not logged>"
            return json.dumps(self._messageForLog(msg))
        except Exception:
            return f"<unrenderable request, {len(data)} bytes, not logged>"

    def _messageForLog(self, msg: Dict[str, Any]) -> Dict[str, Any]:
        out: Dict[str, Any] = {}
        for section, body in msg.items():
            if section in ('version', 'idempotency_key'):
                out[section] = body if isinstance(body, (str, int)) else REDACTED_MARKER
                continue
            if not isinstance(body, dict):
                out[section] = None if body is None else REDACTED_MARKER
                continue
            shown: Dict[str, Any] = {}
            for k, v in body.items():
                if k == 'org' and isinstance(v, dict):
                    shown[k] = {ok: (ov if ok in ('oid', 'ident') and isinstance(ov, str) else (None if ov is None else REDACTED_MARKER)) for ok, ov in v.items()}
                elif k in ('action', 'event_name', 'inv_id') and isinstance(v, str):
                    shown[k] = v
                elif k in ('config', 'conf'):
                    shown[k] = _redactBySchema(v, self.configSchema)
                elif k == 'data' and section == 'request':
                    shown[k] = _redactBySchema(v, self._parameterSchema(body.get('action', None)))
                else:
                    shown[k] = None if v is None else REDACTED_MARKER
            out[section] = shown
        return out

    def _parameterSchema(self, action: Any) -> Optional[SchemaObject]:
        if not isinstance(action, str):
            return None
        schema = self.requestSchema.Actions.get(action, None)
        return None if schema is None else schema.ParameterDefinitions

    def _secretValues(self, msg: Any) -> List[str]:
        '''Every Secret-typed value a request message carries: its org JWT, its
        request parameters (by the action's schema) and its config (by the
        config schema).'''
        out: List[str] = []
        try:
            if not isinstance(msg, dict):
                return out
            for section in ('request', 'event', 'conf_validation'):
                body = msg.get(section, None)
                if not isinstance(body, dict):
                    continue
                org = body.get('org', None)
                if isinstance(org, dict) and isinstance(org.get('jwt', None), str):
                    out.append(org['jwt'])
                for confKey in ('config', 'conf'):
                    _collectSecrets(body.get(confKey, None), self.configSchema, out)
                if section == 'request':
                    _collectSecrets(body.get('data', None), self._parameterSchema(body.get('action', None)), out)
        except Exception:
            pass
        return out

    def _verifyOrigin(self, data: Union[str, bytes], signature: Union[str, bytes]) -> bool:
        if self._secret is None:
            return True
        if isinstance(data, str):
            data = data.encode()
        if isinstance(signature, bytes):
            signature = signature.decode()
        expected = hmac.new(self._secret.encode(), msg = data, digestmod = hashlib.sha256).hexdigest()
        return hmac.compare_digest(expected, signature)

    def _extRequestHandler(self, data: Dict[str, Any]) -> Response:
        msg = Message(data)
        if msg.msg_heart_beat is not None:
            return Response()
        if msg.msg_config_validation is not None:
            sdk = limacharlie.Manager(oid = msg.msg_config_validation.org_access_data.oid, jwt = msg.msg_config_validation.org_access_data.jwt)
            try:
                self.validateConfig(sdk, msg.msg_config_validation.conf)
            except Exception as e:
                return Response(error = str(e))
            return Response()
        if msg.msg_request is not None:
            sdk = limacharlie.Manager(oid = msg.msg_request.org_access_data.oid, jwt = msg.msg_request.org_access_data.jwt)
            handler = self.requestHandlers.get(msg.msg_request.action, None)
            if handler is None:
                self.logCritical(f"unknown action '{msg.msg_request.action}'")
                return Response(error = f"unknown action '{msg.msg_request.action}'")
            return self._callRequestHandler(handler, sdk, msg.msg_request)
        if msg.msg_event is not None:
            sdk = limacharlie.Manager(oid = msg.msg_event.org_access_data.oid, jwt = msg.msg_event.org_access_data.jwt)
            handler = self.eventHandlers.get(msg.msg_event.event_name, None)
            if handler is None:
                self.logCritical(f"unknown event '{msg.msg_event.event_name}'")
                return Response(error = f"unknown event '{msg.msg_event.event_name}'")
            return handler(sdk, msg.msg_event.data, msg.msg_event.conf)
        if msg.msg_error_report is not None:
            self.handleError(msg.msg_error_report.oid, msg.msg_error_report.error)
            return Response()
        if msg.msg_schema_request is not None:
            return Response(data = {
                'views': [v.serialize() for v in self.viewSchemas],
                'config_schema': self.configSchema.serialize(),
                'request_schema': self.requestSchema.serialize(),
                'required_events': self.requiredEvents,
            })
        return Response(error = 'no data in request')
    
    def _callRequestHandler(self, handler: Callable[..., Response], sdk: limacharlie.Manager, req: MessageRequest) -> Response:
        # Request handlers historically take (sdk, data, conf, resState). The
        # platform-set ACL block is passed as an optional trailing positional
        # argument to handlers that declare a fifth parameter (or *args), so
        # existing four-argument handlers keep working unchanged.
        if _acceptsACLArgument(handler):
            return handler(sdk, req.data, req.conf, req.resState, req.acl)
        return handler(sdk, req.data, req.conf, req.resState)

    def _handleEvent(self, sdk: limacharlie.Manager, data: Dict[str, Any], conf: Dict[str, Any]) -> Response:
        return Response()
    
    # Helper functions, feel free to override.
    def log( self, msg: str, data: Optional[Dict[str, Any]] = None ):
        '''Log a message to stdout.

        :param msg: message to log.
        :param data: optional JSON data to include in log.
        '''
        with self._lock:
            ts = time.time()
            entry = {
                'extension' : self._name,
                'timestamp' : {
                    'seconds' : int( ts ),
                    'nanos' : int( ( ts % 1 ) * 1000000000 )
                },
                'severity' : 'INFO',
            }
            if msg is not None:
                entry[ 'message' ] = msg
            if data is not None:
                entry.update( data )
            print( json.dumps( entry ) )
            sys.stdout.flush()

    def logCritical( self, msg: str ):
        '''Log a message to stderr.

        :param msg: critical message to log.
        '''
        with self._lock:
            ts = time.time()
            sys.stderr.write( json.dumps( {
                'message' : msg,
                'extension' : self._name,
                'timestamp' : {
                    'seconds' : int( ts ),
                    'nanos' : int( ( ts % 1 ) * 1000000000 )
                },
                'severity' : 'ERROR',
            } ) )
            sys.stderr.write( "\n" )
            sys.stderr.flush()

    ####### start of webhook methods #####
            
    def create_extension_adapter(self, manager: limacharlie.Manager, opt_mapping: Dict[str, Any] = {}) -> None:
        private_tag = self.get_extension_private_tag()
        try:
            response = manager.create_installation_key(["lc:system", private_tag], self.get_extension_adapter_installation_key_desc())
        except Exception as e:
            raise limacharlie.LcApiException(f"failed to create installation key : {e}")

        hive = limacharlie.Hive(manager, "cloud_sensor", manager._oid)
        iid = response["iid"]
        data = {
                "usr_mtd":{
                    "enabled": True,
                    "tags": ["lc:system", private_tag],
                },
                "data": {
                    "sensor_type": "webhook",
                    "webhook": {
                        "secret": self.generate_webhook_secret_for_org(manager._oid),
                        "client_options": {
                            "hostname": self._name, # ext-name
                            "identity": {
                                "oid": manager._oid,
                                "installation_key": iid,
                            },
                            "platform": "json",
                            "sensor_seed_key": self._name, # ext-name
                            "mapping": opt_mapping,
                        },
                    },
                },
            }
        
        try: 
            hive_record = limacharlie.HiveRecord(self._name, data)
            hive.set(hive_record)
        except Exception as e:
            raise limacharlie.LcApiException(f"failed to create webhook adapter: {e}")

    def delete_extension_adapter(self, manager: limacharlie.Manager) -> None:
        hive = limacharlie.Hive(manager, "cloud_sensor", manager._oid)
        try:
            hive.delete(self._name)
        except Exception as e:
                if not (isinstance(e, limacharlie.LcApiException) and "RECORD_NOT_FOUND" in str(e)):
                    raise limacharlie.LcApiException(f"failed hive delete: {e}")
        
        install_key_desc = self.get_extension_adapter_installation_key_desc()
        try:
            install_keys = manager.get_installation_keys()
        except Exception as e:
            raise limacharlie.LcApiException(f"failed to list installation keys: {e}")
        
        private_tag = self.get_extension_private_tag()

        for org_id, keys in install_keys.items():
            for key_id, key in keys.items():
                if key['desc'] != install_key_desc:
                    continue

                tags = [tag.strip() for tag in key['tags'].split(',') if tag.strip()]
                is_tag_found = private_tag in tags
                if not is_tag_found:
                    continue

                try:
                    manager.delete_installation_key(key['iid'])
                except Exception as e:
                    if not (isinstance(e, limacharlie.LcApiException) and "RECORD_NOT_FOUND" in str(e)):
                        raise limacharlie.LcApiException(f"Failed to delete installation key {key['iid']}: {e}")


    def generate_webhook_secret_for_org(self, oid: str) -> str:
        # This generates a secret value deterministically from
        # the OID so that we can easily know the webhook to
        # hit without having to query LC. The WEBHOOK_SECRET
        # needs to remain secret to avoid someone possibly
        # sending their own data to users.
        h = hashlib.sha256()
        h.update(self._secret.encode())
        h.update(oid.encode())
        return h.hexdigest()
    
    def get_extension_adapter_installation_key_desc(self) -> str:
        # Returns a description string for the extension adapter installation key
        return f"ext {self._name} webhook adapter"

    def get_adapter_client(self, manager: limacharlie.Manager) -> limacharlie.WebhookSender:
        # try to get the client if it already exists
        with self.wh_clients_lock:
            client = self.wh_clients.get(manager._oid)

            if client:
                return client

            # Create a new client if it doesn't exist
            try:
                new_client = limacharlie.WebhookSender(manager, self._name, self.generate_webhook_secret_for_org(manager._oid))
            except Exception as e:
                raise Exception(f"failed to create webhook sender client: {e} ")

            self.wh_clients[manager._oid] = new_client

            return new_client
    
    def send_to_webhook_adapter(self, manager: limacharlie.Manager, data: Dict[str, Any]) -> None:
        try:
            wh_client = self.get_adapter_client(manager)
        except Exception as e:
            raise Exception(f"failed to get adapter client: {e}")
        
        try:
            wh_client.send(data)
        except Exception as e:
            raise Exception(f"failed webhook client send: {e}")
    
    def get_extension_private_tag(self) -> str:
        return f"ext:{self._name}"