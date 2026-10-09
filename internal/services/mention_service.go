package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

// MentionPattern matches @username or @"Display Name" patterns
// Supports: @username, @"John Doe"
var MentionPattern = regexp.MustCompile(`(?:^|[^a-zA-Z0-9.])@([a-zA-Z0-9_.-]+)|(?:^|[^a-zA-Z0-9.])@"([^"]+)"`)

// ProcessMentionsParams contains parameters for processing mentions
type ProcessMentionsParams struct {
	SourceType  string // "comment" or "item_description"
	SourceID    int
	Content     string
	ItemID      int
	WorkspaceID int
	ActorUserID int
}

// MentionService handles mention extraction, storage, and notification
type MentionService struct {
	db                  database.Database
	notificationService *NotificationService
	permissionService   *PermissionService
	workspaceUsers      *WorkspaceUserResolver
}

// SetWorkspaceUserResolver aligns stored mentions and agent triggers with the
// same actionable-user roster used by mention and assignment pickers.
func (s *MentionService) SetWorkspaceUserResolver(resolver *WorkspaceUserResolver) {
	s.workspaceUsers = resolver
}

// NewMentionService creates a new mention service
func NewMentionService(db database.Database, notificationService *NotificationService, permissionService *PermissionService) *MentionService {
	return &MentionService{
		db:                  db,
		notificationService: notificationService,
		permissionService:   permissionService,
	}
}

// ExtractMentionIdentifiers parses content and returns list of mention identifiers
func (s *MentionService) ExtractMentionIdentifiers(content string) []string {
	matches := MentionPattern.FindAllStringSubmatch(content, -1)
	identifiers := make([]string, 0)
	seen := make(map[string]bool)

	for _, match := range matches {
		identifier := match[1] // username pattern
		if identifier == "" {
			identifier = match[2] // "Display Name" pattern
		}

		if identifier != "" && !seen[identifier] {
			seen[identifier] = true
			identifiers = append(identifiers, identifier)
		}
	}

	return identifiers
}

// ResolveMentionedUserIDs parses content and resolves every @mention to an
// active user id, skipping identifiers that match no user. IDs follow first
// appearance order and are deduplicated. Workspace-sensitive callers should
// use ResolveActionableMentionedUserIDs.
func (s *MentionService) ResolveMentionedUserIDs(content string) ([]int, error) {
	identifiers := s.ExtractMentionIdentifiers(content)
	if len(identifiers) == 0 {
		return nil, nil
	}
	ids := make([]int, 0, len(identifiers))
	seen := make(map[int]bool, len(identifiers))
	for _, identifier := range identifiers {
		userID, _, err := s.resolveUserIdentifier(identifier)
		if err != nil {
			return nil, fmt.Errorf("resolve mention %q: %w", identifier, err)
		}
		if userID == 0 || seen[userID] {
			continue
		}
		seen[userID] = true
		ids = append(ids, userID)
	}
	return ids, nil
}

// ResolveActionableMentionedUserIDs resolves mentions and removes users who
// cannot act in the target workspace.
func (s *MentionService) ResolveActionableMentionedUserIDs(content string, workspaceID int) ([]int, error) {
	ids, err := s.ResolveMentionedUserIDs(content)
	if err != nil || len(ids) == 0 {
		return ids, err
	}
	if s.workspaceUsers == nil {
		filtered := make([]int, 0, len(ids))
		for _, userID := range ids {
			if s.canUserReceiveMention(userID, workspaceID) {
				filtered = append(filtered, userID)
			}
		}
		return filtered, nil
	}

	actionable, err := s.actionableUserIDs(context.Background(), workspaceID)
	if err != nil {
		return nil, err
	}
	filtered := make([]int, 0, len(ids))
	for _, userID := range ids {
		if _, ok := actionable[userID]; ok {
			filtered = append(filtered, userID)
		}
	}
	return filtered, nil
}

func (s *MentionService) actionableUserIDs(ctx context.Context, workspaceID int) (map[int]struct{}, error) {
	users, err := s.workspaceUsers.List(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("resolve actionable mention users: %w", err)
	}
	ids := make(map[int]struct{}, len(users))
	for _, user := range users {
		ids[user.ID] = struct{}{}
	}
	return ids, nil
}

