package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"windshift/internal/models"
	"windshift/internal/repository"
)

type scmLinksTestTokens struct{}

// ValidateToken accepts crw_read (items:read) and crw_other (no items scope)
// for user 7.
func (scmLinksTestTokens) ValidateToken(raw string) (*models.User, *models.APIToken, error) {
	switch raw {
	case "crw_read":
		return &models.User{ID: 7}, &models.APIToken{ID: 1, UserID: 7, Permissions: `["items:read"]`}, nil
	case "crw_other":
		return &models.User{ID: 7}, &models.APIToken{ID: 2, UserID: 7, Permissions: `["pages:read"]`}, nil
	}
	return nil, nil, http.ErrNoCookie
}

func (scmLinksTestTokens) CheckTokenPermissions(token *models.APIToken, required []string) bool {
	var granted []string
	_ = json.Unmarshal([]byte(token.Permissions), &granted)
	for _, scope := range required {
		if !slices.Contains(granted, scope) {
			return false
		}
	}
	return true
}

type scmLinksTestLimiter struct{}

func (scmLinksTestLimiter) Acquire(int) (func(), bool) { return func() {}, true }

type scmLinksTestItems struct{ items map[int]*models.Item }

func (f scmLinksTestItems) FindByID(id int) (*models.Item, error) {
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return nil, repository.ErrNotFound
}

func (scmLinksTestItems) FindByIDsInWorkspace(context.Context, int, []int) ([]*models.Item, error) {
	return nil, nil
}

// scmLinksTestAccess lets user 7 view workspace 1 only.
type scmLinksTestAccess struct{}

func (scmLinksTestAccess) CanViewWorkspace(userID, workspaceID int) (bool, error) {
	return userID == 7 && workspaceID == 1, nil
}
func (scmLinksTestAccess) CanEditWorkspace(int, int) (bool, error)      { return false, nil }
func (scmLinksTestAccess) CanAdminWorkspace(int, int) (bool, error)     { return false, nil }
func (scmLinksTestAccess) GetAccessibleWorkspaceIDs(int) ([]int, error) { return []int{1}, nil }

type scmLinksTestReader struct {
	links  map[int][]repository.ItemSCMLink
	called []int
}

func (f *scmLinksTestReader) ListItemSCMLinks(itemID int) ([]repository.ItemSCMLink, error) {
	f.called = append(f.called, itemID)
	links := f.links[itemID]
	if links == nil {
		links = []repository.ItemSCMLink{}
	}
	return links, nil
}

func newSCMLinksTestMux(reader *scmLinksTestReader) *http.ServeMux {
	deps := Deps{
		Tokens:      scmLinksTestTokens{},
		Concurrency: scmLinksTestLimiter{},
		CORS:        func(next Handler) Handler { return next },
		Items: scmLinksTestItems{items: map[int]*models.Item{
			10: {ID: 10, WorkspaceID: 1},
			20: {ID: 20, WorkspaceID: 2},
			30: {ID: 30, WorkspaceID: 1},
		}},
		Access:   scmLinksTestAccess{},
		SCMLinks: reader,
	}
	builder := routeBuilder{exposure: ExposureBoth}
	registerItemSCMLinkRoutes(&builder, deps)
	mux := http.NewServeMux()
	registerMount(mux, restPrefix, builder.routes, deps, false)
	return mux
}

func serveSCMLinks(t *testing.T, mux *http.ServeMux, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestListItemSCMLinksBearer(t *testing.T) {
	reader := &scmLinksTestReader{links: map[int][]repository.ItemSCMLink{
		10: {
			{ID: 3, ItemID: 10, WorkspaceRepositoryID: 4, LinkType: "pull_request", ExternalID: "42", ExternalURL: "https://git.example/pienter/app/pulls/42", State: "open", RepositoryName: "pienter/app", CIState: "success"},
			{ID: 2, ItemID: 10, WorkspaceRepositoryID: 4, LinkType: "branch", ExternalID: "feature/AP-1-find-pr", RepositoryName: "pienter/app"},
		},
	}}
	mux := newSCMLinksTestMux(reader)

	rec := serveSCMLinks(t, mux, "/rest/api/v2/items/10/scm-links", "crw_read")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("got %d links, want 2: %s", len(body.Data), rec.Body)
	}
	pr := body.Data[0]
	if pr["link_type"] != "pull_request" || pr["external_id"] != "42" || pr["state"] != "open" ||
		pr["external_url"] != "https://git.example/pienter/app/pulls/42" || pr["repository_name"] != "pienter/app" || pr["ci_state"] != "success" {
		t.Fatalf("unexpected pull request link: %v", pr)
	}
	if body.Data[1]["external_id"] != "feature/AP-1-find-pr" {
		t.Fatalf("unexpected branch link: %v", body.Data[1])
	}

	empty := serveSCMLinks(t, mux, "/rest/api/v2/items/30/scm-links", "crw_read")
	if empty.Code != http.StatusOK {
		t.Fatalf("empty status = %d, body %s", empty.Code, empty.Body)
	}
	var emptyBody map[string]json.RawMessage
	if err := json.Unmarshal(empty.Body.Bytes(), &emptyBody); err != nil || string(emptyBody["data"]) != "[]" {
		t.Fatalf("want data [], got %s (%v)", empty.Body, err)
	}
}

func TestListItemSCMLinksRefusals(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		token  string
		status int
		code   string
	}{
		{name: "no token", path: "/rest/api/v2/items/10/scm-links", status: http.StatusUnauthorized, code: "authentication_required"},
		{name: "missing items:read", path: "/rest/api/v2/items/10/scm-links", token: "crw_other", status: http.StatusForbidden, code: "insufficient_permission"},
		{name: "workspace not viewable", path: "/rest/api/v2/items/20/scm-links", token: "crw_read", status: http.StatusNotFound, code: "not_found"},
		{name: "unknown item", path: "/rest/api/v2/items/99/scm-links", token: "crw_read", status: http.StatusNotFound, code: "not_found"},
		{name: "invalid item id", path: "/rest/api/v2/items/abc/scm-links", token: "crw_read", status: http.StatusBadRequest, code: "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := &scmLinksTestReader{}
			rec := serveSCMLinks(t, newSCMLinksTestMux(reader), tc.path, tc.token)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d, body %s", rec.Code, tc.status, rec.Body)
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %s: %v", rec.Body, err)
			}
			if body.Error.Code != tc.code {
				t.Fatalf("error code = %q, want %q (%s)", body.Error.Code, tc.code, rec.Body)
			}
			if len(reader.called) != 0 {
				t.Fatalf("links were read for a refused request: %v", reader.called)
			}
		})
	}
}

func TestItemSCMLinksRouteContract(t *testing.T) {
	for _, route := range BearerInventory() {
		if route.Method == http.MethodGet && route.Path == "/items/{item_id}/scm-links" {
			if !slices.Equal(route.Scopes, []string{"items:read"}) || route.ResponseShape != ResponseDocument {
				t.Fatalf("unexpected contract: scopes %v, shape %s", route.Scopes, route.ResponseShape)
			}
			return
		}
	}
	t.Fatal("GET /items/{item_id}/scm-links is not in the bearer inventory")
}
