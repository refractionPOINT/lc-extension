package webserver

import (
	"testing"
	"time"
)

// A peer that trickles or stalls must not hold a connection forever, and the
// bounds must still leave room for the slowest request the platform sends
// (it waits up to 2 minutes for an extension to answer).
func TestNewServerBoundsConnections(t *testing.T) {
	srv := newServer(":0", nil)
	const platformWait = 2 * time.Minute
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout < platformWait || srv.WriteTimeout < platformWait || srv.IdleTimeout <= 0 {
		t.Fatalf("timeouts header=%v read=%v write=%v idle=%v: all must be set, read and write at least %v",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout, platformWait)
	}
}
