package wscli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func milestonePageLinkJSON(id, pageID int, title string) map[string]any {
	return map[string]any{
		"id":              id,
		"milestone_id":    5,
		"page_id":         pageID,
		"page_title":      title,
		"workspace_id":    15,
		"created_by":      1,
		"created_by_name": "Korneel",
		"created_at":      "2026-10-06T10:00:00Z",
	}
}

func TestMilestonePageList(t *testing.T) {
	server, requests := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []any{milestonePageLinkJSON(11, 66, "Goal spec"), milestonePageLinkJSON(12, 67, "Notes")},
		})
	})

	code, out, errOut := runWS(t, server.URL, "", "milestone", "page", "list", "5")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	var links []MilestonePageLink
	if err := json.Unmarshal([]byte(out), &links); err != nil {
		t.Fatalf("output is not a JSON page link list: %v\n%s", err, out)
	}
	if len(links) != 2 || links[0].ID != 11 || links[0].PageID != 66 || links[1].PageTitle != "Notes" {
		t.Fatalf("unexpected links: %+v", links)
	}
	got := requests()
	if len(got) != 1 || got[0].Method != http.MethodGet || got[0].Path != "/rest/api/v2/milestones/5/page-links" {
		t.Fatalf("unexpected requests: %+v", got)
	}

	code, out, errOut = runWS(t, server.URL, "", "milestone", "page", "list", "5", "-o", "table")
	if code != 0 {
		t.Fatalf("table: exit code %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(out, "LINK ID") || !strings.Contains(out, "Goal spec") {
		t.Fatalf("unexpected table output:\n%s", out)
	}
}

func TestMilestonePageAdd(t *testing.T) {
	server, requests := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{"data": milestonePageLinkJSON(11, 66, "Goal spec")})
	})

	code, out, errOut := runWS(t, server.URL, "", "milestone", "page", "add", "5", "66")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	var link MilestonePageLink
	if err := json.Unmarshal([]byte(out), &link); err != nil || link.ID != 11 || link.PageID != 66 {
		t.Fatalf("unexpected output %q (%v)", out, err)
	}
	got := requests()
	if len(got) != 1 || got[0].Method != http.MethodPost || got[0].Path != "/rest/api/v2/milestones/5/page-links" {
		t.Fatalf("unexpected requests: %+v", got)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got[0].Body), &body); err != nil || body["page_id"] != float64(66) || len(body) != 1 {
		t.Fatalf("unexpected request body %q (%v)", got[0].Body, err)
	}
}

func TestMilestonePageAddReportsServerError(t *testing.T) {
	server, _ := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{
			"code": "insufficient_permission", "message": "Changing page links requires edit rights on the milestone",
		}})
	})

	code, _, errOut := runWS(t, server.URL, "", "milestone", "page", "add", "5", "66")
	if code == 0 || !strings.Contains(errOut, "Changing page links requires edit rights on the milestone") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestMilestonePageRemoveLooksUpTheLink(t *testing.T) {
	server, requests := milestoneCommentServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{
				"data": []any{milestonePageLinkJSON(11, 66, "Goal spec"), milestonePageLinkJSON(12, 67, "Notes")},
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	code, out, errOut := runWS(t, server.URL, "", "milestone", "page", "remove", "5", "67")
	if code != 0 {
		t.Fatalf("exit code %d, stderr: %s", code, errOut)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil || result["deleted"] != true || result["link_id"] != float64(12) || result["page_id"] != float64(67) {
		t.Fatalf("unexpected output %q (%v)", out, err)
	}
	got := requests()
	if len(got) != 2 || got[1].Method != http.MethodDelete || got[1].Path != "/rest/api/v2/milestones/5/page-links/12" {
		t.Fatalf("unexpected requests: %+v", got)
	}
}

func TestMilestonePageRemoveRejectsUnlinkedPage(t *testing.T) {
	server, requests := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{milestonePageLinkJSON(11, 66, "Goal spec")}})
	})

	code, _, errOut := runWS(t, server.URL, "", "milestone", "page", "remove", "5", "99")
	if code == 0 || !strings.Contains(errOut, "page 99 is not linked to milestone 5") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	for _, req := range requests() {
		if req.Method == http.MethodDelete {
			t.Fatalf("unexpected delete request: %+v", req)
		}
	}
}

func TestMilestonePageRejectsInvalidIDs(t *testing.T) {
	server, requests := milestoneCommentServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if code, _, errOut := runWS(t, server.URL, "", "milestone", "page", "add", "5", "spec"); code == 0 || !strings.Contains(errOut, "invalid page ID") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runWS(t, server.URL, "", "milestone", "page", "list", "PROJ"); code == 0 || !strings.Contains(errOut, "invalid milestone ID") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if got := requests(); len(got) != 0 {
		t.Fatalf("expected no requests, got %+v", got)
	}
}
