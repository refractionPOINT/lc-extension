from . import _PROTOCOL_VERSION

class Message(object):
    def __init__(self, data):
        self.version = data.get('version', None)
        self.idempotent_key = data.get('idempotency_key', None)

        self.msg_heart_beat = None
        self.msg_error_report = data.get('error_report', None)
        self.msg_config_validation = data.get('conf_validation', None)
        self.msg_schema_request = data.get('schema_request', None)
        self.msg_request = data.get('request', None)
        self.msg_event = data.get('event', None)

        d = data.get('heartbeat', None)
        if d is not None:
            self.msg_heart_beat = MessageHeartBeat(d)
        d = data.get('error_report', None)
        if d is not None:
            self.msg_error_report = MessageErrorReport(d)
        d = data.get('conf_validation', None)
        if d is not None:
            self.msg_config_validation = MessageConfigValidation(d)
        d = data.get('schema_request', None)
        if d is not None:
            self.msg_schema_request = MessageSchemaRequest(d)
        d = data.get('request', None)
        if d is not None:
            self.msg_request = MessageRequest(d)
        d = data.get('event', None)
        if d is not None:
            self.msg_event = MessageEvent(d)

    def __str__(self):
        return f"Message(version={self.version},idempotent_key={self.idempotent_key},msg_heart_beat={self.msg_heart_beat},msg_error_report={self.msg_error_report},msg_config_validation={self.msg_config_validation},msg_schema_request={self.msg_schema_request},msg_request={self.msg_request},msg_event={self.msg_event})"

class OrgAccessData(object):
    def __init__(self, orgData):
        self.oid = orgData.get( 'oid', None )
        self.jwt = orgData.get( 'jwt', None )
        self.ident = orgData.get( 'ident', None )

class MessageHeartBeat(object):
    def __init__(self, data):
        pass

class MessageErrorReport(object):
    def __init__(self, data):
        self.error = data.get('error', None)
        self.oid = data.get('oid', None)

class MessageConfigValidation(object):
    def __init__(self, data):
        self.org_access_data = None
        od = data.get('org', None)
        if od:
            self.org_access_data = OrgAccessData(od)
        self.conf = data.get('conf', None)
        

class MessageRequest(object):
    def __init__(self, data):
        self.org_access_data = None
        od = data.get('org', None)
        if od:
            self.org_access_data = OrgAccessData(od)
        self.action = data.get('action', None)
        self.data = data.get('data', None)
        self.conf = data.get('config', None)
        self.resState = data.get('resource_state', None)
        self.inv_id = data.get('inv_id', None)

class MessageEvent(object):
    def __init__(self, data):
        self.org_access_data = None
        od = data.get('org', None)
        if od:
            self.org_access_data = OrgAccessData(od)
        self.event_name = data.get('event_name', None)
        self.data = data.get('data', None)
        self.conf = data.get('config', None)

class MessageSchemaRequest(object):
    def __init__(self, data):
        pass

class Response(object):
    def __init__(self, error = None, data = None, metrics = None):
        self.error = error
        self.data = data
        self.metrics = metrics
    
    def toJSON(self):
        ret = {
            'version': _PROTOCOL_VERSION,
        }
        if self.error:
            ret['error'] = self.error
        if not self.data:
            ret['data'] = {}
        else:
            ret['data'] = self.data
        if self.metrics:
            ret['metrics'] = self.metrics.serialize()
        return ret
