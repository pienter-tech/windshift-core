// Package metrics exposes Prometheus metrics for the running Windshift server.
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"windshift/internal/database"
	"windshift/internal/version"

	"github.com/felixge/httpsnoop"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "windshift"

// Metrics owns the collectors for one server instance.
type Metrics struct {
	registry *prometheus.Registry
	handler  http.Handler

	httpRequests        *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	scmPolls            *prometheus.CounterVec
	scmPollDuration     *prometheus.HistogramVec
	slaInlineDuration   prometheus.Histogram
}

// New creates an isolated registry and registers process, runtime, database,
// and Windshift collectors.
func New(db database.Database) *Metrics {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{Namespace: namespace}),
	)

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build information for the running Windshift server.",
	}, []string{"version", "commit"})
	buildInfo.WithLabelValues(version.Version, version.Commit).Set(1)
	serverStartTime := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "server_start_time_seconds",
		Help:      "Unix time when the Windshift server metrics registry was created.",
	})
	serverStartTime.SetToCurrentTime()

	httpRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total HTTP requests by method, route pattern, and response status.",
	}, []string{"method", "route", "status_code"})
	httpRequestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request duration by method, route pattern, and response status.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route", "status_code"})
	scmPolls := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "scm",
		Name:      "polls_total",
		Help:      "Total scheduled SCM polls by operation and outcome.",
	}, []string{"operation", "outcome"})
	scmPollDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "scm",
		Name:      "poll_duration_seconds",
		Help:      "Scheduled SCM poll duration by operation.",
		Buckets:   prometheus.ExponentialBuckets(0.25, 2, 12),
	}, []string{"operation"})
	slaInlineDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "sla",
		Name:      "inline_duration_seconds",
		Help:      "Inline SLA evaluation duration per recorded item fact.",
		Buckets:   prometheus.ExponentialBuckets(0.0005, 2, 14),
	})

	registry.MustRegister(
		buildInfo,
		serverStartTime,
		httpRequests,
		httpRequestDuration,
		scmPolls,
		scmPollDuration,
		slaInlineDuration,
	)
	if db != nil && db.GetDB() != nil {
		registry.MustRegister(
			collectors.NewDBStatsCollector(db.GetDB(), "windshift"),
			newDomainCollector(db),
			newSLAJobCollector(db),
		)
	}

	m := &Metrics{
		registry:            registry,
		httpRequests:        httpRequests,
		httpRequestDuration: httpRequestDuration,
		scmPolls:            scmPolls,
		scmPollDuration:     scmPollDuration,
		slaInlineDuration:   slaInlineDuration,
	}
	m.handler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog:          metricsLogger{},
		Registry:          registry,
		Timeout:           5 * time.Second,
		EnableOpenMetrics: true,
	})
	return m
}

// Handler returns the public Prometheus exposition handler. Restrict access at
// the reverse proxy or network boundary when metrics should not be public.
//
// @Summary      Read Prometheus metrics
// @Description  Returns Go runtime, process, database, HTTP, agent, webhook, and SCM metrics. Public; no authentication required. Restrict access at the reverse proxy or network boundary when metrics should not be publicly reachable.
// @Tags         operations
// @Produce      plain,application/openmetrics-text
// @Success      200  {string}  string  "Prometheus or OpenMetrics text exposition"
// @Router       /metrics [get]
func (m *Metrics) Handler() http.Handler {
	return m.handler
}

// Instrument records HTTP metrics after the router has resolved the route
// pattern. Raw paths are never used as labels.
func (m *Metrics) Instrument(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &requestMetricsState{}
		r = r.WithContext(context.WithValue(r.Context(), requestMetricsStateKey{}, state))
		captured := httpsnoop.CaptureMetrics(next, w, r)
		pattern := state.routePattern
		if pattern == "" {
			pattern = r.Pattern
		}
		route := routePattern(pattern)
		if route == "/metrics" {
			return
		}
		statusCode := strconv.Itoa(captured.Code)
		m.httpRequests.WithLabelValues(r.Method, route, statusCode).Inc()
		m.httpRequestDuration.WithLabelValues(r.Method, route, statusCode).Observe(captured.Duration.Seconds())
	})
}

// CaptureRoutePattern wraps the ServeMux so the outer instrumentation can read
// its resolved route even when middleware clones the request.
func (m *Metrics) CaptureRoutePattern(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, _ := r.Context().Value(requestMetricsStateKey{}).(*requestMetricsState)
		defer func() {
			if state != nil {
				state.routePattern = r.Pattern
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ObserveSLAInlineDuration records one inline SLA evaluation's duration.
func (m *Metrics) ObserveSLAInlineDuration(duration time.Duration) {
	if m == nil {
		return
	}
	m.slaInlineDuration.Observe(duration.Seconds())
}

// ObserveSCMPoll records one scheduled SCM polling operation.
func (m *Metrics) ObserveSCMPoll(operation string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	m.scmPolls.WithLabelValues(operation, outcome).Inc()
	m.scmPollDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

func routePattern(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	if _, route, ok := strings.Cut(pattern, " "); ok {
		return route
	}
	return pattern
}

type requestMetricsStateKey struct{}

type requestMetricsState struct {
	routePattern string
}

type metricsLogger struct{}

func (metricsLogger) Println(v ...any) {
	slog.Error("failed to serve metrics", "error", fmt.Sprint(v...))
}
