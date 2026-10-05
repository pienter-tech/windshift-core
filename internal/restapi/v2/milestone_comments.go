package v2

import (
	"errors"
	"net/http"
	"strings"

	"windshift/internal/markdown"
	"windshift/internal/models"
	"windshift/internal/services"
)

// registerMilestoneCommentRoutes publishes Markdown comments on milestones
// (WCORE-20). The same routes serve global and workspace-local milestones:
// anyone who can view the milestone reads and adds comments, and authors
// edit and delete their own. Milestone comments send no notifications.
func registerMilestoneCommentRoutes(builder *routeBuilder, deps Deps) {
	const comments = "/milestones/{milestone_id}/comments"
	const comment = comments + "/{comment_id}"
	builder.Page(comments, AuthAuthenticated, []string{"milestones:read"}, listMilestoneComments(deps.MilestoneComments))
	builder.JSON(http.MethodPost, comments, http.StatusCreated, false, AuthAuthenticated, []string{"milestones:write"}, createMilestoneComment(deps.MilestoneComments))
	builder.JSON(http.MethodPatch, comment, http.StatusOK, true, AuthAuthenticated, []string{"milestones:write"}, updateMilestoneComment(deps.MilestoneComments))
	builder.Command(http.MethodDelete, comment, AuthAuthenticated, []string{"milestones:delete"}, deleteMilestoneComment(deps.MilestoneComments))
}

type milestoneCommentCreateRequest struct {
	Content string `json:"content"`
}

type milestoneCommentPatchRequest struct {
	Content Optional[string] `json:"content"`
}

func listMilestoneComments(comments milestoneCommentApplication) pageOperation[models.MilestoneComment] {
	return func(r *http.Request) ([]models.MilestoneComment, Pagination, int, error) {
		user, milestoneID, err := planningTarget(r, "milestone_id")
		if err != nil {
			return nil, Pagination{}, 0, err
		}
		page, err := ParsePagination(r, map[string]bool{"created_at": true}, "created_at")
		if err != nil {
			return nil, Pagination{}, 0, err
		}
		rows, total, err := comments.List(user.ID, milestoneID, services.MilestoneCommentListParams{
			Limit: page.PageSize, Offset: page.Offset, Desc: page.Desc,
		})
		if err != nil {
			return nil, page, 0, milestoneCommentError(err)
		}
		for i := range rows {
			if err := renderMilestoneCommentHTML(&rows[i]); err != nil {
				return nil, page, 0, internalError(err)
			}
		}
		return rows, page, total, nil
	}
}

func createMilestoneComment(comments milestoneCommentApplication) jsonOperation[milestoneCommentCreateRequest, models.MilestoneComment] {
	return func(r *http.Request, input milestoneCommentCreateRequest) (models.MilestoneComment, error) {
		user, milestoneID, err := planningTarget(r, "milestone_id")
		if err != nil {
			return models.MilestoneComment{}, err
		}
		if strings.TrimSpace(input.Content) == "" {
			return models.MilestoneComment{}, newError(http.StatusBadRequest, "invalid_request", "content is required")
		}
		created, err := comments.Create(user.ID, milestoneID, input.Content)
		if err != nil {
			return models.MilestoneComment{}, milestoneCommentError(err)
		}
		if err := renderMilestoneCommentHTML(created); err != nil {
			return models.MilestoneComment{}, internalError(err)
		}
		return *created, nil
	}
}

func updateMilestoneComment(comments milestoneCommentApplication) jsonOperation[milestoneCommentPatchRequest, models.MilestoneComment] {
	return func(r *http.Request, input milestoneCommentPatchRequest) (models.MilestoneComment, error) {
		if !input.Content.Set || input.Content.Null || strings.TrimSpace(input.Content.Value) == "" {
			return models.MilestoneComment{}, newError(http.StatusBadRequest, "invalid_request", "content is required")
		}
		user, milestoneID, commentID, err := milestoneCommentTarget(r)
		if err != nil {
			return models.MilestoneComment{}, err
		}
		updated, err := comments.Update(user.ID, milestoneID, commentID, input.Content.Value)
		if err != nil {
			return models.MilestoneComment{}, milestoneCommentError(err)
		}
		if err := renderMilestoneCommentHTML(updated); err != nil {
			return models.MilestoneComment{}, internalError(err)
		}
		return *updated, nil
	}
}

func deleteMilestoneComment(comments milestoneCommentApplication) commandOperation {
	return func(r *http.Request) error {
		user, milestoneID, commentID, err := milestoneCommentTarget(r)
		if err != nil {
			return err
		}
		return milestoneCommentError(comments.Delete(user.ID, milestoneID, commentID))
	}
}

func milestoneCommentTarget(r *http.Request) (user *models.User, milestoneID, commentID int, err error) {
	user, milestoneID, err = planningTarget(r, "milestone_id")
	if err != nil {
		return nil, 0, 0, err
	}
	commentID, err = pathID(r, "comment_id")
	return user, milestoneID, commentID, err
}

func renderMilestoneCommentHTML(comment *models.MilestoneComment) error {
	html, err := markdown.Render(comment.Content)
	if err != nil {
		return err
	}
	comment.ContentHTML = html
	return nil
}

// milestoneCommentError maps service errors. Unreadable milestones and
// comments on other milestones read as not found (planningError); editing
// someone else's comment is forbidden.
func milestoneCommentError(err error) error {
	if errors.Is(err, services.ErrMilestoneCommentNotAuthor) {
		return newError(http.StatusForbidden, "insufficient_permission", "Only the author can change this comment")
	}
	return planningError(err)
}
