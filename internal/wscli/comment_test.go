package wscli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// itemCommentFeed is a v2 item comment feed page (inside the data envelope)
// with the server's flat author fields: a human comment (is_agent omitted, as
// the server does when false), an agent comment, and a portal-customer comment
// without author_id.
func itemCommentFeed() map[string]any {
	return map[string]any{"data": map[string]any{
		"comments": []any{
			map[string]any{
				"id": 1, "item_id": 42, "author_id": 3, "author_name": "Korneel Eeckhout",
				"content": "please check", "source": "human",
				"created_at": "2026-10-01T10:00:00Z", "updated_at": "2026-10-01T10:00:00Z",
			},
			map[string]any{
				"id": 2, "item_id": 42, "author_id": 9, "author_name": "Claude Code", "is_agent": true,
				"content": "done", "source": "human",
				"created_at": "2026-10-02T10:00:00Z", "updated_at": "2026-10-02T10:00:00Z",
			},
			map[string]any{
				"id": 3, "item_id": 42, "portal_customer_id": 4, "author_name": "Customer",
				"content": "thanks", "source": "human",
				"created_at": "2026-10-03T10:00:00Z", "updated_at": "2026-10-03T10:00:00Z",
			},
		},
		"has_more": false,
	}}
}

func TestCommentListJSONIncludesAuthor(t *testing.T) {
	server, requests := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, itemCommentFeed())
	})

	code, out, errOut := runWS(t, server.URL, "", "comment", "list", "42")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	if got := requests(); len(got) != 1 || got[0].Path != "/rest/api/v2/items/42/comments" {
		t.Fatalf("unexpected requests: %+v", got)
	}

	var comments []map[string]any
	if err := json.Unmarshal([]byte(out), &comments); err != nil {
		t.Fatalf("output is not a JSON comment list: %v\n%s", err, out)
	}
	if len(comments) != 3 {
		t.Fatalf("expected 3 comments, got %d:\n%s", len(comments), out)
	}
	want := []map[string]any{
		{"author_id": float64(3), "author_name": "Korneel Eeckhout", "is_agent": false},
		{"author_id": float64(9), "author_name": "Claude Code", "is_agent": true},
		{"author_id": nil, "author_name": "Customer", "is_agent": false},
	}
	for i, fields := range want {
		for key, value := range fields {
			got, ok := comments[i][key]
			if !ok {
				t.Fatalf("comment %d: missing %q in %v", i, key, comments[i])
			}
			if got != value {
				t.Fatalf("comment %d: %s = %v, want %v", i, key, got, value)
			}
		}
	}
}

func TestCommentListTableShowsAuthorAndMarksAgent(t *testing.T) {
	server, _ := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, itemCommentFeed())
	})

	code, out, errOut := runWS(t, server.URL, "", "comment", "list", "42", "-o", "table")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 5 || !strings.Contains(lines[0], "AUTHOR") {
		t.Fatalf("unexpected table output:\n%s", out)
	}
	if !strings.Contains(lines[2], "Korneel Eeckhout") || strings.Contains(lines[2], "(agent)") {
		t.Fatalf("human row: %q", lines[2])
	}
	if !strings.Contains(lines[3], "Claude Code (agent)") {
		t.Fatalf("agent row: %q", lines[3])
	}
	if !strings.Contains(lines[4], "Customer") || strings.Contains(lines[4], "(agent)") {
		t.Fatalf("portal row: %q", lines[4])
	}
}
