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

class OrgAccessData(object):
    def __init__(self, oid, jwt):
        self.oid = oid
        self.jwt = jwt

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
    def __init__(self, error = None, data = None):
        self.error = error
        self.data = data
    
    def toJSON(self):
        ret = {
            'version': _PROTOCOL_VERSION,
        }
        if self.error:
            ret['error'] = self.error
        if self.data:
            ret['data'] = {}
        else:
            ret['data'] = self.data
        return ret
