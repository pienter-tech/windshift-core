package services

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/sanitize"
)

// ErrMilestoneCommentNotAuthor rejects edits and deletes of another user's
// milestone comment. Only the author may change their own comment.
var ErrMilestoneCommentNotAuthor = errors.New("only the author can change this milestone comment")

// MilestoneReadAuthorizer confirms that a user may view a milestone.
// PlanningApplicationService implements it.
type MilestoneReadAuthorizer interface {
	AuthorizeMilestoneRead(userID, milestoneID int) (*MilestoneResult, error)
}

// MilestoneCommentStore is the persistence the comment service needs.
// repository.MilestoneCommentRepository implements it.
type MilestoneCommentStore interface {
	ListByMilestone(milestoneID, limit, offset int, desc bool) ([]models.MilestoneComment, error)
	CountByMilestone(milestoneID int) (int, error)
	GetByID(id int) (*models.MilestoneComment, error)
	Create(milestoneID, authorID int, content string) (int, error)
	UpdateContent(id int, content string) error
	Delete(id int) error
}

// MilestoneCommentMentionNotifier notifies users @mentioned in a milestone
// comment. MentionService implements it.
type MilestoneCommentMentionNotifier interface {
	NotifyMilestoneCommentMentions(params MilestoneCommentMentionParams) error
}

// MilestoneCommentService owns Markdown comments on milestones (WCORE-20).
// Anyone who can view the milestone may read and add comments; authors edit
// and delete their own. @mentions notify users who can view the milestone
// (WCORE-25); milestone comments emit no item events, mention records, or
// webhooks.
type MilestoneCommentService struct {
	comments MilestoneCommentStore
	access   MilestoneReadAuthorizer
	mentions MilestoneCommentMentionNotifier
}

// NewMilestoneCommentService creates a MilestoneCommentService.
func NewMilestoneCommentService(comments MilestoneCommentStore, access MilestoneReadAuthorizer) *MilestoneCommentService {
	return &MilestoneCommentService{comments: comments, access: access}
}

// SetMentionNotifier wires @mention notifications. Without one, mentions
// stay plain text.
func (s *MilestoneCommentService) SetMentionNotifier(mentions MilestoneCommentMentionNotifier) {
	s.mentions = mentions
}

// MilestoneCommentListParams selects one page of a milestone's comments.
type MilestoneCommentListParams struct {
	Limit  int
	Offset int
	Desc   bool
}

// List returns one page of a milestone's comments and the total count.
func (s *MilestoneCommentService) List(userID, milestoneID int, params MilestoneCommentListParams) ([]models.MilestoneComment, int, error) {
	if _, err := s.access.AuthorizeMilestoneRead(userID, milestoneID); err != nil {
		return nil, 0, err
	}
	rows, err := s.comments.ListByMilestone(milestoneID, params.Limit, params.Offset, params.Desc)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.comments.CountByMilestone(milestoneID)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Create adds a comment authored by userID to a milestone the user can view.
func (s *MilestoneCommentService) Create(userID, milestoneID int, content string) (*models.MilestoneComment, error) {
	milestone, err := s.access.AuthorizeMilestoneRead(userID, milestoneID)
	if err != nil {
		return nil, err
	}
	clean, err := cleanMilestoneCommentContent(content)
	if err != nil {
		return nil, err
	}
	id, err := s.comments.Create(milestoneID, userID, clean)
	if err != nil {
		return nil, err
	}
	s.notifyMentions(userID, milestone, "", clean)
	return s.comments.GetByID(id)
}

// Update replaces the Markdown of the caller's own comment.
func (s *MilestoneCommentService) Update(userID, milestoneID, commentID int, content string) (*models.MilestoneComment, error) {
	milestone, existing, err := s.requireAuthor(userID, milestoneID, commentID)
	if err != nil {
		return nil, err
	}
	clean, err := cleanMilestoneCommentContent(content)
	if err != nil {
		return nil, err
	}
	if err := s.comments.UpdateContent(commentID, clean); err != nil {
		return nil, err
	}
	s.notifyMentions(userID, milestone, existing.Content, clean)
	return s.comments.GetByID(commentID)
}

// Delete removes the caller's own comment.
func (s *MilestoneCommentService) Delete(userID, milestoneID, commentID int) error {
	if _, _, err := s.requireAuthor(userID, milestoneID, commentID); err != nil {
		return err
	}
	return s.comments.Delete(commentID)
}

// requireAuthor confirms the user can view the milestone and wrote the
// comment. A comment on a different milestone reads as not found.
func (s *MilestoneCommentService) requireAuthor(userID, milestoneID, commentID int) (*MilestoneResult, *models.MilestoneComment, error) {
	milestone, err := s.access.AuthorizeMilestoneRead(userID, milestoneID)
	if err != nil {
		return nil, nil, err
	}
	comment, err := s.comments.GetByID(commentID)
	if err != nil {
		return nil, nil, err
	}
	if comment.MilestoneID != milestoneID {
		return nil, nil, fmt.Errorf("milestone comment %d not on milestone %d: %w", commentID, milestoneID, repository.ErrNotFound)
	}
	if comment.AuthorID != userID {
		return nil, nil, ErrMilestoneCommentNotAuthor
	}
	return milestone, comment, nil
}

// notifyMentions notifies users newly @mentioned by a comment write who can
// view the milestone. Like item comments, a failure is logged and does not
// fail the write.
func (s *MilestoneCommentService) notifyMentions(actorUserID int, milestone *MilestoneResult, previousContent, content string) {
	if s.mentions == nil {
		return
	}
	err := s.mentions.NotifyMilestoneCommentMentions(MilestoneCommentMentionParams{
		Milestone:       milestone,
		ActorUserID:     actorUserID,
		PreviousContent: previousContent,
		Content:         content,
		CanView: func(userID int) (bool, error) {
			_, err := s.access.AuthorizeMilestoneRead(userID, milestone.ID)
			if errors.Is(err, ErrPlanningForbidden) {
				return false, nil
			}
			return err == nil, err
		},
	})
	if err != nil {
		slog.Warn("failed to notify milestone comment mentions",
			slog.Int("milestone_id", milestone.ID),
			slog.Any("error", err))
	}
}

// milestoneCommentBreakRegex matches the `<br />` hard breaks RichText keeps
// for the Markdown editor; a comment of only breaks counts as empty.
var milestoneCommentBreakRegex = regexp.MustCompile(`(?i)<br\s*/?>`)

// cleanMilestoneCommentContent uses RichText rather than Comment so the
// editor's `<br />` hard breaks survive; Comment strips them.
func cleanMilestoneCommentContent(content string) (string, error) {
	clean := sanitize.RichText.Sanitize(content)
	if strings.TrimSpace(milestoneCommentBreakRegex.ReplaceAllString(clean, "")) == "" {
		return "", planningValidationError("content", "content is required")
	}
	return clean, nil
}
