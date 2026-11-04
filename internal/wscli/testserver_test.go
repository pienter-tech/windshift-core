package wscli

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var (
	loopbackListenOnce sync.Once
	loopbackListenErr  error
)

// skipUnlessLoopbackListen skips t when no local port can be bound, as in a
// sandbox that blocks binding. httptest.NewServer panics in that case, so call
// this before creating a server. The probe runs once per test binary and tries
// the same addresses as httptest: 127.0.0.1, then [::1].
func skipUnlessLoopbackListen(t testing.TB) {
	t.Helper()
	loopbackListenOnce.Do(func() {
		var errs []string
		for _, addr := range []struct{ network, address string }{
			{"tcp", "127.0.0.1:0"},
			{"tcp6", "[::1]:0"},
		} {
			ln, err := net.Listen(addr.network, addr.address)
			if err == nil {
				_ = ln.Close()
				return
			}
			errs = append(errs, err.Error())
		}
		loopbackListenErr = errors.New(strings.Join(errs, "; "))
	})
	if loopbackListenErr != nil {
		t.Skipf("local port binding is blocked here, so httptest cannot start a server: %v", loopbackListenErr)
	}
}

// newTestServer starts an httptest server for handler and closes it when the
// test ends. It skips the test when local port binding is blocked.
func newTestServer(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	skipUnlessLoopbackListen(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
