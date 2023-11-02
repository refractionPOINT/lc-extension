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
from .messages import *
from .schema import *
import yaml 
from .jobs import *
import traceback 

class Extension(object):
    
    def __init__(self, name, secret):
        self._name = name
        self._secret = secret
        self._lock = threading.Lock()
        self.viewSchemas = []
        self.configSchema = SchemaObject()
        self.requestSchema = RequestSchemas()
        self.requiredEvents = []

        self._isLogRequest = os.environ.get("LOG_REQUEST", "") != ""
        self._isLogResponse = os.environ.get("LOG_RESPONSE", "") != ""

        self._handlers = {
            'subscribe' : self._handleSubscribe,
            'unsubscribe' : self._handleUnsubscribe,
            'detection' : self._onDetection,
            'org_per_1h' : self._every1HourPerOrg,
        }

        self._rootInvestigationId = "ext-%s-ex" % ( self._name, )
        self._interactiveRule = yaml.safe_load( f'''
            {self._rootInvestigationId}:
              namespace: managed
              detect:
                op: and
                rules:
                  - op: starts with
                    path: routing/investigation_id
                    value: {self._rootInvestigationId}
                  - op: is
                    not: true
                    path: routing/event_type
                    value: CLOUD_NOTIFICATION
              respond:
                - action: report
                  name: __{self._rootInvestigationId}
        ''' )

        # Get all the detections, we'll do the routing
        # to the right callbacks internally.
        self.subscribeToDetect( "__%s" % ( self._rootInvestigationId, ) )

        # We make a table of all possible callbacks, which we
        # limit to methods of this service object. We map each
        # one to a hash(shared_secret+callbackName) which we use
        # when we issue the tasking. This avoids user having to
        # manually register their callbacks ahead of time, and it
        # also prevents other entities than this service from
        # faking callbacks.
        # This may seem complex, but remember that this has to
        # work statelessly since we cannot guarantee the response
        # will come back to the same instance of the service.
        self._callbackHashes = {}
        for elem in dir( self ):
            if elem.startswith( '_' ):
                continue
            if not callable( getattr( self, elem ) ):
                continue
            self._callbackHashes[ self._getCallbackKey( elem ) ] = getattr( self, elem )
            
        self._app = flask.Flask(self._name)

        @self._app.post('/')
        def _handler():
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
                    status = 500
                resp = json.dumps(resp.toJSON())
                if self._isLogResponse:
                    self.log(f"response: {resp}")
                return resp, status
            resp = json.dumps(Response(error = "invalid request").toJSON())
            if self._isLogResponse:
                self.log(f"response: {resp}")
            return resp, 400

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
        expected = hmac.new(self._secret.encode(), msg = data, digestmod = hashlib.sha256).hexdigest()
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
            return Response(data = {
                'views': [v.serialize() for v in self.viewSchemas],
                'config_schema': self.configSchema.serialize(),
                'request_schema': self.requestSchema.serialize(),
                'required_events': self.requiredEvents,
            })
        return Response(error = 'no data in request')
    
    def _handleEvent(self, sdk, data, conf):
        jwt = data.get( 'jwt', None )
        oid = data.get( 'oid', None )
        event_name = data.get( 'event_name', None )

        request = MessageRequest( event_name = event_name, data = data )

        # If the event_name is 'request' and the service specifies
        # some request parameter definitions we'll do some
        # validation on the parameters.
        if 'request' == event_name and 0 != len( self._supportedRequestParameters ):
            for k, v in request.data.items():
                if v is None:
                    continue
                definition = self._supportedRequestParameters.get( k, None )
                if definition is None:
                    continue
                validationResult = self._validateRequestParameter( definition, v )
                if validationResult is None:
                    continue
                return Response( error = True,
                                      data = "invalid parameter %s: %s" % ( k, validationResult ) )
            for k, v in self._supportedRequestParameters.items():
                if not v.get( 'is_required', False ):
                    continue
                if request.data.get( k, None ) is None:
                    return Response( error = True,
                                          error = "missing parameter %s" % ( k, ) )

        handler = self._handlers.get( event_name, None )
        if handler is None:
            return Response( error = True,
                              data = { 'error' : 'not implemented' } )

        lcApi = None
        if oid is not None and jwt is not None:
            invId = str( uuid.uuid4() )
            # We support a special function to wrap the LC API
            # if needed to provide some added magic.
            lcApi = getattr( self, 'wrapSdk' )( oid = oid, jwt = jwt, inv_id = invId )

        try:
            with self._lock:
                self._nCallsInProgress += 1
            resp = handler( lcApi, oid, request )
            if resp is True:
                # Shotcut for simple success.
                resp = Response( error = False )
            elif resp is False:
                # Shortcut for simple failure no retry.
                resp = Response( error = True )
            elif resp is None:
                # Shortcut for simple failure with retry.
                resp = Response( error = True )
            elif not isinstance( resp, dict ):
                self.logCritical( 'no valid response specified in %s, assuming success' % ( event_name, ) )
                resp = Response( error = False,
                                      data = {} )

            return resp
        except:
            exc = traceback.format_exc()
            self.logCritical( exc )
            return Response( error = True,
                                  data = { 'exception' : exc } )
        finally:
            with self._lock:
                self._nCallsInProgress -= 1
            now = time.time()
    
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

    def subscribeToDetect( self, detectName ):
        '''Subscribe this service to the specific detection names of all subscribed orgs.

        :param detectName: name of the detection to subscribe to.
        '''
        self._detectSubscribed.add( detectName )
        
    def _getCallbackKey( self, cbName ):
        return hashlib.md5( ( "%s/%s" % ( self._originSecret, cbName ) ).encode() ).hexdigest()[ : 8 ]

    def _onDetection( self, sdk, oid, request ):
        # If the detect does not have the root investigation id
        # it means it's destined for the real service.
        event = request.data.get( 'detect', {} )
        invId = event.get( 'routing', {} ).get( 'investigation_id', '' )
        if not invId.startswith( self._rootInvestigationId ):
            return self.onDetection( sdk, oid, request )

        # This is an interactive response.
        _, callbackId, jobId, ctx = invId.split( '/', 3 )

        callback = self._callbackHashes.get( callbackId, None )
        if callback is None:
            self.logCritical( "Unknown callback: %s" % ( callbackId, ) )
            return False

        job = None
        if jobId != '':
            job = Job( jobId )

        return callback( sdk, oid, event, job, ctx )

    def _every1HourPerOrg( self, sdk, oid, request ):
        ret = True

        # Check if the user has implemented the callback.
        # If yes, call it and use its return value.
        originalCb = getattr( self, 'every1HourPerOrg' )
        if not hasattr( originalCb, 'is_not_supported' ):
            ret = self.every1HourPerOrg( sdk, oid, request )

        self._applyInteractiveRule( sdk )

        return ret

    def _handleSubscribe( self, sdk, oid, request ):
        ret = True

        # Check if the user has implemented the callback.
        # If yes, call it and use its return value.
        originalCb = getattr( self, 'handleSubscribe' )
        if not hasattr( originalCb, 'is_not_supported' ):
            ret = self.handleSubscribe( sdk, oid, request )

        self._applyInteractiveRule( sdk )

        return ret

    def _handleUnsubscribe( self, sdk, oid, request ):
        ret = True

        # Check if the user has implemented the callback.
        # If yes, call it and use its return value.
        originalCb = getattr( self, 'handleUnsubscribe' )
        if not hasattr( originalCb, 'is_not_supported' ):
            ret = self.handleUnsubscribe( sdk, oid, request )

        self._removeInteractiveRule( sdk )

        return ret

    def _applyInteractiveRule( self, sdk ):
        # Sync in our D&R rule but don't use "isForce"
        # since the user may also have their own D&R rules
        sync = limacharlie.Sync( manager = sdk )
        rules = {
            'rules' : self._interactiveRule,
        }
        sync.pushRules( rules )

    def _removeInteractiveRule( self, sdk ):
        for ruleName, rule in self._interactiveRule.items():
            try:
                sdk.del_rule( ruleName, namespace = rule.get( 'namespace', None ) )
            except:
                self.logCritical( traceback.format_exc() )

    def wrapSdk( self, *args, **kwargs ):
        this = self

        class _interactiveSensor( limacharlie.Sensor.Sensor ):
            def task( self, tasks, inv_id = None, callback = None, job = None, ctx = '' ):
                if callback is not None:
                    return self._taskWithCallback( tasks, callback, job, ctx )
                return super().task( tasks, inv_id = inv_id )

            def _taskWithCallback( self, cmd, callback, job, ctx ):
                sensor = self._manager.sensor( self.sid )
                cbHash = this._getCallbackKey( callback.__name__ )
                jobId = ''
                if job is not None:
                    jobId = job.getId()
                return sensor.task( cmd, inv_id = "%s/%s/%s/%s" % ( this._rootInvestigationId, cbHash, jobId, ctx ) )

        class _interactiveManager( limacharlie.Manager ):
            def sensor( self, sid, inv_id = None ):
                s = _interactiveSensor( self, sid )
                if inv_id is not None:
                    s.setInvId( inv_id )
                elif self._inv_id is not None:
                    s.setInvId( self._inv_id )
                return s

        return _interactiveManager( *args, **kwargs )
    
    def responseNotImplemented( self ):
        '''Generate a pre-made response indicating the callback is not implemented.
        '''
        return self.response( isSuccess = False,
                            isDoRetry = False,
                            data = { 'error' : 'not implemented' } )

    def _unsupportedFunc( method ):
        method.is_not_supported = True
        return method

    @_unsupportedFunc
    def handleSubscribe( self, sdk, oid, request ):
        '''Called when a new organization subscribes to this service.
        '''
        return self.responseNotImplemented()

    @_unsupportedFunc
    def handleUnsubscribe( self, sdk, oid, request ):
        '''Called when an organization unsubscribes from this service.
        '''
        return self.responseNotImplemented()

    @_unsupportedFunc
    def onDetection( self, sdk, oid, request ):
        '''Called when a detection is received for an organization.
        '''
        return self.responseNotImplemented()    

    # LC Service Cron-like Functions
    @_unsupportedFunc
    def every1HourPerOrg( self, sdk, oid, request ):
        '''Called every hour for every organization subscribed.
        '''
        return self.responseNotImplemented()