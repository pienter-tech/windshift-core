-- Workspace canned responses (WI-1138): reusable agent reply snippets,
-- scoped per workspace. Private snippets are internal notes and must never
-- be delivered to portal customers. Archived responses keep their row with
-- is_active = false so history and usage counters survive.

CREATE TABLE IF NOT EXISTS canned_responses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    body TEXT NOT NULL,
    is_private BOOLEAN NOT NULL DEFAULT false,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_by INTEGER,
    updated_by INTEGER,
    used_count INTEGER NOT NULL DEFAULT 0,
    last_used_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL,
    FOREIGN KEY (updated_by) REFERENCES users(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_canned_responses_ws_name_ci ON canned_responses(LOWER(name), workspace_id);
CREATE INDEX IF NOT EXISTS idx_canned_responses_ws_active ON canned_responses(workspace_id, is_active, name);
