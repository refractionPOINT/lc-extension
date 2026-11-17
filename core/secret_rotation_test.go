package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

func signWith(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// While the shared secret is rotated, a request signed with either the new
// SecretKey or a previous one is accepted, gzipped or not, and a request
// signed with anything else is still refused before reaching the handler.
func TestPreviousSecretKeysAcceptedDuringRotation(t *testing.T) {
	const oldSecret = "old-secret"
	msg := pingMessage(t)
	for name, gzipped := range encodings {
		for _, tc := range []struct {
			name     string
			sig      string
			previous []string
			want     int
			calls    int
		}{
			{"new secret", signWith(testSecret, msg), []string{oldSecret}, http.StatusOK, 1},
			{"previous secret", signWith(oldSecret, msg), []string{oldSecret}, http.StatusOK, 1},
			{"previous secret after empty entries", signWith(oldSecret, msg), []string{"", oldSecret}, http.StatusOK, 1},
			{"unknown secret", signWith("other-secret", msg), []string{oldSecret}, http.StatusUnauthorized, 0},
			{"previous secret once removed", signWith(oldSecret, msg), nil, http.StatusUnauthorized, 0},
			{"empty previous secret does not accept an empty key", signWith("", msg), []string{""}, http.StatusUnauthorized, 0},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				calls := 0
				ext, done := newPingExtension(t, &calls)
				defer done()
				ext.PreviousSecretKeys = tc.previous
				body := msg
				if gzipped {
					body = gz(t, msg)
				}
				if rec := post(ext, body, tc.sig, gzipped); rec.Code != tc.want || calls != tc.calls {
					t.Fatalf("status %d calls %d, want %d and %d calls: %s", rec.Code, calls, tc.want, tc.calls, rec.Body.String())
				}
			})
		}
	}
}
