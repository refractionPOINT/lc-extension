import limacharlie
import flask
import gzip
import json
import hmac
import hashlib
import time
import sys
import threading
import os
from typing import Dict, List, Optional, Any, Union, Callable
from .messages import *
from .schema import *

class Extension(object):
    
    def __init__(self, name: str, secret: str):
        self._name: str = name
        self._secret: str = secret
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
            data = flask.request.get_data()
            if flask.request.headers.get('Content-Encoding', '') == 'gzip':
                data = gzip.decompress(data)
            if self._isLogRequest:
                self.log(f"request: {data}")
            if not self._verifyOrigin(data, sig):
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
                    resp = Response(error = str(e))
                    self.logCritical(f"exception: {str(e)}")
                status = 200
                if resp.error:
                    status = 500 if resp.is_retriable else 503
                resp = json.dumps(resp.toJSON())
                if self._isLogResponse:
                    self.log(f"response: {resp}")
                return resp, status
            resp = json.dumps(Response(error = "invalid request").toJSON())
            if self._isLogResponse:
                self.log(f"response: {resp}")
            return resp, 400

        self.init()

    def getApp(self) -> flask.Flask:
        return self._app

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
            return handler(sdk, msg.msg_request.data, msg.msg_request.conf, msg.msg_request.resState)
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