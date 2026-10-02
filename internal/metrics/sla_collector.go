package metrics

import (
	"context"
	"sync"
	"time"

	"windshift/internal/database"

	"github.com/prometheus/client_golang/prometheus"
)

// slaJobRefreshInterval bounds how often a scrape pays for the job-count
// aggregate. Pending and failed job counts are operational, so they refresh
// more often than the domain collector's heavy tables.
const slaJobRefreshInterval = 30 * time.Second

// slaJobCollector exposes the SLA due-work queue depth: how many jobs are
// waiting and how many have parked as failed and need operator action.
type slaJobCollector struct {
	db database.Database

	jobsPending *prometheus.Desc
	jobsFailed  *prometheus.Desc

	refreshInterval time.Duration
	now             func() time.Time

	mu          sync.Mutex
	pending     float64
	failed      float64
	refreshedAt time.Time
	haveSummary bool
}

func newSLAJobCollector(db database.Database) prometheus.Collector {
	return &slaJobCollector{
		db: db,
		jobsPending: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "sla", "jobs_pending"),
			"Current number of pending SLA jobs.", nil, nil,
		),
		jobsFailed: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "sla", "jobs_failed"),
			"Current number of failed SLA jobs awaiting operator action.", nil, nil,
		),
		refreshInterval: slaJobRefreshInterval,
		now:             time.Now,
	}
}

func (c *slaJobCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.jobsPending
	ch <- c.jobsFailed
}

func (c *slaJobCollector) Collect(ch chan<- prometheus.Metric) {
	pending, failed, err := c.counts()
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.jobsPending, err)
		ch <- prometheus.NewInvalidMetric(c.jobsFailed, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.jobsPending, prometheus.GaugeValue, pending)
	ch <- prometheus.MustNewConstMetric(c.jobsFailed, prometheus.GaugeValue, failed)
}

func (c *slaJobCollector) counts() (pending, failed float64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.haveSummary && c.now().Sub(c.refreshedAt) < c.refreshInterval {
		return c.pending, c.failed, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), collectionTimeout)
	defer cancel()
	var pendingCount, failedCount int64
	err = c.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN state = 'pending' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN state = 'failed' THEN 1 ELSE 0 END), 0)
		FROM sla_jobs`).Scan(&pendingCount, &failedCount)
	if err != nil {
		if c.haveSummary {
			return c.pending, c.failed, nil
		}
		return 0, 0, err
	}
	c.pending = float64(pendingCount)
	c.failed = float64(failedCount)
	c.refreshedAt = c.now()
	c.haveSummary = true
	return c.pending, c.failed, nil
}
