package services

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"windshift/internal/database"
	"windshift/internal/emailutil"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/smtp"
)

// EmailReplyService sends threaded SMTP replies to portal customers
// when internal users add comments to email-originated items.
type EmailReplyService struct {
	db         database.Database
	smtpSender ThreadedEmailSender
	idResolver *IDResolverService
	outboxMu   sync.Mutex
	canonical  bool
	// leaseOwner identifies this process's delivery claims so a manual retry
	// can tell a live claim from retry backoff (WI-1572).
	leaseOwner string
}

// NewEmailReplyService creates a new EmailReplyService.
func NewEmailReplyService(db database.Database, smtpSender ThreadedEmailSender) *EmailReplyService {
	hostname, _ := os.Hostname()
	return &EmailReplyService{
		db:         db,
		smtpSender: smtpSender,
		idResolver: NewIDResolverService(db),
		leaseOwner: fmt.Sprintf("%s-%d", hostname, os.Getpid()),
	}
}

// HandleCommentCreated checks if an outbound email should be sent for a new comment.
// It sends a threaded email to the portal customer if:
// - The comment is not private
// - The comment is from an internal user (not from a portal customer)
// - The item was created via an email channel by a portal customer
func (s *EmailReplyService) HandleCommentCreated(params HandleCommentParams) error {
	if s.canonical {
		return nil
	}
	return s.handleCommentCreated(params, true)
}

