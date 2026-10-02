-- SLA warning thresholds. A threshold fires an sla.warning when a percentage
-- of the goal has elapsed. Thresholds deliberately live outside Jira-imported
-- goals and targets; they are consumed by the existing notification and
-- automation rules subscribed to sla.warning.

CREATE TABLE IF NOT EXISTS sla_warning_thresholds (
	id SERIAL PRIMARY KEY,
	workspace_id INTEGER NOT NULL,
	metric_id INTEGER,
	label TEXT NOT NULL DEFAULT '',
	percent INTEGER NOT NULL,
	is_active BOOLEAN NOT NULL DEFAULT TRUE,
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	CHECK (percent > 0 AND percent < 100),
	FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
	FOREIGN KEY (metric_id) REFERENCES sla_metrics(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_sla_warning_thresholds_scope
	ON sla_warning_thresholds(workspace_id, COALESCE(metric_id, 0), percent);
