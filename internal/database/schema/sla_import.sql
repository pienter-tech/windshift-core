-- Jira SLA import identity. A source id lets a re-import update the same
-- imported cycle instead of duplicating it, matching the metric/goal/target
-- idempotency model.
ALTER TABLE item_sla_cycles ADD COLUMN source_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_item_sla_cycles_source
	ON item_sla_cycles(metric_id, source_id) WHERE source_id IS NOT NULL;
