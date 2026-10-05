-- External request participants (WI-1136): portal customers attached to a
-- work item alongside its assignee. Internal users collaborate through
-- assignment, teams, and watchers and are never stored here. Participants see
-- customer-facing content only.

CREATE TABLE IF NOT EXISTS item_participants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER NOT NULL,
    portal_customer_id INTEGER NOT NULL,
    added_by INTEGER,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE,
    FOREIGN KEY (portal_customer_id) REFERENCES portal_customers(id) ON DELETE CASCADE,
    FOREIGN KEY (added_by) REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE(item_id, portal_customer_id)
);

CREATE INDEX IF NOT EXISTS idx_item_participants_item ON item_participants(item_id);
CREATE INDEX IF NOT EXISTS idx_item_participants_customer ON item_participants(portal_customer_id);
