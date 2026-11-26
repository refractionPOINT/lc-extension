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


def post(body: bytes, env=None, ext=_SecretExtension):
    '''POST body to an extension with the env set; return the response and
    everything the extension wrote to stdout and stderr.'''
    out = io.StringIO()
    with mock.patch.dict(os.environ, env or {}), mock.patch('limacharlie.Manager'):
        client = ext('log-redaction-test', SECRET).getApp().test_client()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(out):
            resp = client.post('/', data=body, headers={'lc-ext-sig': sign(body)})
    return resp, out.getvalue()


def post_logged(body: bytes) -> str:
    return post(body, {'LOG_REQUEST': '1'})[1]


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

    def test_schema_free_sections_are_redacted(self):
        body = json.dumps({'version': 20221218, 'idempotency_key': 'idem-1',
            'fleet_request': {'action': 'run_it', 'oids': ['oid-1'], 'data': {'key': FAKE_SECRET_PARAM}},
            'event': {'org': {'oid': 'oid-1', 'jwt': FAKE_ORG_JWT}, 'event_name': 'subscribe', 'data': {'x': FAKE_UNKNOWN_PARAM}},
            'request': {'org': {'oid': 'oid-1', 'jwt': FAKE_ORG_JWT}, 'action': 'run_it', 'data': {'name': 'visible-name'},
                        'resource_state': {'s': FAKE_NESTED_SECRET}, 'acl': {'scopes': [FAKE_CONFIG_SECRET]}},
        }).encode()
        captured = post_logged(body)
        self.assertNoFake(captured)
        for visible in ('idem-1', 'subscribe', 'run_it', 'oid-1', 'visible-name'):
            self.assertIn(visible, captured)

    def test_malformed_action_answers_the_same_with_logging(self):
        body = json.dumps({'version': 20221218, 'request': {
            'org': {'oid': 'oid-1', 'jwt': FAKE_ORG_JWT}, 'action': ['not', 'a', 'string'], 'data': {'key': FAKE_SECRET_PARAM},
        }}).encode()
        plain, _ = post(body)
        logged, captured = post(body, {'LOG_REQUEST': '1'})
        self.assertEqual(plain.status_code, logged.status_code)
        self.assertNotEqual(logged.status_code, 500)
        self.assertNoFake(captured)

    def test_handler_exception_and_response_log_are_scrubbed(self):
        class _Raising(_SecretExtension):
            def init(self):
                super().init()
                def run_it(sdk, data, conf, res):
                    if data.get('raise'):
                        raise RuntimeError(f"auth failed with {data['key']} and {conf['api_key']}")
                    return lcextension.Response(data={'echo': data['key']})
                self.requestHandlers = {'run_it': run_it}
        for raise_it in (True, False):
            body = json.dumps({'version': 20221218, 'request': {
                'org': {'oid': 'oid-1', 'jwt': FAKE_ORG_JWT}, 'action': 'run_it',
                'data': {'key': FAKE_SECRET_PARAM, 'raise': raise_it} if raise_it else {'key': FAKE_SECRET_PARAM},
                'config': {'api_key': FAKE_CONFIG_SECRET},
            }}).encode()
            resp, captured = post(body, {'LOG_RESPONSE': '1'}, _Raising)
            self.assertIn('response:', captured)
            self.assertNoFake(captured)
            if raise_it:
                self.assertNoFake(resp.get_data(as_text=True))
                self.assertIn(lcextension.ext.REDACTED_MARKER, resp.get_data(as_text=True))

    def test_returned_error_escaped_and_mutated_values_are_scrubbed(self):
        quoted = 'FAKE"QUOTED\\SECRET-0006'
        class _Echo(_SecretExtension):
            def init(self):
                super().init()
                def run_it(sdk, data, conf, res):
                    key = data.pop('key')
                    nested = json.loads(data['nested'])['token']
                    if data.get('raise'):
                        raise RuntimeError(json.dumps({'k': key, 'n': nested}))
                    return lcextension.Response(error=f'refused {key} {nested}', data={'echo': key})
                self.requestHandlers = {'run_it': run_it}
        for raise_it in (False, True):
            data = {'key': quoted, 'nested': json.dumps({'token': FAKE_NESTED_SECRET})}
            if raise_it:
                data['raise'] = True
            body = json.dumps({'version': 20221218, 'request': {
                'org': {'oid': 'oid-1', 'jwt': FAKE_ORG_JWT}, 'action': 'run_it', 'data': data,
            }}).encode()
            resp, captured = post(body, {'LOG_RESPONSE': '1'}, _Echo)
            answer = json.loads(resp.get_data(as_text=True))
            for text in (captured, answer.get('error', '')):
                self.assertNotIn(FAKE_NESTED_SECRET, text)
                self.assertNotIn(quoted, text)
                self.assertNotIn(json.dumps(quoted)[1:-1], text)
            self.assertIn(lcextension.ext.REDACTED_MARKER, answer['error'])

    def test_short_secret_withholds_the_text(self):
        self.assertNotIn('123', lcextension.ext._scrubSecrets('pin 123 refused', ['123']))


if __name__ == '__main__':
    unittest.main()
