-- Append-only ticket fact events used by support metrics (WI-1133).
-- One row per observed fact; rows are never updated or deleted.
CREATE TABLE IF NOT EXISTS item_support_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL,
    item_id INTEGER NOT NULL,
    kind TEXT NOT NULL,
    occurred_at DATETIME NOT NULL,
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
    FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_item_support_events_scope ON item_support_events(workspace_id, kind, occurred_at);
CREATE INDEX IF NOT EXISTS idx_item_support_events_item ON item_support_events(item_id, kind);
CREATE UNIQUE INDEX IF NOT EXISTS uq_item_support_events_single_shot ON item_support_events(item_id, kind) WHERE kind IN ('first_response', 'resolved');
