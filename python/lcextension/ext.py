import limacharlie
import flask
import gzip
import json
import hmac
import hashlib
import time
import sys
import threading
from .messages import *
from .schema import *

class Extension(object):
    
    def __init__(self, name, secret):
        self._name = name
        self._secret = secret
        self._lock = threading.Lock()
        self.configSchema = SchemaObject()
        self.requestSchema = RequestSchemas()
        self.requiredEvents = []

        self._app = flask.Flask(self._name)

        @self._app.post('/')
        def _handler():
            sig = flask.request.headers.get('lc-ext-sig', None)
            if not sig:
                return {}, 200
            data = flask.request.data
            if flask.request.headers.get('Content-Type', '') == 'gzip':
                data = gzip.decompress(data)
            if not self._verifyOrigin(data, sig):
                return {"error": "invalid signature"}, 401
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
                    status = 500
                return resp.toJSON(), status
            return {"error": "invalid request"}, 400

        self.init()

    def getApp(self):
        return self._app

    def _verifyOrigin(self, data, signature):
        if self._secret is None:
            return True
        if isinstance(data, str):
            data = data.encode()
        if isinstance(signature, bytes):
            signature = signature.decode()
        expected = hmac.new(self._originSecret, msg = data, digestmod = hashlib.sha256).hexdigest()
        return hmac.compare_digest(expected, signature)

    def _extRequestHandler(self, data):
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
            return handler(sdk, msg.msg_request.data, msg.msg_request.conf)
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
            return {
                'config_schema': self.configSchema.serialize(),
                'request_schema': self.requestSchema.serialize(),
                'required_events': self.requiredEvents,
            }
        return Response(error = 'no data in request')
    
    def _handleEvent(self, sdk, data, conf):
        return Response()
    
    # Helper functions, feel free to override.
    def log( self, msg, data = None ):
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

    def logCritical( self, msg ):
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