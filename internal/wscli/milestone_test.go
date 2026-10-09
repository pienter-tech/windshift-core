package wscli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMilestoneUpdateReadsDescriptionFileVerbatim(t *testing.T) {
	server, requests := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": 5, "name": "v2", "status": "planning"}})
	})
	markdown := "# Goal\n\n- literal \\n stays\n- \"quotes\" and `code`\n"
	path := filepath.Join(t.TempDir(), "description.md")
	if err := os.WriteFile(path, []byte(markdown), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, errOut := runWS(t, server.URL, "", "milestone", "update", "5", "-w", "1", "--file", path)
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	code, _, errOut = runWS(t, server.URL, markdown, "milestone", "update", "5", "-w", "1", "-f", "-")
	if code != 0 {
		t.Fatalf("stdin: exit code %d, stderr: %s", code, errOut)
	}

	got := requests()
	if len(got) != 2 {
		t.Fatalf("expected 2 requests, got %+v", got)
	}
	for i, req := range got {
		if req.Method != http.MethodPatch || req.Path != "/rest/api/v2/milestones/5" {
			t.Fatalf("request %d: %s %s", i, req.Method, req.Path)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(req.Body), &body); err != nil || body["description"] != markdown || len(body) != 1 {
			t.Fatalf("request %d: unexpected body %q", i, req.Body)
		}
	}
}

func TestMilestoneUpdateRejectsDescriptionAndFileTogether(t *testing.T) {
	server, requests := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	code, _, errOut := runWS(t, server.URL, "", "milestone", "update", "5", "-w", "1", "-d", "x", "--file", "notes.md")
	if code == 0 || !strings.Contains(errOut, "none of the others can be") {
		t.Fatalf("expected -d and --file together to fail, exit %d, stderr %q", code, errOut)
	}
	code, _, errOut = runWS(t, server.URL, "", "milestone", "update", "5", "-w", "1", "--file", filepath.Join(t.TempDir(), "missing.md"))
	if code == 0 || !strings.Contains(errOut, "failed to read") {
		t.Fatalf("missing file: exit %d, stderr %q", code, errOut)
	}
	if got := requests(); len(got) != 0 {
		t.Fatalf("expected no requests, got %+v", got)
	}
}