// resolveUserIdentifier looks up a user by username or display name
// FIXME: require unique mention identifiers; display-name matches are ambiguous.
func (s *MentionService) resolveUserIdentifier(identifier string) (userID int, displayName string, err error) {
	var firstName, lastName string

	err = s.db.QueryRow(`
		SELECT id, first_name, last_name
		FROM users
		WHERE LOWER(username) = LOWER(?) AND is_active = true
	`, identifier).Scan(&userID, &firstName, &lastName)

	if err == nil {
		displayName = strings.TrimSpace(firstName + " " + lastName)
		return userID, displayName, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}

	err = s.db.QueryRow(`
		SELECT id, first_name, last_name
		FROM users
		WHERE LOWER(TRIM(first_name || ' ' || last_name)) = LOWER(?) AND is_active = true
	`, identifier).Scan(&userID, &firstName, &lastName)

	if err == nil {
		displayName = strings.TrimSpace(firstName + " " + lastName)
		return userID, displayName, nil
	}

	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil // User not found, not an error
	}

	return 0, "", err
}

// ProcessMentions handles mention diff and creates/removes mention records
func (s *MentionService) ProcessMentions(params ProcessMentionsParams) error {
	slog.Debug("Processing mentions", slog.String("component", "mentions"), slog.String("source_type", params.SourceType), slog.Int("source_id", params.SourceID), slog.Int("item_id", params.ItemID))

	// Resolve active, actionable recipients, then diff them against stored mentions.
	identifiers := s.ExtractMentionIdentifiers(params.Content)
	var actionable map[int]struct{}
	if s.workspaceUsers != nil && len(identifiers) > 0 {
		var err error
		actionable, err = s.actionableUserIDs(context.Background(), params.WorkspaceID)
		if err != nil {
			return err
		}
	}

	currentMentions := make(map[int]string) // userID -> displayName
	for _, identifier := range identifiers {
		userID, displayName, err := s.resolveUserIdentifier(identifier)
		if err != nil {
			slog.Error("Error resolving identifier", slog.String("component", "mentions"), slog.String("identifier", identifier), slog.Any("error", err))
			continue
		}
		if userID == 0 {
			continue // Unknown user, skip
		}
		if userID == params.ActorUserID {
			continue
		}
		_, isActionable := actionable[userID]
		if (actionable != nil && !isActionable) || (actionable == nil && !s.canUserReceiveMention(userID, params.WorkspaceID)) {
			slog.Debug("skipping mention: user cannot act in workspace",
				slog.String("component", "mentions"),
				slog.Int("user_id", userID),
				slog.Int("workspace_id", params.WorkspaceID),
			)
			continue
		}
		currentMentions[userID] = displayName
	}

	existingMentions, err := s.getExistingMentions(params.SourceType, params.SourceID)
	if err != nil {
		return fmt.Errorf("failed to get existing mentions: %w", err)
	}

	existingIDs := make(map[int]bool)
	for _, m := range existingMentions {
		existingIDs[m.MentionedUserID] = true
	}

	newMentions := make([]struct {
		userID      int
		displayName string
	}, 0)

	for userID, displayName := range currentMentions {
		if !existingIDs[userID] {
			slog.Debug("Creating new mention", slog.String("component", "mentions"), slog.Int("user_id", userID), slog.String("source_type", params.SourceType), slog.Int("source_id", params.SourceID))

			err = s.createMention(&models.Mention{
				SourceType:               params.SourceType,
				SourceID:                 params.SourceID,
				MentionedUserID:          userID,
				ItemID:                   params.ItemID,
				WorkspaceID:              params.WorkspaceID,
				CreatedBy:                params.ActorUserID,
				MentionedUserDisplayName: displayName,
			})
			if err != nil {
				slog.Error("Error creating mention", slog.String("component", "mentions"), slog.Int("user_id", userID), slog.Any("error", err))
				continue
			}

			newMentions = append(newMentions, struct {
				userID      int
				displayName string
			}{userID, displayName})
		}
	}

	for _, existingMention := range existingMentions {
		if _, exists := currentMentions[existingMention.MentionedUserID]; !exists {
			slog.Debug("Removing mention", slog.String("component", "mentions"), slog.Int("mention_id", existingMention.ID), slog.Int("user_id", existingMention.MentionedUserID))

			_, err = s.db.ExecWrite(`DELETE FROM mentions WHERE id = ?`, existingMention.ID)
			if err != nil {
				slog.Error("Error deleting mention", slog.String("component", "mentions"), slog.Int("mention_id", existingMention.ID), slog.Any("error", err))
			}
		}
	}

	isPersonal, err := s.isPersonalWorkspace(params.WorkspaceID)
	if err != nil {
		slog.Error("Error checking if workspace is personal", slog.String("component", "mentions"), slog.Any("error", err))
	}

	if !isPersonal {
		for _, mention := range newMentions {
			s.emitMentionNotification(params, mention.userID, mention.displayName)
		}
	} else {
		slog.Debug("Skipping notifications for personal workspace", slog.String("component", "mentions"), slog.Int("workspace_id", params.WorkspaceID))
	}

	slog.Debug("ProcessMentions completed", slog.String("component", "mentions"), slog.Int("created", len(newMentions)), slog.Int("removed", countRemovedMentions(existingMentions, currentMentions)))

	return nil
}

