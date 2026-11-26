import contextlib
import hashlib
import hmac
import io
import json
import os
import sys
import unittest
from unittest import mock

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

import lcextension

SECRET = 'test-secret'
# Obviously fake values: none is a real key or token.
FAKE_SECRET_PARAM = 'FAKE-SECRET-PARAM-VALUE-0001'
FAKE_NESTED_SECRET = 'FAKE-NESTED-SECRET-VALUE-0002'
FAKE_UNKNOWN_PARAM = 'FAKE-UNKNOWN-PARAM-VALUE-0003'
FAKE_ORG_JWT = 'FAKE-ORG-JWT-0004'
FAKE_CONFIG_SECRET = 'FAKE-CONFIG-SECRET-0005'
FAKES = (FAKE_SECRET_PARAM, FAKE_NESTED_SECRET, FAKE_UNKNOWN_PARAM, FAKE_ORG_JWT, FAKE_CONFIG_SECRET)


class _SecretExtension(lcextension.Extension):
    def init(self):
        self.configSchema = lcextension.SchemaObject(Fields={
            'api_key': lcextension.SchemaElement(DataType=lcextension.SchemaDataTypes.Secret),
            'region': lcextension.SchemaElement(DataType=lcextension.SchemaDataTypes.String),
        })
        self.requestSchema.Actions = {
            'run_it': lcextension.RequestSchema(ParameterDefinitions=lcextension.SchemaObject(Fields={
                'name': lcextension.SchemaElement(DataType=lcextension.SchemaDataTypes.String),
                'key': lcextension.SchemaElement(DataType=lcextension.SchemaDataTypes.Secret),
                'nested': lcextension.SchemaElement(DataType=lcextension.SchemaDataTypes.Object, ObjectSchema=lcextension.SchemaObject(Fields={
                    'token': lcextension.SchemaElement(DataType=lcextension.SchemaDataTypes.Secret),
                    'label': lcextension.SchemaElement(DataType=lcextension.SchemaDataTypes.String),
                })),
            })),
        }
        self.requestHandlers = {'run_it': lambda sdk, data, conf, res: lcextension.Response()}
        self.eventHandlers = {}


def sign(body: bytes) -> str:
    return hmac.new(SECRET.encode(), body, hashlib.sha256).hexdigest()


def post_logged(body: bytes) -> str:
    '''POST body to an extension with LOG_REQUEST set and return everything it
    wrote to stdout and stderr.'''
    out = io.StringIO()
    with mock.patch.dict(os.environ, {'LOG_REQUEST': '1'}), mock.patch('limacharlie.Manager'):
        client = _SecretExtension('log-redaction-test', SECRET).getApp().test_client()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(out):
            client.post('/', data=body, headers={'lc-ext-sig': sign(body)})
    return out.getvalue()


class LogRequestRedactionTest(unittest.TestCase):
    def assertNoFake(self, captured):
        for v in FAKES:
            self.assertNotIn(v, captured)

    def test_request_secrets_are_redacted(self):
        body = json.dumps({'version': 20221218, 'request': {
            'org': {'oid': 'oid-1', 'jwt': FAKE_ORG_JWT},
            'action': 'run_it',
            'data': {'name': 'visible-name', 'key': FAKE_SECRET_PARAM, 'kee': FAKE_UNKNOWN_PARAM,
                     'nested': {'token': FAKE_NESTED_SECRET, 'label': 'visible-label'}},
            'config': {'api_key': FAKE_CONFIG_SECRET, 'region': 'visible-region'},
        }}).encode()
        captured = post_logged(body)
        self.assertIn('request:', captured)
        self.assertNoFake(captured)
        self.assertIn(lcextension.ext.REDACTED_MARKER, captured)
        for visible in ('visible-name', 'visible-label', 'visible-region'):
            self.assertIn(visible, captured)

    def test_unknown_action_shows_no_parameter_value(self):
        body = json.dumps({'version': 20221218, 'request': {
            'org': {'oid': 'oid-1', 'jwt': FAKE_ORG_JWT},
            'action': 'no_such_action',
            'data': {'name': FAKE_UNKNOWN_PARAM, 'key': FAKE_SECRET_PARAM},
        }}).encode()
        captured = post_logged(body)
        self.assertIn('request:', captured)
        self.assertNoFake(captured)

    def test_event_and_config_validation_redact_org_jwt_and_config(self):
        for section in ('event', 'conf_validation'):
            body = json.dumps({'version': 20221218, section: {
                'org': {'oid': 'oid-1', 'jwt': FAKE_ORG_JWT},
                'event_name': 'subscribe',
                'conf': {'api_key': FAKE_CONFIG_SECRET},
                'config': {'api_key': FAKE_CONFIG_SECRET},
            }}).encode()
            captured = post_logged(body)
            self.assertIn('request:', captured)
            self.assertNoFake(captured)

    def test_body_that_is_not_json_is_not_logged(self):
        body = ('not json ' + FAKE_SECRET_PARAM).encode()
        captured = post_logged(body)
        self.assertIn('request:', captured)
        self.assertNoFake(captured)


if __name__ == '__main__':
    unittest.main()
