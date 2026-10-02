package services

import (
	"database/sql"
	"errors"
	"log/slog"

	"windshift/internal/database"
	"windshift/internal/emailutil"
)

// maybeRecordPortalThreadAnchor mints the synthetic RFC 5322 thread root for
// items created by a portal customer through a non-email channel (portal or
// public form). The anchor gives outbound customer notifications something to
// thread In-Reply-To/References from and lets email replies quote their way
// back onto the portal ticket (WI-1546).
//
// The anchor row is an ordinary email_message_tracking record indistinguishable
// from other system-generated outbound Message-IDs: direction 'outbound' with
// comment_id NULL marks it as a root, and the empty from_email deliberately
// grants no thread participation (the participant guard reads from_email).
//
// Best-effort and idempotent: a failed insert is logged and the submission
// proceeds; the unique (channel_id, dedup_key) index turns redelivery into a
// no-op. Email-channel items already anchor to their real inbound Message-ID
// and are skipped here, as are items without a portal-customer creator.
func maybeRecordPortalThreadAnchor(db database.Database, itemID int64, channelID, creatorPortalCustomerID *int, title string) {
	if db == nil || channelID == nil || creatorPortalCustomerID == nil {
		return
	}
	var channelType string
	err := db.QueryRow("SELECT type FROM channels WHERE id = ?", *channelID).Scan(&channelType)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("portal thread anchor: channel lookup failed",
				"item_id", itemID, "channel_id", *channelID, "error", err)
		}
		return
	}
	if channelType == "email" || channelType == "imap" {
		// Legacy 'imap' channels are email intake too: their items anchor to
		// the customer's real inbound mail.
		return
	}

	messageID := emailutil.AnchorMessageID(itemID, smtpDomain(db))
	if _, err := db.ExecWrite(`
		INSERT INTO email_message_tracking (
			channel_id, message_id, dedup_key, in_reply_to, from_email, from_name, subject,
			item_id, comment_id, direction, processed_at
		) VALUES (?, ?, ?, NULL, '', NULL, ?, ?, NULL, 'outbound', CURRENT_TIMESTAMP)
		ON CONFLICT(channel_id, dedup_key) DO NOTHING
	`, *channelID, messageID, messageID, title, itemID); err != nil {
		slog.Warn("portal thread anchor: insert failed",
			"item_id", itemID, "channel_id", *channelID, "error", err)
		return
	}
	slog.Info("recorded portal thread anchor", "item_id", itemID, "channel_id", *channelID)
}
