import hashlib
import hmac
import json
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

import lcextension
from lcextension.messages import ACL, Message, MessageRequest, ACL_SOURCE_USER, acl_scope_from_tag
from lcextension.ext import _acceptsACLArgument


def enforcing(*scopes):
    return ACL({'scopes': list(scopes), 'global': False, 'enforce': True, 'source': ACL_SOURCE_USER})


class TestACLAllows(unittest.TestCase):
    def test_table(self):
        cases = [
            ('untagged resource', enforcing('hr'), ['prod', 'windows'], True),
            ('no tags at all', enforcing('hr'), [], True),
            ('tags is None', enforcing('hr'), None, True),
            ('member of the scope', enforcing('hr'), ['acl:hr'], True),
            ('not a member', enforcing('hr'), ['acl:finance'], False),
            ('AND over two scopes, both held', enforcing('hr', 'finance'), ['acl:hr', 'acl:finance'], True),
            ('AND over two scopes, one missing', enforcing('hr'), ['acl:hr', 'acl:finance'], False),
            ('unknown scope locks', enforcing('hr'), ['acl:does-not-exist'], False),
            ('bare acl: locks', enforcing('hr'), ['acl:'], False),
            ('bare acl: locks even with whitespace', enforcing('hr'), [' ACL:  '], False),
            ('mixed case and whitespace on tag', enforcing('hr'), ['  ACL:HR  '], True),
            ('mixed case and whitespace on scope', enforcing('  HR '), ['acl:hr'], True),
            ('comma-joined entry, all held', enforcing('hr', 'finance'), ['prod,acl:hr, acl:finance'], True),
            ('comma-joined entry, one missing', enforcing('hr'), ['acl:hr,acl:finance'], False),
            ('single string of tags', enforcing('hr'), 'prod,acl:hr', True),
            ('non-acl tags mixed in are ignored', enforcing('hr'), ['prod', 'acl:hr', 'win'], True),
            ('prefix must be exact namespace', enforcing('hr'), ['acls:hr', 'xacl:hr'], True),
            ('empty scopes with acl tag locks', enforcing(), ['acl:hr'], False),
            ('global allows everything', ACL({'global': True, 'enforce': True}), ['acl:hr', 'acl:'], True),
            ('enforce=false allows everything', ACL({'enforce': False}), ['acl:hr', 'acl:'], True),
            ('absent block: untagged allowed', ACL(None), ['prod'], True),
            ('absent block: tagged locked', ACL(None), ['acl:hr'], False),
            ('absent block: bare acl: locked', ACL(None), ['acl:'], False),
        ]
        for name, acl, tags, want in cases:
            with self.subTest(name):
                self.assertEqual(acl.allows(tags), want, f"{name}: {acl}")

    def test_absent_is_fail_closed(self):
        acl = ACL(None)
        self.assertFalse(acl.present)
        self.assertEqual(acl.scopes, [])
        self.assertFalse(acl.is_global)
        self.assertTrue(acl.enforce)
        self.assertIsNone(acl.source)
        self.assertIsNone(acl.serialize())

    def test_present_round_trip(self):
        block = {'scopes': ['hr', 'finance'], 'global': False, 'enforce': True, 'source': 'dr'}
        acl = ACL(block)
        self.assertTrue(acl.present)
        self.assertEqual(acl.serialize(), block)
        # An explicit enforce=false block is present and must not be mistaken
        # for the fail-closed default.
        acl = ACL({'enforce': False})
        self.assertTrue(acl.present)
        self.assertFalse(acl.enforce)

    def test_scope_from_tag(self):
        self.assertEqual(acl_scope_from_tag('acl:hr'), 'hr')
        self.assertEqual(acl_scope_from_tag(' ACL:HR '), 'hr')
        self.assertEqual(acl_scope_from_tag('acl:'), '')
        self.assertEqual(acl_scope_from_tag('acl: '), '')
        self.assertIsNone(acl_scope_from_tag('prod'))
        self.assertIsNone(acl_scope_from_tag(''))
        self.assertIsNone(acl_scope_from_tag('acls:hr'))


class TestMessageRequestACL(unittest.TestCase):
    def test_parses_top_level_block(self):
        req = MessageRequest({
            'org': {'oid': 'oid-1', 'jwt': 'jwt', 'ident': 'someone@example.com'},
            'action': 'ping',
            'data': {'acl': {'global': True}},  # user-controlled, must be ignored
            'config': {},
            'acl': {'scopes': ['hr'], 'global': False, 'enforce': True, 'source': 'user'},
        })
        self.assertTrue(req.acl.present)
        self.assertFalse(req.acl.is_global)
        self.assertEqual(req.acl.scopes, ['hr'])
        self.assertTrue(req.acl.allows(['acl:hr']))
        self.assertFalse(req.acl.allows(['acl:legal']))

    def test_absent_block_fails_closed(self):
        req = MessageRequest({'org': {'oid': 'oid-1', 'jwt': 'jwt'}, 'action': 'ping', 'data': {}, 'config': {}})
        self.assertFalse(req.acl.present)
        self.assertTrue(req.acl.allows(['prod']))
        self.assertFalse(req.acl.allows(['acl:hr']))

    def test_message_envelope(self):
        msg = Message({'version': 1, 'idempotency_key': 'k', 'request': {
            'org': {'oid': 'oid-1', 'jwt': 'jwt'}, 'action': 'ping', 'data': {}, 'config': {},
            'acl': {'scopes': [], 'global': True, 'enforce': True, 'source': 'internal'},
        }})
        self.assertTrue(msg.msg_request.acl.present)
        self.assertTrue(msg.msg_request.acl.is_global)


