package models

import "time"

// SupportMetricsAggregate is the response document for support metrics
// (WI-1133). It is a closed shape on purpose: widgets today and the
// customizable reports page later consume the same contract, and the CSV
// column definitions mirror it.
type SupportMetricsAggregate struct {
	From        string                    `json:"from"`
	To          string                    `json:"to"`
	Timezone    string                    `json:"timezone"`
	Bucket      string                    `json:"bucket"`
	GeneratedAt time.Time                 `json:"generated_at"`
	Summary     SupportMetricsSummary     `json:"summary"`
	Buckets     []SupportMetricsBucket    `json:"buckets"`
	Segments    []SupportMetricsSegment   `json:"segments"`
	Queues      []SupportMetricsQueueView `json:"queues"`
}

// SupportMetricsSummary carries window totals. Backlog and unassigned are
// live snapshots of the scoped ticket set (not window-restricted); the rest
// are activity inside the window.
type SupportMetricsSummary struct {
	Created       int64                       `json:"created"`
	Resolved      int64                       `json:"resolved"`
	Reopened      int64                       `json:"reopened"`
	Backlog       int64                       `json:"backlog"`
	Unassigned    int64                       `json:"unassigned"`
	FirstResponse *SupportMetricsDistribution `json:"first_response,omitempty"`
	Resolution    *SupportMetricsDistribution `json:"resolution,omitempty"`
	SLA           *SupportMetricsSLA          `json:"sla,omitempty"`
}

// SupportMetricsDistribution summarizes wall-clock durations in milliseconds.
type SupportMetricsDistribution struct {
	P50Ms float64 `json:"p50_ms"`
	P90Ms float64 `json:"p90_ms"`
	Count int64   `json:"count"`
}

// SupportMetricsSLA counts completed SLA cycles in the window. The numbers
// are calendar-aware by construction because cycles store elapsed time
// through their business calendars.
type SupportMetricsSLA struct {
	Completed    int64   `json:"completed"`
	Breached     int64   `json:"breached"`
	CompliantPct float64 `json:"compliant_pct"`
}

// SupportMetricsBucket is one point of the trend series. BacklogEnd is the
// open-at-bucket-end estimate reconstructed from created/resolved facts; a
// reopen can shift historical points (documented limitation — the live
// summary backlog is always exact).
type SupportMetricsBucket struct {
	BucketStart        string   `json:"bucket_start"`
	Created            int64    `json:"created"`
	Resolved           int64    `json:"resolved"`
	Reopened           int64    `json:"reopened"`
	BacklogEnd         int64    `json:"backlog_end"`
	FirstResponseP50Ms *float64 `json:"first_response_p50_ms,omitempty"`
	ResolutionP50Ms    *float64 `json:"resolution_p50_ms,omitempty"`
}

// SupportMetricsSegment is one value of the requested segmentation. An empty
// Key means "no value" (internal or unset dimension); the label carries the
// display fallback.
type SupportMetricsSegment struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Created     int64  `json:"created"`
	Backlog     int64  `json:"backlog"`
	Reopened    int64  `json:"reopened"`
	SLABreached int64  `json:"sla_breached"`
}

// SupportMetricsQueueView mirrors the support queue counts for
// single-workspace scope so a widget can render queue tabs inline.
type SupportMetricsQueueView struct {
	ID      int    `json:"id"`
	Key     string `json:"key,omitempty"`
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
	Count   int64  `json:"count"`
}