func (s *EmailReplyService) handleCommentCreated(params HandleCommentParams, deliverImmediately bool) error {
	// Guard: skip private comments
	if params.IsPrivate {
		return nil
	}

	// Guard: skip if comment is FROM a portal customer (don't echo back)
	if params.PortalCustomerID != nil {
		return nil
	}

	// Guard: skip if no identified author
	if params.AuthorID == 0 {
		return nil
	}

	// Query item: channel, portal customer creator, workspace key, item number, title
	item, err := repository.NewItemRepository(s.db).FindByIDWithDetails(params.ItemID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to query item for email reply: %w", err)
	}

	// Skip if item has no channel or no portal customer creator
	if item.ChannelID == nil || item.CreatorPortalCustomerID == nil {
		return nil
	}

	// Any existing channel works as long as the conversation is threadable:
	// email-originated tickets thread from the customer's real messages,
	// portal/form tickets thread from their synthetic anchor.
	var channelType string
	err = s.db.QueryRow("SELECT type FROM channels WHERE id = ?", *item.ChannelID).Scan(&channelType)
	if err != nil {
		// Channel gone — nothing to thread from, skip.
		return nil
	}

	// Portal/form-originated tickets thread from the anchor minted at
	// submission; the ensure covers pre-upgrade tickets (created before the
	// anchor existed) and repairs a failed best-effort mint. It is a no-op for
	// email channels and for items that already have their anchor.
	maybeRecordPortalThreadAnchor(s.db, int64(params.ItemID), item.ChannelID, item.CreatorPortalCustomerID, item.Title)

	// Recipient: today the creator only. When ticket participants ship
	// (WI-1136) the fan-out widens to authorized participants — each gets
	// their own outbox row so replies attribute per recipient, and the intake
	// participant guard already accepts them as repliers.
	// Look up portal customer email
	var customerEmail, customerName string
	err = s.db.QueryRow("SELECT email, name FROM portal_customers WHERE id = ?", *item.CreatorPortalCustomerID).Scan(&customerEmail, &customerName)
	if err != nil || customerEmail == "" {
		slog.Debug("no email for portal customer, skipping reply",
			slog.String("component", "email_reply_service"),
			slog.Int("customer_id", *item.CreatorPortalCustomerID),
		)
		return nil
	}

	// Build threading headers from email_message_tracking
	type trackingRecord struct {
		MessageID string
		Subject   sql.NullString
	}
	rows, err := s.db.Query(`
		SELECT message_id, subject FROM email_message_tracking
		WHERE item_id = ?
		ORDER BY processed_at ASC
	`, params.ItemID)
	if err != nil {
		return fmt.Errorf("failed to query email tracking: %w", err)
	}
	defer rows.Close()

	var records []trackingRecord
	for rows.Next() {
		var rec trackingRecord
		if err = rows.Scan(&rec.MessageID, &rec.Subject); err != nil {
			continue
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate email tracking: %w", err)
	}

	if len(records) == 0 {
		// No email tracking records — can't thread, skip
		slog.Debug("no email tracking records for item, skipping reply",
			slog.String("component", "email_reply_service"),
			slog.Int("item_id", params.ItemID),
		)
		return nil
	}

	// References: all Message-IDs chronologically (item-scoped — the thread
	// follows the conversation across channels).
	var references []string
	for _, rec := range records {
		if rec.MessageID != "" {
			references = append(references, rec.MessageID)
		}
	}

	// In-Reply-To: most recent Message-ID
	inReplyTo := records[len(records)-1].MessageID

	// Subject: Re: {original subject} from first tracking record
	originalSubject := item.Title
	if records[0].Subject.Valid && records[0].Subject.String != "" {
		originalSubject = records[0].Subject.String
	}
	subject := originalSubject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	// Get SMTP domain for Message-ID generation
	smtpDomain := s.getSMTPDomain()

	// Generate Message-ID for this outbound email
	messageID := fmt.Sprintf("<ws-comment-%d@%s>", params.CommentID, smtpDomain)

	// Get author name for email template
	authorName := s.idResolver.ResolveUserName(params.AuthorID)
	if authorName == "" {
		authorName = "Team member"
	}

	// Build email body via the shared template pipeline. We pre-compute the
	// threaded subject above (Re: …) and pass it through as OriginalSubject;
	// the rendered subject from RenderEmail is discarded so the threading
	// stays correct.
	itemKey := fmt.Sprintf("%s-%d", item.WorkspaceKey, item.WorkspaceItemNumber)
	_, htmlBody, textBody, err := s.smtpSender.RenderEmail(emailutil.TemplatePortalReply, struct {
		AuthorName      string
		ItemKey         string
		ItemTitle       string
		Content         string
		OriginalSubject string
	}{
		AuthorName:      authorName,
		ItemKey:         itemKey,
		ItemTitle:       item.Title,
		Content:         params.Content,
		OriginalSubject: subject,
	})
	if err != nil {
		return fmt.Errorf("failed to render portal reply email: %w", err)
	}

	referencesJSON, err := json.Marshal(references)
	if err != nil {
		return fmt.Errorf("encode email references: %w", err)
	}
	_, err = s.db.ExecWrite(`
		INSERT INTO email_reply_outbox (
			comment_id, channel_id, item_id, to_email, to_name, subject,
			html_body, text_body, message_id, in_reply_to, references_json,
			from_email, from_name, next_attempt_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(comment_id) DO NOTHING
	`, params.CommentID, *item.ChannelID, params.ItemID, customerEmail, customerName,
		subject, htmlBody, textBody, messageID, inReplyTo, string(referencesJSON),
		s.getSMTPFromEmail(), authorName)
	if err != nil {
		return fmt.Errorf("enqueue threaded email reply: %w", err)
	}

	// Sending immediately keeps the current low-latency behavior. The durable
	// row remains pending on failure and is retried by NotificationScheduler.
	if !deliverImmediately || !s.smtpSender.IsSMTPConfigured() {
		return nil
	}
	s.outboxMu.Lock()
	defer s.outboxMu.Unlock()
	if _, err := s.deliverPendingReply(params.CommentID); err != nil {
		return fmt.Errorf("threaded email queued for retry: %w", err)
	}
	return nil
}

type emailReplyOutboxRow struct {
	CommentID      int
	ChannelID      int
	ItemID         int
	ToEmail        string
	ToName         string
	Subject        string
	HTMLBody       string
	TextBody       string
	MessageID      string
	InReplyTo      string
	ReferencesJSON string
	FromEmail      string
	FromName       string
	AttemptCount   int
}

// SendAutomationNotice implements CustomerNotifier: it emails the portal
// customer who created the item through the same threaded transport as reply
// notifications. Automation notices send directly rather than through the
// comment-bound reply outbox; action run history is the durable record.
// Skips (no customer, no thread, SMTP unset) are reported, not errors.
func (s *EmailReplyService) SendAutomationNotice(itemID, actorID int, subject, message string) (delivered bool, skipReason string, err error) {
	if !s.smtpSender.IsSMTPConfigured() {
		return false, "smtp_not_configured", nil
	}

	item, err := repository.NewItemRepository(s.db).FindByIDWithDetails(itemID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return false, "item_missing", nil
		}
		return false, "", fmt.Errorf("load item for customer notice: %w", err)
	}
	if item.ChannelID == nil || item.CreatorPortalCustomerID == nil {
		return false, "no_customer", nil
	}

	// Same anchor ensure as the reply path: automation notices must thread for
	// portal-originated tickets (and survive pre-upgrade tickets) too.
	maybeRecordPortalThreadAnchor(s.db, int64(itemID), item.ChannelID, item.CreatorPortalCustomerID, item.Title)

	var customerEmail, customerName string
	err = s.db.QueryRow("SELECT email, name FROM portal_customers WHERE id = ?", *item.CreatorPortalCustomerID).Scan(&customerEmail, &customerName)
	if err != nil {
		return false, "no_customer", nil
	}
	if customerEmail == "" {
		return false, "no_email", nil
	}

	// Thread against the item's recorded Message-IDs (item-scoped); without
	// a thread there is nothing to reply into, so the notice would start an
	// orphaned thread.
	var inReplyTo string
	var references []string
	var originalSubject sql.NullString
	rows, err := s.db.Query(`
		SELECT message_id, subject FROM email_message_tracking
		WHERE item_id = ?
		ORDER BY processed_at ASC
	`, itemID)
	if err != nil {
		return false, "", fmt.Errorf("query email tracking for customer notice: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var messageID string
		if scanErr := rows.Scan(&messageID, &originalSubject); scanErr != nil {
			continue
		}
		references = append(references, messageID)
		inReplyTo = messageID
	}
	if err := rows.Err(); err != nil {
		return false, "", fmt.Errorf("iterate email tracking for customer notice: %w", err)
	}
	if len(references) == 0 {
		return false, "no_thread", nil
	}

	emailSubject := item.Title
	if originalSubject.Valid && originalSubject.String != "" {
		emailSubject = originalSubject.String
	}
	if subject != "" {
		emailSubject = subject
	}
	if !strings.HasPrefix(strings.ToLower(emailSubject), "re:") {
		emailSubject = "Re: " + emailSubject
	}

	authorName := s.idResolver.ResolveUserName(actorID)
	if authorName == "" {
		authorName = "Support Team"
	}
	itemKey := fmt.Sprintf("%s-%d", item.WorkspaceKey, item.WorkspaceItemNumber)
	_, htmlBody, textBody, err := s.smtpSender.RenderEmail(emailutil.TemplatePortalReply, struct {
		AuthorName      string
		ItemKey         string
		ItemTitle       string
		Content         string
		OriginalSubject string
	}{
		AuthorName:      authorName,
		ItemKey:         itemKey,
		ItemTitle:       item.Title,
		Content:         message,
		OriginalSubject: emailSubject,
	})
	if err != nil {
		return false, "", fmt.Errorf("render customer notice email: %w", err)
	}

	smtpDomain := s.getSMTPDomain()
	messageID := fmt.Sprintf("<ws-notice-%d-%d@%s>", itemID, time.Now().UnixNano(), smtpDomain)
	if err := s.smtpSender.SendThreadedEmail(smtp.ThreadedEmailParams{
		ToEmail:    customerEmail,
		ToName:     customerName,
		Subject:    emailSubject,
		HTMLBody:   htmlBody,
		TextBody:   textBody,
		MessageID:  messageID,
		InReplyTo:  inReplyTo,
		References: references,
	}); err != nil {
		return false, "", fmt.Errorf("send customer notice: %w", err)
	}
	return true, "", nil
}

// ProcessPendingReplies retries a bounded batch from the durable reply outbox.
// It is called by NotificationScheduler on the existing SMTP cadence.
func (s *EmailReplyService) ProcessPendingReplies(limit int) (int, error) {
	if limit <= 0 {
		limit = 50
	}
	if !s.smtpSender.IsSMTPConfigured() {
		return 0, nil
	}

	s.outboxMu.Lock()
	defer s.outboxMu.Unlock()

	rows, err := s.db.Query(`
		SELECT comment_id
		FROM email_reply_outbox
		WHERE delivered_at IS NULL AND discarded_at IS NULL AND next_attempt_at <= CURRENT_TIMESTAMP
		ORDER BY created_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("query email reply outbox: %w", err)
	}
	var commentIDs []int
	for rows.Next() {
		var commentID int
		if err := rows.Scan(&commentID); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan email reply outbox: %w", err)
		}
		commentIDs = append(commentIDs, commentID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate email reply outbox: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close email reply outbox rows: %w", err)
	}

	delivered := 0
	var lastErr error
	for _, commentID := range commentIDs {
		sent, err := s.deliverPendingReply(commentID)
		if err != nil {
			lastErr = err
			continue
		}
		if sent {
			delivered++
		}
	}
	// Delivered rows are retained briefly for idempotence/audit, then pruned.
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := s.db.ExecWrite(`DELETE FROM email_reply_outbox WHERE delivered_at IS NOT NULL AND delivered_at < ?`, cutoff); err != nil {
		slog.Warn("failed to prune delivered email reply outbox rows", "error", err)
	}
	return delivered, lastErr
}

func (s *EmailReplyService) deliverPendingReply(commentID int) (bool, error) {
	var row emailReplyOutboxRow
	// Atomically lease the row before crossing the SMTP boundary. The process
	// mutex prevents duplicates within one server; this conditional UPDATE also
	// prevents two application instances from selecting and sending the same
	// pending reply. A crashed worker releases itself when the lease expires.
	leaseUntil := time.Now().Add(5 * time.Minute)
	err := s.db.QueryRow(`
		UPDATE email_reply_outbox
		SET next_attempt_at = ?, lease_owner = ?, updated_at = CURRENT_TIMESTAMP
		WHERE comment_id = ? AND delivered_at IS NULL AND discarded_at IS NULL
		  AND next_attempt_at <= CURRENT_TIMESTAMP
		RETURNING comment_id, channel_id, item_id, to_email, to_name, subject,
		       html_body, text_body, message_id, in_reply_to, references_json,
		       from_email, from_name, attempt_count
	`, leaseUntil, s.leaseOwner, commentID).Scan(
		&row.CommentID, &row.ChannelID, &row.ItemID, &row.ToEmail, &row.ToName,
		&row.Subject, &row.HTMLBody, &row.TextBody, &row.MessageID, &row.InReplyTo,
		&row.ReferencesJSON, &row.FromEmail, &row.FromName, &row.AttemptCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim pending email reply: %w", err)
	}

	var references []string
	if err := json.Unmarshal([]byte(row.ReferencesJSON), &references); err != nil {
		s.recordReplyFailure(row.CommentID, row.AttemptCount, err)
		return false, fmt.Errorf("decode pending email references: %w", err)
	}
	err = s.smtpSender.SendThreadedEmail(smtp.ThreadedEmailParams{
		ToEmail:    row.ToEmail,
		ToName:     row.ToName,
		Subject:    row.Subject,
		HTMLBody:   row.HTMLBody,
		TextBody:   row.TextBody,
		MessageID:  row.MessageID,
		InReplyTo:  row.InReplyTo,
		References: references,
	})
	if err != nil {
		s.recordReplyFailure(row.CommentID, row.AttemptCount, err)
		return false, fmt.Errorf("send threaded email: %w", err)
	}

	if _, err := s.db.ExecWrite(`
		UPDATE email_reply_outbox
		SET delivered_at = CURRENT_TIMESTAMP, last_error = NULL, lease_owner = NULL,
		    updated_at = CURRENT_TIMESTAMP
		WHERE comment_id = ? AND delivered_at IS NULL
	`, row.CommentID); err != nil {
		return false, fmt.Errorf("mark threaded email delivered: %w", err)
	}

	// Tracking is best-effort after delivery. Do not retry SMTP merely because
	// this audit/threading insert failed; that would duplicate customer mail.
	if _, err := s.db.ExecWrite(`
		INSERT INTO email_message_tracking (
			channel_id, message_id, dedup_key, in_reply_to, from_email, from_name, subject,
			item_id, comment_id, direction, processed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'outbound', CURRENT_TIMESTAMP)
		ON CONFLICT(channel_id, dedup_key) DO NOTHING
	`, row.ChannelID, row.MessageID, row.MessageID, row.InReplyTo, row.FromEmail,
		row.FromName, row.Subject, row.ItemID, row.CommentID); err != nil {
		slog.Warn("failed to record delivered outbound email in tracking",
			"comment_id", row.CommentID, "error", err)
	}

	slog.Info("sent threaded email reply to customer",
		"component", "email_reply_service",
		"comment_id", row.CommentID,
		"item_id", row.ItemID,
		"to", row.ToEmail,
	)
	return true, nil
}

func (s *EmailReplyService) recordReplyFailure(commentID, previousAttempts int, sendErr error) {
	shift := min(previousAttempts, 6)
	nextAttempt := time.Now().Add(time.Minute * time.Duration(1<<shift))
	if _, err := s.db.ExecWrite(`
		UPDATE email_reply_outbox
		SET attempt_count = attempt_count + 1, next_attempt_at = ?,
		    last_error = ?, lease_owner = NULL, updated_at = CURRENT_TIMESTAMP
		WHERE comment_id = ? AND delivered_at IS NULL
	`, nextAttempt, sendErr.Error(), commentID); err != nil {
		slog.Error("failed to record email reply delivery failure", "comment_id", commentID, "error", err)
	}
}

// getSMTPDomain extracts the domain from the SMTP from email.
func (s *EmailReplyService) getSMTPDomain() string {
	return smtpDomain(s.db)
}

// getSMTPFromEmail gets the configured SMTP from email address.
func (s *EmailReplyService) getSMTPFromEmail() string {
	return smtpFromEmail(s.db)
}

// fallbackSMTPFromEmail is used when no default outbound SMTP channel is
// configured or its config can't be read.
const fallbackSMTPFromEmail = "noreply@windshift.local"

// smtpDomain extracts the domain from the default outbound SMTP from email.
// Shared by the reply service and the portal thread-anchor minter.
func smtpDomain(db database.Database) string {
	fromEmail := smtpFromEmail(db)
	if idx := strings.LastIndex(fromEmail, "@"); idx >= 0 {
		return fromEmail[idx+1:]
	}
	return "windshift.local"
}

// smtpFromEmail reads the configured from email of the default outbound SMTP
// channel, falling back when none is configured or its config can't be read.
func smtpFromEmail(db database.Database) string {
	var configJSON string
	err := db.QueryRow(`
		SELECT COALESCE(config, '{}') FROM channels
		WHERE type = 'smtp' AND direction = 'outbound'
		  AND status = 'enabled' AND is_default = true
		ORDER BY updated_at DESC
		LIMIT 1
	`).Scan(&configJSON)
	if err != nil {
		return fallbackSMTPFromEmail
	}

	var cfg models.ChannelConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil || cfg.SMTPFromEmail == "" {
		return fallbackSMTPFromEmail
	}
	return cfg.SMTPFromEmail
}

// ErrEmailReplyNotRetryable marks an outbox row an operator cannot re-send:
// it is already delivered, explicitly discarded, or belongs to another
// channel. Handlers surface it as a conflict; repository.ErrNotFound stays
// the missing-row signal.
var ErrEmailReplyNotRetryable = errors.New("email reply is not retryable")

// ErrEmailReplyInFlight marks an outbox row whose delivery lease another
// worker still holds; the operator retry must not steal it (WI-1572).
var ErrEmailReplyInFlight = errors.New("email reply is being delivered by another worker")

// RetryPendingReply attempts immediate delivery of one pending outbound
// reply on behalf of an operator. The row's backoff lease is cleared first so
// a stuck schedule (or an earlier failure's next_attempt_at) cannot block the
// explicit retry. A failed send is recorded like any scheduler attempt:
// attempt_count increments, last_error is stored, and the row returns to the
// backoff schedule.
func (s *EmailReplyService) RetryPendingReply(channelID, commentID int) (delivered bool, err error) {
	s.outboxMu.Lock()
	defer s.outboxMu.Unlock()

	var state struct {
		delivered sql.NullTime
		discarded sql.NullTime
	}
	err = s.db.QueryRow(`
		SELECT delivered_at, discarded_at FROM email_reply_outbox
		WHERE channel_id = ? AND comment_id = ?
	`, channelID, commentID).Scan(&state.delivered, &state.discarded)
	if errors.Is(err, sql.ErrNoRows) {
		return false, repository.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("load email reply for retry: %w", err)
	}
	if state.delivered.Valid || state.discarded.Valid {
		return false, ErrEmailReplyNotRetryable
	}

	if !s.smtpSender.IsSMTPConfigured() {
		return false, ErrSMTPNotConfigured
	}

	// Retry scheduling never steals a live claim: the reset only applies
	// while no worker holds the delivery lease. A future next_attempt_at
	// with a NULL owner is retry backoff and retries right now; a future
	// next_attempt_at with an owner is another instance's in-flight send.
	res, err := s.db.ExecWrite(`
		UPDATE email_reply_outbox
		SET next_attempt_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE channel_id = ? AND comment_id = ?
		  AND (lease_owner IS NULL OR next_attempt_at <= CURRENT_TIMESTAMP)
	`, channelID, commentID)
	if err != nil {
		return false, fmt.Errorf("reset email reply backoff for retry: %w", err)
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return false, ErrEmailReplyInFlight
	}

	return s.deliverPendingReply(commentID)
}