class TestAcceptsACLArgument(unittest.TestCase):
    def test_signatures(self):
        def four(sdk, data, conf, resState): pass
        def five(sdk, data, conf, resState, acl): pass
        def five_optional(sdk, data, conf, resState, acl=None): pass
        def star(sdk, *args): pass
        def kw_only(sdk, data, conf, resState, *, acl=None): pass

        class H(object):
            def four(self, sdk, data, conf, resState): pass
            def five(self, sdk, data, conf, resState, acl): pass

        self.assertFalse(_acceptsACLArgument(four))
        self.assertTrue(_acceptsACLArgument(five))
        self.assertTrue(_acceptsACLArgument(five_optional))
        self.assertTrue(_acceptsACLArgument(star))
        self.assertFalse(_acceptsACLArgument(kw_only))
        self.assertFalse(_acceptsACLArgument(H().four))
        self.assertTrue(_acceptsACLArgument(H().five))
        self.assertFalse(_acceptsACLArgument(lambda sdk, d, c, r: None))
        self.assertTrue(_acceptsACLArgument(lambda sdk, d, c, r, a: None))


SECRET = 'test-secret'


class DispatchExtension(lcextension.Extension):
    def init(self):
        self.calls = []
        self.requestHandlers = {
            'legacy': self.handleLegacy,
            'aware': self.handleAware,
        }
        self.eventHandlers = {}

    def handleLegacy(self, sdk, data, conf, resState):
        self.calls.append(('legacy', data, conf, resState))
        return lcextension.Response(data={'ok': True})

    def handleAware(self, sdk, data, conf, resState, acl):
        self.calls.append(('aware', data, conf, resState, acl))
        return lcextension.Response(data={'allowed': acl.allows(['acl:hr'])})

    def handleError(self, oid, error):
        pass


def _envelope(action, acl):
    req = {
        'org': {'oid': '11111111-2222-3333-4444-555555555555', 'jwt': 'jwt'},
        'action': action,
        'data': {'x': 1},
        'config': {'c': 2},
        'resource_state': {'r': {'last_mod': 7}},
    }
    if acl is not None:
        req['acl'] = acl
    return {'version': lcextension._PROTOCOL_VERSION, 'idempotency_key': 'idem', 'request': req}


class TestRequestDispatch(unittest.TestCase):
    def setUp(self):
        self.ext = DispatchExtension('acl-test', SECRET)

    def test_legacy_four_argument_handler_still_works(self):
        resp = self.ext._extRequestHandler(_envelope('legacy', {'scopes': ['hr'], 'global': False, 'enforce': True, 'source': 'user'}))
        self.assertIsNone(resp.error)
        self.assertEqual(self.ext.calls, [('legacy', {'x': 1}, {'c': 2}, {'r': {'last_mod': 7}})])

    def test_aware_handler_receives_block(self):
        resp = self.ext._extRequestHandler(_envelope('aware', {'scopes': ['hr'], 'global': False, 'enforce': True, 'source': 'user'}))
        self.assertIsNone(resp.error)
        self.assertEqual(resp.data, {'allowed': True})
        acl = self.ext.calls[0][4]
        self.assertIsInstance(acl, ACL)
        self.assertTrue(acl.present)
        self.assertEqual(acl.scopes, ['hr'])

    def test_aware_handler_without_block_fails_closed(self):
        resp = self.ext._extRequestHandler(_envelope('aware', None))
        self.assertIsNone(resp.error)
        self.assertEqual(resp.data, {'allowed': False})
        acl = self.ext.calls[0][4]
        self.assertFalse(acl.present)
        self.assertTrue(acl.enforce)

    def test_block_inside_data_is_ignored(self):
        env = _envelope('aware', None)
        env['request']['data']['acl'] = {'global': True, 'enforce': False}
        resp = self.ext._extRequestHandler(env)
        self.assertEqual(resp.data, {'allowed': False})

    def test_end_to_end_signed_http_request(self):
        body = json.dumps(_envelope('aware', {'scopes': ['hr'], 'global': False, 'enforce': True, 'source': 'user'})).encode()
        sig = hmac.new(SECRET.encode(), msg=body, digestmod=hashlib.sha256).hexdigest()
        client = self.ext.getApp().test_client()
        r = client.post('/', data=body, headers={'lc-ext-sig': sig, 'Content-Type': 'application/json'})
        self.assertEqual(r.status_code, 200, r.data)
        self.assertEqual(json.loads(r.data)['data'], {'allowed': True})


if __name__ == '__main__':
    unittest.main()
