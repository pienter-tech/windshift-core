package wscli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func setMilestoneItemJSON(milestoneIDs []int) map[string]any {
	milestones := make([]map[string]any, len(milestoneIDs))
	for i, id := range milestoneIDs {
		milestones[i] = map[string]any{"id": id, "name": fmt.Sprintf("M%d", id)}
	}
	return map[string]any{"id": 7, "workspace_id": 1, "title": "Task", "milestones": milestones}
}

// setMilestoneServer fakes GET and PATCH /rest/api/v2/items/7. The item starts
// in the given milestones; a PATCH answers with the milestone_ids it received.
func setMilestoneServer(t *testing.T, existing []int) (string, func() []recordedRequest) {
	t.Helper()
	var requests func() []recordedRequest
	var server *httptest.Server
	server, requests = milestoneCommentServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/v2/items/7" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unexpected path"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"data": setMilestoneItemJSON(existing)})
		case http.MethodPatch:
			var body struct {
				MilestoneIDs []int `json:"milestone_ids"`
			}
			// The recorder has already read r.Body; use its copy.
			recorded := requests()
			if err := json.Unmarshal([]byte(recorded[len(recorded)-1].Body), &body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": setMilestoneItemJSON(body.MilestoneIDs)})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "unexpected method"})
		}
	})
	return server.URL, requests
}

func patchedMilestoneIDs(t *testing.T, requests []recordedRequest) []int {
	t.Helper()
	var patches []recordedRequest
	for _, req := range requests {
		if req.Method == http.MethodPatch {
			patches = append(patches, req)
		}
	}
	if len(patches) != 1 {
		t.Fatalf("expected 1 PATCH request, got %d: %+v", len(patches), requests)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(patches[0].Body), &body); err != nil {
		t.Fatalf("PATCH body is not JSON: %v\n%s", err, patches[0].Body)
	}
	var ids []int
	if err := json.Unmarshal(body["milestone_ids"], &ids); err != nil {
		t.Fatalf("PATCH body milestone_ids: %v\n%s", err, patches[0].Body)
	}
	return ids
}

func TestTaskSetMilestoneDefaultReplacesMemberships(t *testing.T) {
	serverURL, requests := setMilestoneServer(t, []int{3, 4})

	code, _, errOut := runWS(t, serverURL, "", "task", "set-milestone", "7", "5", "-w", "1")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	if got := patchedMilestoneIDs(t, requests()); !reflect.DeepEqual(got, []int{5}) {
		t.Fatalf("milestone_ids = %v, want [5]", got)
	}
}

func TestTaskSetMilestoneAddKeepsExistingMemberships(t *testing.T) {
	serverURL, requests := setMilestoneServer(t, []int{3, 4})

	code, out, errOut := runWS(t, serverURL, "", "task", "set-milestone", "7", "5", "--add", "-w", "1", "-o", "table")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	if got := patchedMilestoneIDs(t, requests()); !reflect.DeepEqual(got, []int{3, 4, 5}) {
		t.Fatalf("milestone_ids = %v, want [3 4 5]", got)
	}
	if !strings.Contains(out, `Assigned 7 to milestone(s) "M3, M4, M5"`) {
		t.Fatalf("table output does not list every milestone:\n%s", out)
	}
}

func TestTaskSetMilestoneAddExistingMembershipSendsNoUpdate(t *testing.T) {
	serverURL, requests := setMilestoneServer(t, []int{3, 5})

	code, out, errOut := runWS(t, serverURL, "", "task", "set-milestone", "7", "5", "--add", "-w", "1", "-o", "table")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	for _, req := range requests() {
		if req.Method != http.MethodGet {
			t.Fatalf("unexpected %s %s for an existing membership", req.Method, req.Path)
		}
	}
	if !strings.Contains(out, "already in that milestone; nothing changed") || !strings.Contains(out, `"M3, M5"`) {
		t.Fatalf("unexpected table output:\n%s", out)
	}
}

func TestTaskSetMilestoneAddAndClearAreExclusive(t *testing.T) {
	serverURL, requests := setMilestoneServer(t, []int{3})

	code, _, errOut := runWS(t, serverURL, "", "task", "set-milestone", "7", "5", "--add", "--clear", "-w", "1")
	if code == 0 {
		t.Fatal("expected --add with --clear to fail")
	}
	if !strings.Contains(errOut, "none of the others can be") {
		t.Fatalf("unexpected error: %s", errOut)
	}
	if got := requests(); len(got) != 0 {
		t.Fatalf("expected no requests, got %+v", got)
	}
}