// canUserReceiveMention reports whether userID has enough access to the
// workspace that we can legitimately notify them they were mentioned. If the
// permission service isn't wired (tests, logbook sidecar), fail open to stay
// compatible — production always configures one.
func (s *MentionService) canUserReceiveMention(userID, workspaceID int) bool {
	if s.permissionService == nil {
		return true
	}
	ok, err := s.permissionService.HasWorkspacePermission(userID, workspaceID, models.PermissionItemView)
	if err != nil {
		slog.Warn("mention permission check failed; denying",
			slog.String("component", "mentions"),
			slog.Int("user_id", userID),
			slog.Int("workspace_id", workspaceID),
			slog.Any("error", err),
		)
		return false
	}
	return ok
}

// countRemovedMentions counts how many existing mentions were removed
func countRemovedMentions(existingMentions []models.Mention, currentMentions map[int]string) int {
	count := 0
	for _, m := range existingMentions {
		if _, exists := currentMentions[m.MentionedUserID]; !exists {
			count++
		}
	}
	return count
}

// isPersonalWorkspace checks if the workspace is a personal workspace
func (s *MentionService) isPersonalWorkspace(workspaceID int) (bool, error) {
	var isPersonal bool
	err := s.db.QueryRow(`SELECT is_personal FROM workspaces WHERE id = ?`, workspaceID).Scan(&isPersonal)
	if err != nil {
		return false, err
	}
	return isPersonal, nil
}

