package handlers

import (
	"net/http"
	"time"

	"windshift/internal/sla"
)

// testSLAClockHandler serves the WINDSHIFT_E2E_TEST_HOOKS-only SLA clock
// route. It sets or advances the server's test clock so a browser test can
// move the SLA evaluation instant forward while the due-work loop stays off.
type testSLAClockHandler struct {
	clock *sla.TestClock
}

// NewTestSLAClock builds the handler. Server.go mounts the returned
// http.Handler only when WINDSHIFT_E2E_TEST_HOOKS=1, mirroring the other test
// hooks.
func NewTestSLAClock(clock *sla.TestClock) http.Handler {
	return &testSLAClockHandler{clock: clock}
}

type testSLAClockRequest struct {
	Now       *string `json:"now"`
	AdvanceMs *int64  `json:"advance_ms"`
}

func (h *testSLAClockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeJSON[testSLAClockRequest](w, r)
	if !ok {
		return
	}
	if request.Now != nil {
		parsed, err := time.Parse(time.RFC3339, *request.Now)
		if err != nil {
			respondValidationError(w, r, "now must be RFC3339")
			return
		}
		h.clock.Set(parsed)
	}
	if request.AdvanceMs != nil {
		h.clock.Advance(time.Duration(*request.AdvanceMs) * time.Millisecond)
	}
	respondJSONOK(w, map[string]string{"now": h.clock.Now().UTC().Format(time.RFC3339Nano)})
}
