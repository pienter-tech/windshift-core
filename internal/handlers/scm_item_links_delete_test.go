package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"windshift/internal/middleware"
)

// TestDeleteItemSCMLinkRemembersTheDeletion deletes a detected and a manual
// link through the item page's endpoint; both are remembered so the
// repository sync does not link the PR to the item again.
func TestDeleteItemSCMLinkRemembersTheDeletion(t *testing.T) {
	f := newGiteaWebhookFixture(t)
	var workspaceID int
	if err := f.db.QueryRow(`SELECT id FROM workspaces WHERE key = 'FGJ'`).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	result, err := f.db.ExecWrite(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index) VALUES (?, 1, 'Stacked', 'a0')`, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	for _, link := range []struct{ externalID, source string }{{"7", "pr_body"}, {"8", "manual"}} {
		result, err := f.db.ExecWrite(`INSERT INTO item_scm_links (item_id, workspace_repository_id, link_type, external_id, detection_source) VALUES (?, ?, 'pull_request', ?, ?)`,
			itemID, f.repoID, link.externalID, link.source)
		if err != nil {
			t.Fatal(err)
		}
		linkID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodDelete, "/api/items/"+strconv.FormatInt(itemID, 10)+"/scm-links/"+strconv.FormatInt(linkID, 10), nil)
		req.SetPathValue("linkId", strconv.FormatInt(linkID, 10))
		req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyUser, f.user))
		recorder := httptest.NewRecorder()
		f.handler.DeleteItemSCMLink(recorder, req)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("delete %s link: status %d, body %s", link.source, recorder.Code, recorder.Body.String())
		}

		var links, dismissals int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM item_scm_links WHERE id = ?`, linkID).Scan(&links); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(`
			SELECT COUNT(*) FROM item_scm_link_dismissals
			WHERE item_id = ? AND workspace_repository_id = ? AND link_type = 'pull_request' AND external_id = ? AND created_by = ?
		`, itemID, f.repoID, link.externalID, f.user.ID).Scan(&dismissals); err != nil {
			t.Fatal(err)
		}
		if links != 0 || dismissals != 1 {
			t.Fatalf("after deleting the %s link: %d link rows, %d dismissals; want 0 and 1", link.source, links, dismissals)
		}
	}
}
