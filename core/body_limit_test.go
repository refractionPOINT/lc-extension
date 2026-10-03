package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/common"
)

const encGzip = "gzip"

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func post(ext *Extension, body []byte, sig string, gzipped bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("lc-ext-sig", sig)
	if gzipped {
		req.Header.Set("Content-Encoding", encGzip)
	}
	rec := httptest.NewRecorder()
	ext.ServeHTTP(rec, req)
	return rec
}

func pingMessage(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(&common.Message{
		Version:        PROTOCOL_VERSION,
		IdempotencyKey: testIdem,
		Request: &common.RequestMessage{
			Org:    common.OrgAccessData{OID: "oid", JWT: automationTestJWT, Ident: testIdent},
			Action: testAction,
			Data:   limacharlie.Dict{"k": "v"},
			Config: limacharlie.Dict{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newPingExtension(t *testing.T, calls *int) (*Extension, func()) {
	t.Helper()
	ext, ms := newTestExtension(t)
	ext.Callbacks.RequestHandlers = map[common.ActionName]RequestCallback{
		testAction: {Callback: func(_ context.Context, _ RequestCallbackParams) common.Response {
			*calls++
			return common.Response{Data: limacharlie.Dict{"pong": true}}
		}},
	}
	return ext, ms.Close
}

// A valid signed request still goes through, gzipped (what the platform sends) or not, at default limits.
func TestSignedRequestStillAccepted(t *testing.T) {
	msg := pingMessage(t)
	for name, gzipped := range map[string]bool{encGzip: true, "plain": false} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			ext, done := newPingExtension(t, &calls)
			defer done()
			body := msg
			if gzipped {
				body = gz(t, msg)
			}
			if rec := post(ext, body, sign(msg), gzipped); rec.Code != http.StatusOK || calls != 1 {
				t.Fatalf("status %d calls %d: %s", rec.Code, calls, rec.Body.String())
			}
		})
	}
}

// A small gzip body that expands past the decoded cap is refused with 413 and never reaches the handler,
// and so is one whose signature is valid: the cap does not depend on who sent it.
func TestOversizeDecompressedBodyRefused(t *testing.T) {
	const decodedLimit = 1 << 20
	huge := bytes.Repeat([]byte("a"), 4*decodedLimit)
	wire := gz(t, huge)
	if len(wire) > 64<<10 {
		t.Fatalf("test body should be tiny on the wire, is %d", len(wire))
	}
	for name, sig := range map[string]string{"unsigned": "bogus", "signed": sign(huge)} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			ext, done := newPingExtension(t, &calls)
			defer done()
			ext.MaxDecodedBodyBytes = decodedLimit
			if rec := post(ext, wire, sig, true); rec.Code != http.StatusRequestEntityTooLarge || calls != 0 {
				t.Fatalf("status %d calls %d, want 413 and no handler call", rec.Code, calls)
			}
		})
	}
}

// The decoded body is checked against the cap exactly: at the cap is fine, one byte over is not.
func TestDecompressedBodyCapBoundary(t *testing.T) {
	msg := pingMessage(t)
	for _, tc := range []struct {
		name  string
		limit int64
		want  int
	}{
		{"at the cap", int64(len(msg)), http.StatusOK},
		{"one over", int64(len(msg)) - 1, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ext, done := newPingExtension(t, &calls)
			defer done()
			ext.MaxDecodedBodyBytes = tc.limit
			if rec := post(ext, gz(t, msg), sign(msg), true); rec.Code != tc.want {
				t.Fatalf("status %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// An oversize body is refused on the wire cap, declared (Content-Length) or not, before any decoding.
func TestOversizeWireBodyRefused(t *testing.T) {
	msg := pingMessage(t)
	for name, gzipped := range map[string]bool{encGzip: true, "plain": false} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			ext, done := newPingExtension(t, &calls)
			defer done()
			ext.MaxBodyBytes = 1024
			ext.MaxDecodedBodyBytes = 1024
			body := append(bytes.Clone(msg), strings.Repeat(" ", 4096)...)
			if gzipped {
				// Incompressible so the wire size stays over the cap.
				junk := make([]byte, 8192)
				for i := range junk {
					junk[i] = byte(i*31 + i>>3)
				}
				body = junk
			}
			declared := post(ext, body, sign(msg), gzipped)
			if declared.Code != http.StatusRequestEntityTooLarge || calls != 0 {
				t.Fatalf("declared length: status %d calls %d", declared.Code, calls)
			}

			// Unknown length (chunked): only the reader's cap can stop it.
			req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(bytes.NewReader(body)))
			req.ContentLength = -1
			req.Header.Set("lc-ext-sig", sign(msg))
			if gzipped {
				req.Header.Set("Content-Encoding", encGzip)
			}
			rec := httptest.NewRecorder()
			ext.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge || calls != 0 {
				t.Fatalf("unknown length: status %d calls %d", rec.Code, calls)
			}
		})
	}
}

// What an unauthenticated gzip request makes the extension allocate is bounded by its wire size, not by
// how far it expands: a ~1 MiB wire body expanding to 1 GiB must not cost anywhere near that.
func TestGzipBombDoesNotAllocateDecodedSize(t *testing.T) {
	const expanded = 1 << 30
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	chunk := make([]byte, 1<<20)
	for i := 0; i < expanded/len(chunk); i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	wire := buf.Bytes()

	calls := 0
	ext, done := newPingExtension(t, &calls)
	defer done()
	ext.MaxDecodedBodyBytes = 2 * expanded // permit it: it is the signature check that must not buffer

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rec := post(ext, wire, "bogus", true)
	runtime.ReadMemStats(&after)

	if rec.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("status %d calls %d, want 401 and no handler call", rec.Code, calls)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 64<<20 {
		t.Fatalf("a %d byte wire body that expands to %d allocated %d bytes before authentication", len(wire), expanded, alloc)
	}
}

func TestBadSignatureStillRejected(t *testing.T) {
	msg := pingMessage(t)
	for name, gzipped := range map[string]bool{encGzip: true, "plain": false} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			ext, done := newPingExtension(t, &calls)
			defer done()
			body := msg
			if gzipped {
				body = gz(t, msg)
			}
			if rec := post(ext, body, sign([]byte("other")), gzipped); rec.Code != http.StatusUnauthorized || calls != 0 {
				t.Fatalf("status %d calls %d", rec.Code, calls)
			}
		})
	}
}
