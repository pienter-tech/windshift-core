-- Per-(action, item) inactivity marks (WI-1132). The inactivity sweeper
-- records the item activity timestamp a trigger fired for, so an action
-- re-fires only after the item sees new activity and goes stale again.
-- Rows cascade away with their action or item.

CREATE TABLE IF NOT EXISTS action_trigger_marks (
    action_id INTEGER NOT NULL,
    item_id INTEGER NOT NULL,
    last_activity_at DATETIME NOT NULL,
    marked_at DATETIME NOT NULL,
    PRIMARY KEY (action_id, item_id),
    FOREIGN KEY (action_id) REFERENCES actions(id) ON DELETE CASCADE,
    FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_action_trigger_marks_item ON action_trigger_marks(item_id);
