package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
	"windshift/internal/services"
)

type recordingMilestoneActivity struct {
	calls []services.MilestoneActivityListParams
}

func (a *recordingMilestoneActivity) List(_, _ int, params services.MilestoneActivityListParams) ([]models.MilestoneActivity, int, error) {
	a.calls = append(a.calls, params)
	return []models.MilestoneActivity{}, 0, nil
}

func serveMilestoneActivity(t *testing.T, activity milestoneActivityApplication, query string) *httptest.ResponseRecorder {
	t.Helper()
	handler := Adapt(transport{}.Page(listMilestoneActivity(activity)))
	request := httptest.NewRequest(http.MethodGet, "/milestones/7/activity"+query, nil)
	request.SetPathValue("milestone_id", "7")
	request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: 3}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestListMilestoneActivitySince(t *testing.T) {
	activity := &recordingMilestoneActivity{}

	if recorder := serveMilestoneActivity(t, activity, "?page=2&page_size=10"); recorder.Code != http.StatusOK {
		t.Fatalf("without since: status %d: %s", recorder.Code, recorder.Body)
	}
	if recorder := serveMilestoneActivity(t, activity, "?since=2026-01-01T09:00:00.5%2B02:00&page_size=10"); recorder.Code != http.StatusOK {
		t.Fatalf("with since: status %d: %s", recorder.Code, recorder.Body)
	}
	if len(activity.calls) != 2 {
		t.Fatalf("got %d service calls, want 2", len(activity.calls))
	}
	if first := activity.calls[0]; !first.Since.IsZero() || first.Limit != 10 || first.Offset != 10 {
		t.Fatalf("without since: %+v", first)
	}
	want := time.Date(2026, 1, 1, 7, 0, 0, 500_000_000, time.UTC)
	if second := activity.calls[1]; !second.Since.Equal(want) || second.Limit != 10 || second.Offset != 0 {
		t.Fatalf("with since: %+v, want since %s", second, want)
	}

	for _, raw := range []string{"yesterday", "2026-01-01", "2026-01-01T09:00:00", "2026-01-01T09:00:00 02:00"} {
		recorder := serveMilestoneActivity(t, activity, "?since="+url.QueryEscape(raw))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("since=%q: status %d, want 400", raw, recorder.Code)
		}
		var body struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("since=%q: decode %s: %v", raw, recorder.Body, err)
		}
		if body.Error.Code != "invalid_request" || body.Error.Details["field"] != "since" {
			t.Fatalf("since=%q: error body %s", raw, recorder.Body)
		}
	}
	if len(activity.calls) != 2 {
		t.Fatalf("invalid since reached the service (%d calls)", len(activity.calls))
	}
}

func TestMilestoneActivityContractDocumentsSince(t *testing.T) {
	for _, route := range Inventory() {
		if route.Method != http.MethodGet || route.Path != "/milestones/{milestone_id}/activity" {
			continue
		}
		if !slices.Contains(route.DocumentedErrors, http.StatusBadRequest) {
			t.Errorf("400 is not documented: %v", route.DocumentedErrors)
		}
		for _, parameter := range route.Parameters {
			if parameter.Name == "since" && parameter.In == "query" && parameter.Schema["format"] == "date-time" {
				return
			}
		}
		t.Fatalf("since query parameter missing: %+v", route.Parameters)
	}
	t.Fatal("milestone activity route not found")
}
