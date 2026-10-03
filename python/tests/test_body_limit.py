import gzip
import hashlib
import hmac
import io
import json
import os
import sys
import tracemalloc
import unittest
import zlib

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

import lcextension
from lcextension.ext import DEFAULT_MAX_BODY_BYTES, DEFAULT_MAX_DECODED_BODY_BYTES

SECRET = 'test-secret'


def sign(body: bytes) -> str:
    return hmac.new(SECRET.encode(), body, hashlib.sha256).hexdigest()


def heartbeat() -> bytes:
    return json.dumps({'version': 20221218, 'idempotent_key': 'i', 'heartbeat': {}}).encode()


class _TestExtension(lcextension.Extension):
    def init(self):
        self.requestHandlers = {}
        self.eventHandlers = {}


def newClient(**kwargs):
    ext = _TestExtension('body-limit-test', SECRET, **kwargs)
    return ext.getApp().test_client()


def post(client, body, sig, gzipped=True):
    headers = {'lc-ext-sig': sig}
    if gzipped:
        headers['Content-Encoding'] = 'gzip'
    return client.post('/', data=body, headers=headers)


class ShortReadStream(io.BytesIO):
    '''A WSGI input that, like a socket-backed one, returns fewer bytes than asked for
    before EOF.'''
    def __init__(self, data: bytes, chunk: int = 7):
        super().__init__(data)
        self._chunk = chunk

    def read(self, size: int = -1) -> bytes:
        n = self._chunk if size is None or size < 0 else min(size, self._chunk)
        return super().read(n)


def postStreamed(client, body, headers, chunk=7):
    '''POST with no Content-Length (as a chunked request, which the server terminates), so the
    handler reads the raw input stream, which here returns short reads.'''
    environ = {'wsgi.input_terminated': True, 'CONTENT_LENGTH': ''}
    return client.post('/', input_stream=ShortReadStream(body, chunk), headers=headers, environ_overrides=environ)


def gzipZeros(n: int) -> bytes:
    c = zlib.compressobj(9, zlib.DEFLATED, 31)
    chunk = b'\0' * (1 << 20)
    out = []
    for _ in range(n // len(chunk)):
        out.append(c.compress(chunk))
    out.append(c.flush())
    return b''.join(out)


class TestBodyLimit(unittest.TestCase):
    def test_signed_requests_still_accepted(self):
        msg = heartbeat()
        for name, body, gzipped in (('gzip', gzip.compress(msg), True), ('plain', msg, False)):
            with self.subTest(name):
                r = post(newClient(), body, sign(msg), gzipped)
                self.assertEqual(r.status_code, 200, r.data)

    def test_short_reads_do_not_truncate_the_body(self):
        msg = heartbeat()
        for name, body, gzipped in (('gzip', gzip.compress(msg), True), ('plain', msg, False)):
            with self.subTest(name):
                headers = {'lc-ext-sig': sign(msg)}
                if gzipped:
                    headers['Content-Encoding'] = 'gzip'
                r = postStreamed(newClient(), body, headers)
                self.assertEqual(r.status_code, 200, r.data)

    def test_oversize_body_of_unknown_length_refused(self):
        junk = os.urandom(8192)
        c = newClient(max_body_bytes=1024, max_decoded_body_bytes=1024)
        r = postStreamed(c, junk, {'lc-ext-sig': sign(junk), 'Content-Encoding': 'gzip'}, 100)
        self.assertEqual(r.status_code, 413)

    def test_multi_member_gzip_accepted(self):
        msg = heartbeat()
        body = gzip.compress(msg[:10]) + gzip.compress(msg[10:])
        self.assertEqual(post(newClient(), body, sign(msg)).status_code, 200)

    def test_bad_signature_rejected(self):
        msg = heartbeat()
        for name, body, gzipped in (('gzip', gzip.compress(msg), True), ('plain', msg, False)):
            with self.subTest(name):
                self.assertEqual(post(newClient(), body, sign(b'other'), gzipped).status_code, 401)

    def test_oversize_decompressed_refused_before_signature(self):
        limit = 1 << 20
        wire = gzipZeros(4 * limit)
        self.assertLess(len(wire), 64 << 10)
        for name, sig in (('unsigned', 'bogus'), ('signed', sign(b'\0' * (4 * limit)))):
            with self.subTest(name):
                r = post(newClient(max_decoded_body_bytes=limit), wire, sig)
                self.assertEqual(r.status_code, 413, r.data)

    def test_decoded_cap_boundary(self):
        msg = heartbeat()
        self.assertEqual(post(newClient(max_decoded_body_bytes=len(msg)), gzip.compress(msg), sign(msg)).status_code, 200)
        self.assertEqual(post(newClient(max_decoded_body_bytes=len(msg) - 1), gzip.compress(msg), sign(msg)).status_code, 413)

    def test_oversize_wire_refused(self):
        junk = os.urandom(8192)
        for name, gzipped in (('gzip', True), ('plain', False)):
            with self.subTest(name):
                c = newClient(max_body_bytes=1024, max_decoded_body_bytes=1024)
                self.assertEqual(post(c, junk, sign(junk), gzipped).status_code, 413)

    def test_bad_gzip_is_client_error(self):
        for name, body in (('garbage', b'not gzip at all'), ('truncated', gzip.compress(heartbeat())[:-8]), ('empty', b'')):
            with self.subTest(name):
                self.assertEqual(post(newClient(), body, 'bogus').status_code, 400)

    def test_gzip_bomb_does_not_allocate_decoded_size(self):
        expanded = 256 << 20
        wire = gzipZeros(expanded)
        client = newClient(max_decoded_body_bytes=2 * expanded)
        tracemalloc.start()
        try:
            r = post(client, wire, 'bogus')
            _, peak = tracemalloc.get_traced_memory()
        finally:
            tracemalloc.stop()
        self.assertEqual(r.status_code, 401)
        self.assertLess(peak, 32 << 20, f'{len(wire)} byte wire body expanding to {expanded} peaked at {peak} bytes before authentication')


if __name__ == '__main__':
    unittest.main()
