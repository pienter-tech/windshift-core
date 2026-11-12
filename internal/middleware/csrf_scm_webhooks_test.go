package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SCM webhook deliveries are server-to-server POSTs without Sec-Fetch-Site,
// Origin, or Referer, so they must bypass CSRF protection.
func TestCSRFProtectionExemptsSCMWebhookDeliveries(t *testing.T) {
	handler := CSRFProtection([]string{"https://windshift.example"})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)

	tests := []struct {
		name string
		path string
		want int
	}{
		{"gitea webhook", "/api/scm/webhooks/gitea/abc123", http.StatusNoContent},
		{"gitlab webhook", "/api/scm/webhooks/gitlab/abc123", http.StatusNoContent},
		{"non-exempt api path", "/api/scm/item-links", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("POST %s status = %d, want %d (body %q)", tt.path, rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
