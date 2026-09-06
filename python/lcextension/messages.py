from . import _PROTOCOL_VERSION
from typing import Optional, Dict, List, Any, Union

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
        

# Reserved tag namespace that marks a resource's content as restricted by a
# LimaCharlie resource ACL: a resource tagged "acl:<scope>" is only readable
# by principals holding the scope <scope>.
ACL_TAG_PREFIX = 'acl:'

# Known values for ACL.source. Informational only, they never affect the
# access decision.
ACL_SOURCE_USER = 'user'
ACL_SOURCE_IMPERSONATED = 'impersonated'
ACL_SOURCE_DR = 'dr'
ACL_SOURCE_CONTINUATION = 'continuation'
ACL_SOURCE_INTERNAL = 'internal'

def normalize_acl_scope(scope: Any) -> str:
    '''Normalize a scope name (or tag) the way the platform does: lowercased
    and trimmed of surrounding whitespace.'''
    return str(scope).strip().lower()

def acl_scope_from_tag(tag: Any) -> Optional[str]:
    '''Return the normalized scope name carried by an "acl:" tag, or None if
    the tag is outside the "acl:" namespace. A bare "acl:" tag yields an empty
    scope name; since no scope can be named "", such a tag always locks the
    resource in ACL.allows().'''
    n = normalize_acl_scope(tag)
    if not n.startswith(ACL_TAG_PREFIX):
        return None
    return n[len(ACL_TAG_PREFIX):].strip()

class ACL(object):
    '''The platform-set block LimaCharlie attaches to every request envelope,
    describing what the initiator of the request may access with respect to
    resource ACLs (resources tagged "acl:<scope>").

    It is set by the platform at the top level of the request envelope, never
    inside the user-controlled request data, and is covered by the same HMAC
    signature as the rest of the body. Never trust an "acl" value found
    anywhere else.

    The object is always usable, even when the envelope did not carry an ACL
    block (for example when talking to an older LimaCharlie extension
    manager). In that case ``present`` is False and the object FAILS CLOSED
    for extensions that enforce ACLs: ``scopes`` is empty, ``is_global`` is
    False and ``enforce`` is True, so ``allows()`` returns False for any
    resource carrying an "acl:" tag and True for resources without one. This
    way an extension can never leak a restricted resource just because the
    platform did not tell it what the initiator holds.

    Attributes:
        scopes: scope names the initiator holds, without the "acl:" prefix.
        is_global: True when the initiator holds "access.global" and may
            access every resource regardless of its ACL tags.
        enforce: True when the org has at least one enabled ACL scope. When
            False no resource is restricted and every ACL check may be skipped.
        source: where the request came from (see ACL_SOURCE_*), informational.
        present: whether the envelope actually carried the block.
    '''
    def __init__(self, data: Optional[Dict[str, Any]] = None):
        self.present: bool = data is not None
        if data is None:
            self.scopes: List[str] = []
            self.is_global: bool = False
            self.enforce: bool = True
            self.source: Optional[str] = None
            return
        scopes = data.get('scopes', None) or []
        self.scopes = [str(s) for s in scopes]
        self.is_global = bool(data.get('global', False))
        self.enforce = bool(data.get('enforce', False))
        self.source = data.get('source', None)

    def allows(self, tags: Optional[Union[List[Any], str]]) -> bool:
        '''Report whether the initiator of the request may access a resource
        carrying the given tags.

        Rules:
        - global initiators are allowed everywhere;
        - when ``enforce`` is False nothing is restricted and everything is allowed;
        - otherwise every "acl:<scope>" tag on the resource must name a scope
          the initiator holds (AND rule). A bare "acl:" tag or a scope the
          initiator does not hold locks the resource. Tags outside the "acl:"
          namespace are ignored.

        Tags and scope names are compared case-insensitively with surrounding
        whitespace trimmed, and an entry containing commas is treated as a
        list of tags (the platform's tag list separator).

        ``allows()`` only ever restricts: it is the second half of a decision
        and must be combined with the extension's normal permission checks,
        never used to grant access the org permissions would not already allow.
        '''
        if self.is_global:
            return True
        if not self.enforce:
            return True
        if tags is None:
            return True
        if isinstance(tags, str):
            tags = [tags]
        held = None
        for entry in tags:
            for tag in str(entry).split(','):
                scope = acl_scope_from_tag(tag)
                if scope is None:
                    continue
                if held is None:
                    held = set(normalize_acl_scope(s) for s in self.scopes)
                if scope not in held:
                    return False
        return True

    def serialize(self) -> Optional[Dict[str, Any]]:
        '''Return the block as it was received on the envelope, or None if the
        envelope did not carry one. Meant for intermediaries forwarding the
        request to another extension.'''
        if not self.present:
            return None
        return {
            'scopes': list(self.scopes),
            'global': self.is_global,
            'enforce': self.enforce,
            'source': self.source,
        }

    def __str__(self) -> str:
        return f"ACL(present={self.present},scopes={self.scopes},is_global={self.is_global},enforce={self.enforce},source={self.source})"

    __repr__ = __str__

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
        # Platform-set, always usable; fails closed when absent (see ACL).
        self.acl: ACL = ACL(data.get('acl', None))

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
