package metrics

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"windshift/internal/database"

	"github.com/prometheus/client_golang/prometheus"
)

// emailRefreshInterval bounds how often a scrape pays for the email
// aggregates. Operators alert on outbox backlog age and intake rate limits,
// so both must be cheap enough to scrape frequently (WI-1142).
const emailRefreshInterval = 30 * time.Second

// emailCollector exposes helpdesk email health: how much outbound mail is
// queued or stuck, how old the oldest pending reply is, and how much inbound
// intake has been rate-limited.
type emailCollector struct {
	db database.Database

	outboxPending    *prometheus.Desc
	outboxOldestSec  *prometheus.Desc
	outboxFailed     *prometheus.Desc
	intakeLimited    *prometheus.Desc
	pollFailures     *prometheus.Desc
	deliveriesFailed *prometheus.Desc

	refreshInterval time.Duration
	now             func() time.Time

	mu          sync.Mutex
	pending     float64
	oldestSec   float64
	failed      float64
	limited     float64
	pollFail    float64
	delivFailed float64
	refreshedAt time.Time
	haveSummary bool
}

func newEmailCollector(db database.Database) prometheus.Collector {
	return &emailCollector{
		db: db,
		outboxPending: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "email", "outbox_pending"),
			"Current number of outbound replies queued for delivery.", nil, nil,
		),
		outboxOldestSec: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "email", "outbox_oldest_pending_seconds"),
			"Age in seconds of the oldest pending outbound reply.", nil, nil,
		),
		outboxFailed: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "email", "outbox_exhausted"),
			"Outbound replies at or beyond six delivery attempts; operator action needed.", nil, nil,
		),
		intakeLimited: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "email", "intake_rate_limited_total"),
			"Total inbound emails parked by the per-sender intake rate limit.", nil, nil,
		),
		pollFailures: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "email", "poll_failures_total"),
			"Total IMAP intake polls that failed.", nil, nil,
		),
		deliveriesFailed: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "email", "poll_errors_total"),
			"Total recorded IMAP poll error count across intake channels.", nil, nil,
		),
		refreshInterval: emailRefreshInterval,
		now:             time.Now,
	}
}

func (c *emailCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.outboxPending
	ch <- c.outboxOldestSec
	ch <- c.outboxFailed
	ch <- c.intakeLimited
	ch <- c.pollFailures
	ch <- c.deliveriesFailed
}

func (c *emailCollector) Collect(ch chan<- prometheus.Metric) {
	if err := c.refresh(); err != nil {
		ch <- prometheus.NewInvalidMetric(c.outboxPending, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.outboxPending, prometheus.GaugeValue, c.pending)
	ch <- prometheus.MustNewConstMetric(c.outboxOldestSec, prometheus.GaugeValue, c.oldestSec)
	ch <- prometheus.MustNewConstMetric(c.outboxFailed, prometheus.GaugeValue, c.failed)
	ch <- prometheus.MustNewConstMetric(c.intakeLimited, prometheus.CounterValue, c.limited)
	ch <- prometheus.MustNewConstMetric(c.pollFailures, prometheus.CounterValue, c.pollFail)
	ch <- prometheus.MustNewConstMetric(c.deliveriesFailed, prometheus.CounterValue, c.delivFailed)
}

func (c *emailCollector) refresh() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.haveSummary && c.now().Sub(c.refreshedAt) < c.refreshInterval {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), collectionTimeout)
	defer cancel()

	var oldest sql.NullTime
	err := c.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			MIN(created_at),
			COALESCE(SUM(CASE WHEN attempt_count >= 6 THEN 1 ELSE 0 END), 0)
		FROM email_reply_outbox
		WHERE delivered_at IS NULL AND discarded_at IS NULL
	`).Scan(&c.pending, &oldest, &c.failed)
	if err != nil {
		return err
	}
	if oldest.Valid {
		c.oldestSec = c.now().UTC().Sub(oldest.Time).Seconds()
		if c.oldestSec < 0 {
			c.oldestSec = 0
		}
	}

	var limited, pollFail float64
	if err := c.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM email_message_tracking WHERE rate_limited_at IS NOT NULL),
			(SELECT COALESCE(SUM(error_count), 0) FROM email_channel_state)
	`).Scan(&limited, &pollFail); err == nil {
		c.limited = limited
		c.pollFail = pollFail
	}

	c.refreshedAt = c.now()
	c.haveSummary = true
	return nil
}
