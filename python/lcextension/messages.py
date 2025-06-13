from . import _PROTOCOL_VERSION
from typing import Optional, Dict, List, Any

class Message(object):
    def __init__(self, data: Dict[str, Any]):
        self.version: Optional[int] = data.get('version', None)
        self.idempotent_key: Optional[str] = data.get('idempotency_key', None)

        self.msg_heart_beat: Optional[MessageHeartBeat] = None
        self.msg_error_report: Optional[MessageErrorReport] = data.get('error_report', None)
        self.msg_config_validation: Optional[MessageConfigValidation] = data.get('conf_validation', None)
        self.msg_schema_request: Optional[MessageSchemaRequest] = data.get('schema_request', None)
        self.msg_request: Optional[MessageRequest] = data.get('request', None)
        self.msg_event: Optional[MessageEvent] = data.get('event', None)

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

    def __str__(self) -> str:
        return f"Message(version={self.version},idempotent_key={self.idempotent_key},msg_heart_beat={self.msg_heart_beat},msg_error_report={self.msg_error_report},msg_config_validation={self.msg_config_validation},msg_schema_request={self.msg_schema_request},msg_request={self.msg_request},msg_event={self.msg_event})"

class OrgAccessData(object):
    def __init__(self, orgData: Dict[str, Any]):
        self.oid: Optional[str] = orgData.get('oid', None)
        self.jwt: Optional[str] = orgData.get('jwt', None)
        self.ident: Optional[str] = orgData.get('ident', None)

class MessageHeartBeat(object):
    def __init__(self, data: Dict[str, Any]):
        pass

class MessageErrorReport(object):
    def __init__(self, data: Dict[str, Any]):
        self.error: Optional[str] = data.get('error', None)
        self.oid: Optional[str] = data.get('oid', None)

class MessageConfigValidation(object):
    def __init__(self, data: Dict[str, Any]):
        self.org_access_data: Optional[OrgAccessData] = None
        od = data.get('org', None)
        if od:
            self.org_access_data = OrgAccessData(od)
        self.conf: Optional[Dict[str, Any]] = data.get('conf', None)
        

class MessageRequest(object):
    def __init__(self, data: Dict[str, Any]):
        self.org_access_data: Optional[OrgAccessData] = None
        od = data.get('org', None)
        if od:
            self.org_access_data = OrgAccessData(od)
        self.action: Optional[str] = data.get('action', None)
        self.data: Optional[Dict[str, Any]] = data.get('data', None)
        self.conf: Optional[Dict[str, Any]] = data.get('config', None)
        self.resState: Optional[Dict[str, Any]] = data.get('resource_state', None)
        self.inv_id: Optional[str] = data.get('inv_id', None)

class MessageEvent(object):
    def __init__(self, data: Dict[str, Any]):
        self.org_access_data: Optional[OrgAccessData] = None
        od = data.get('org', None)
        if od:
            self.org_access_data = OrgAccessData(od)
        self.event_name: Optional[str] = data.get('event_name', None)
        self.data: Optional[Dict[str, Any]] = data.get('data', None)
        self.conf: Optional[Dict[str, Any]] = data.get('config', None)

class MessageSchemaRequest(object):
    def __init__(self, data: Dict[str, Any]):
        pass


class ContinuationRequest(object):
    def __init__(self, in_delay_sec: int, action: str, state: Dict[str, Any]):
        self.in_delay_sec: int = in_delay_sec
        self.action: str = action
        self.state: Dict[str, Any] = state

    def serialize(self) -> Dict[str, Any]:
        return {
            'in_delay_sec': self.in_delay_sec,
            'action': self.action,
            'state': self.state,
        }


class Response(object):
    def __init__(self, 
                 error: Optional[str] = None, 
                 data: Optional[Dict[str, Any]] = None, 
                 metrics: Optional[Any] = None, 
                 continuations: Optional[List[ContinuationRequest]] = None,
                 is_retriable: Optional[bool] = None):
        self.error: Optional[str] = error
        self.data: Optional[Dict[str, Any]] = data
        self.metrics: Optional[Any] = metrics
        self.continuations: List[ContinuationRequest] = continuations if continuations else []
        self.is_retriable: Optional[bool] = is_retriable
    
    def toJSON(self) -> Dict[str, Any]:
        ret: Dict[str, Any] = {
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
        if self.continuations:
            ret['continuations'] = [c.serialize() for c in self.continuations]
        ret['retriable'] = self.is_retriable or self.is_retriable is None
        return ret
