package wscli

import (
	"net/http"
	"strings"
	"testing"
)

// TestRunPrintsCommandErrorsOnce checks that a failing command reports its
// error a single time on stderr, keeps cobra's "Error: " prefix, exits
// non-zero, and (as before) prints no usage block for argument errors.
func TestRunPrintsCommandErrorsOnce(t *testing.T) {
	server, _ := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{
			"code": "insufficient_permission", "message": "Changing page links requires edit rights on the milestone",
		}})
	})

	tests := []struct {
		name     string
		args     []string
		message  string
		wantHint bool
	}{
		{
			name:    "server error",
			args:    []string{"milestone", "page", "add", "5", "66"},
			message: "Changing page links requires edit rights on the milestone",
		},
		{
			name:    "invalid argument",
			args:    []string{"milestone", "page", "add", "5", "spec"},
			message: "invalid page ID: spec",
		},
		{
			name:    "wrong argument count",
			args:    []string{"milestone", "page", "list"},
			message: "accepts 1 arg(s), received 0",
		},
		{
			name:    "unknown flag",
			args:    []string{"milestone", "page", "list", "5", "--bogus"},
			message: "unknown flag: --bogus",
		},
		{
			name:     "unknown command",
			args:     []string{"frobnicate"},
			message:  `unknown command "frobnicate" for "ws"`,
			wantHint: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := runWS(t, server.URL, "", tt.args...)
			if code == 0 {
				t.Fatalf("exit code 0, want non-zero; stderr %q", errOut)
			}
			if n := strings.Count(errOut, tt.message); n != 1 {
				t.Fatalf("error message %q appears %d times in stderr, want 1:\n%s", tt.message, n, errOut)
			}
			if !strings.HasPrefix(errOut, "Error: ") {
				t.Fatalf("stderr does not start with %q:\n%s", "Error: ", errOut)
			}
			if strings.Contains(errOut, "Usage:") {
				t.Fatalf("stderr unexpectedly contains a usage block:\n%s", errOut)
			}
			if hint := "Run 'ws --help' for usage."; strings.Contains(errOut, hint) != tt.wantHint {
				t.Fatalf("usage hint present = %v, want %v:\n%s", !tt.wantHint, tt.wantHint, errOut)
			}
		})
	}
}