// getExistingMentions retrieves existing mentions for a source
func (s *MentionService) getExistingMentions(sourceType string, sourceID int) ([]models.Mention, error) {
	rows, err := s.db.Query(`
		SELECT id, mentioned_user_id, mentioned_user_display_name
		FROM mentions
		WHERE source_type = ? AND source_id = ?
	`, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mentions []models.Mention
	for rows.Next() {
		var m models.Mention
		if err := rows.Scan(&m.ID, &m.MentionedUserID, &m.MentionedUserDisplayName); err != nil {
			return nil, err
		}
		mentions = append(mentions, m)
	}

	return mentions, rows.Err()
}

// createMention inserts a new mention record
func (s *MentionService) createMention(m *models.Mention) error {
	_, err := s.db.ExecWrite(`
		INSERT INTO mentions (
			source_type, source_id, mentioned_user_id, item_id, workspace_id,
			created_by, mentioned_user_display_name, notification_sent, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, false, ?)
	`, m.SourceType, m.SourceID, m.MentionedUserID, m.ItemID, m.WorkspaceID,
		m.CreatedBy, m.MentionedUserDisplayName, time.Now())

	return err
}

// emitMentionNotification sends a notification for a new mention
func (s *MentionService) emitMentionNotification(params ProcessMentionsParams, mentionedUserID int, _ string) {
	if s.notificationService == nil {
		return
	}

	item, err := repository.NewItemRepository(s.db).FindByIDWithDetails(params.ItemID)
	if err != nil {
		slog.Error("Error fetching item details", slog.String("component", "mentions"), slog.Any("error", err))
		return
	}

	actorName := s.actorName(params.ActorUserID)

	itemKey := fmt.Sprintf("%s-%d", item.WorkspaceKey, item.WorkspaceItemNumber)

	var sourceTypeDesc string
	switch params.SourceType {
	case "comment":
		sourceTypeDesc = "a comment"
	case "item_description":
		sourceTypeDesc = "the description"
	default:
		sourceTypeDesc = "content"
	}

	// Mentions notify the resolved recipient directly after the visibility gate;
	// the rule-based pipeline cannot express this exact-recipient contract.
	title := "You were mentioned"
	message := fmt.Sprintf("%s mentioned you in %s on %s (%s)",
		actorName, sourceTypeDesc, item.Title, itemKey)
	if err := s.notificationService.NotifyUsers(
		[]int{mentionedUserID},
		params.WorkspaceID,
		params.ItemID,
		params.ActorUserID,
		"mention", // notifType — short form used by the UI; matches getNotificationType(EventMention)
		title,
		message,
	); err != nil {
		slog.Error("failed to add mention notification",
			slog.String("component", "mentions"),
			slog.Int("recipient_user_id", mentionedUserID),
			slog.Int("item_id", params.ItemID),
			slog.Any("error", err))
	}

	_, err = s.db.ExecWrite(`
		UPDATE mentions
		SET notification_sent = true
		WHERE source_type = ? AND source_id = ? AND mentioned_user_id = ?
	`, params.SourceType, params.SourceID, mentionedUserID)
	if err != nil {
		slog.Error("Error marking notification as sent", slog.String("component", "mentions"), slog.Any("error", err))
	}
}

// actorName returns the display name used in mention notifications.
func (s *MentionService) actorName(userID int) string {
	var firstName, lastName string
	err := s.db.QueryRow(`
		SELECT first_name, last_name FROM users WHERE id = ?
	`, userID).Scan(&firstName, &lastName)
	if err != nil {
		slog.Error("Error fetching actor name", slog.String("component", "mentions"), slog.Any("error", err))
		firstName = "Someone"
	}
	return strings.TrimSpace(firstName + " " + lastName)
}

// MilestoneCommentMentionParams describes a created or edited milestone
// comment whose @mentions should notify.
type MilestoneCommentMentionParams struct {
	Milestone       *MilestoneResult
	ActorUserID     int
	PreviousContent string // stored content before an edit; empty on create
	Content         string
	// CanView reports whether a mentioned user may view the milestone.
	CanView func(userID int) (bool, error)
}

// NotifyMilestoneCommentMentions notifies users @mentioned in a milestone
// comment. It follows the item comment rule: only users newly mentioned by
// this write are notified (an edit re-notifies nobody it already mentioned),
// the author is never notified, and users who can't view the milestone are
// skipped. Milestone comments keep no mention records, so "already mentioned"
// is read from the previous content. The notification opens the milestone.
func (s *MentionService) NotifyMilestoneCommentMentions(params MilestoneCommentMentionParams) error {
	if s.notificationService == nil || params.Milestone == nil {
		return nil
	}
	current, err := s.ResolveMentionedUserIDs(params.Content)
	if err != nil || len(current) == 0 {
		return err
	}
	previous, err := s.ResolveMentionedUserIDs(params.PreviousContent)
	if err != nil {
		return err
	}
	already := make(map[int]bool, len(previous))
	for _, userID := range previous {
		already[userID] = true
	}
	recipients := make([]int, 0, len(current))
	for _, userID := range current {
		if !already[userID] {
			recipients = append(recipients, userID)
		}
	}
	if len(recipients) == 0 {
		return nil
	}

	milestone := params.Milestone
	var workspaceID *int
	if !milestone.IsGlobal {
		workspaceID = milestone.WorkspaceID
	}
	message := fmt.Sprintf("%s mentioned you in a comment on milestone %s",
		s.actorName(params.ActorUserID), milestone.Name)
	return s.notificationService.NotifyUsersForMilestone(
		recipients,
		milestone.ID,
		workspaceID,
		params.ActorUserID,
		"mention", // same type as item mentions, so the UI toasts it
		"You were mentioned",
		message,
		params.CanView,
	)
}

// DeleteMentionsForSource removes all mentions for a source (called when comment/item is deleted)
func (s *MentionService) DeleteMentionsForSource(sourceType string, sourceID int) error {
	_, err := s.db.ExecWrite(`
		DELETE FROM mentions WHERE source_type = ? AND source_id = ?
	`, sourceType, sourceID)
	return err
}
