package v2

import (
	"errors"
	"net/http"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

// registerCannedResponseRoutes publishes workspace canned responses (WI-1138).
// Any workspace member with item view can list and preview; workspace admins
// manage the catalog.
func registerCannedResponseRoutes(builder *routeBuilder, deps Deps) {
	const responses = "/workspaces/{workspace_id}/canned-responses"
	const response = responses + "/{response_id}"
	builder.Read(responses, AuthAuthenticated, []string{"items:read"}, listCannedResponses(deps))
	builder.JSON(http.MethodPost, responses, http.StatusCreated, false, AuthAuthenticated, []string{"workspaces:write"}, createCannedResponse(deps))
	builder.Read(response, AuthAuthenticated, []string{"items:read"}, getCannedResponse(deps))
	builder.JSON(http.MethodPatch, response, http.StatusOK, true, AuthAuthenticated, []string{"workspaces:write"}, updateCannedResponse(deps))
	builder.Command(http.MethodDelete, response, AuthAuthenticated, []string{"workspaces:write"}, deleteCannedResponse(deps))
	builder.JSON(http.MethodPost, response+"/preview", http.StatusOK, false, AuthAuthenticated, []string{"items:read"}, previewCannedResponse(deps))
}

type cannedResponseCreateRequest struct {
	Name      string `json:"name"`
	Body      string `json:"body"`
	IsPrivate bool   `json:"is_private"`
}

type cannedResponsePatchRequest struct {
	Name      Optional[string] `json:"name"`
	Body      Optional[string] `json:"body"`
	IsPrivate Optional[bool]   `json:"is_private"`
	IsActive  Optional[bool]   `json:"is_active"`
}

type cannedResponsePreviewRequest struct {
	ItemID int `json:"item_id,omitempty"`
}

type cannedResponsePreviewResponse struct {
	Rendered string `json:"rendered"`
}

func listCannedResponses(deps Deps) readOperation[[]models.CannedResponse] {
	return func(r *http.Request) ([]models.CannedResponse, error) {
		user, workspaceID, err := principalAndWorkspace(r)
		if err != nil {
			return nil, err
		}
		if err := requireWorkspace(deps.Access.CanViewWorkspace, user.ID, workspaceID); err != nil {
			return nil, err
		}
		includeArchived := r.URL.Query().Get("include_archived") == "true"
		responses, err := deps.CannedResponses.List(workspaceID, includeArchived)
		if err != nil {
			return nil, internalError(err)
		}
		return responses, nil
	}
}

func getCannedResponse(deps Deps) readOperation[models.CannedResponse] {
	return func(r *http.Request) (models.CannedResponse, error) {
		user, workspaceID, err := principalAndWorkspace(r)
		if err != nil {
			return models.CannedResponse{}, err
		}
		if err := requireWorkspace(deps.Access.CanViewWorkspace, user.ID, workspaceID); err != nil {
			return models.CannedResponse{}, err
		}
		id, err := pathID(r, "response_id")
		if err != nil {
			return models.CannedResponse{}, err
		}
		cr, err := deps.CannedResponses.Get(workspaceID, id)
		if err != nil {
			return models.CannedResponse{}, cannedResponseError(err)
		}
		return *cr, nil
	}
}

func createCannedResponse(deps Deps) jsonOperation[cannedResponseCreateRequest, models.CannedResponse] {
	return func(r *http.Request, input cannedResponseCreateRequest) (models.CannedResponse, error) {
		user, workspaceID, err := principalAndWorkspace(r)
		if err != nil {
			return models.CannedResponse{}, err
		}
		if err := requireWorkspace(deps.Access.CanAdminWorkspace, user.ID, workspaceID); err != nil {
			return models.CannedResponse{}, err
		}
		cr, err := deps.CannedResponses.Create(workspaceID, services.CannedResponseInput{
			Name:      input.Name,
			Body:      input.Body,
			IsPrivate: input.IsPrivate,
			IsActive:  true,
		}, auditActor(r, user))
		if err != nil {
			return models.CannedResponse{}, cannedResponseError(err)
		}
		return *cr, nil
	}
}

func updateCannedResponse(deps Deps) jsonOperation[cannedResponsePatchRequest, models.CannedResponse] {
	return func(r *http.Request, input cannedResponsePatchRequest) (models.CannedResponse, error) {
		if input.Name.Null || input.Body.Null || input.IsPrivate.Null || input.IsActive.Null {
			return models.CannedResponse{}, newError(http.StatusBadRequest, "invalid_request", "Canned response fields cannot be null")
		}
		user, workspaceID, err := principalAndWorkspace(r)
		if err != nil {
			return models.CannedResponse{}, err
		}
		if err := requireWorkspace(deps.Access.CanAdminWorkspace, user.ID, workspaceID); err != nil {
			return models.CannedResponse{}, err
		}
		id, err := pathID(r, "response_id")
		if err != nil {
			return models.CannedResponse{}, err
		}
		update := services.CannedResponseUpdate{}
		if input.Name.Set {
			update.Name = &input.Name.Value
		}
		if input.Body.Set {
			update.Body = &input.Body.Value
		}
		if input.IsPrivate.Set {
			update.IsPrivate = &input.IsPrivate.Value
		}
		if input.IsActive.Set {
			update.IsActive = &input.IsActive.Value
		}
		cr, err := deps.CannedResponses.Update(workspaceID, id, update, auditActor(r, user))
		if err != nil {
			return models.CannedResponse{}, cannedResponseError(err)
		}
		return *cr, nil
	}
}

func deleteCannedResponse(deps Deps) commandOperation {
	return func(r *http.Request) error {
		user, workspaceID, err := principalAndWorkspace(r)
		if err != nil {
			return err
		}
		if err := requireWorkspace(deps.Access.CanAdminWorkspace, user.ID, workspaceID); err != nil {
			return err
		}
		id, err := pathID(r, "response_id")
		if err != nil {
			return err
		}
		_, err = deps.CannedResponses.Delete(workspaceID, id, auditActor(r, user))
		return cannedResponseError(err)
	}
}

func previewCannedResponse(deps Deps) jsonOperation[cannedResponsePreviewRequest, cannedResponsePreviewResponse] {
	return func(r *http.Request, input cannedResponsePreviewRequest) (cannedResponsePreviewResponse, error) {
		user, workspaceID, err := principalAndWorkspace(r)
		if err != nil {
			return cannedResponsePreviewResponse{}, err
		}
		if err := requireWorkspace(deps.Access.CanViewWorkspace, user.ID, workspaceID); err != nil {
			return cannedResponsePreviewResponse{}, err
		}
		id, err := pathID(r, "response_id")
		if err != nil {
			return cannedResponsePreviewResponse{}, err
		}
		rendered, err := deps.CannedResponses.RenderPreview(workspaceID, id, input.ItemID, auditActor(r, user))
		if err != nil {
			return cannedResponsePreviewResponse{}, cannedResponseError(err)
		}
		return cannedResponsePreviewResponse{Rendered: rendered}, nil
	}
}

func cannedResponseError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, services.ErrCannedResponseWorkspaceWrong):
		return newError(http.StatusNotFound, "not_found", "Canned response was not found")
	case errors.Is(err, services.ErrCannedResponseNameRequired), errors.Is(err, services.ErrCannedResponseBodyRequired):
		return newError(http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, repository.ErrDuplicateEntry):
		return newError(http.StatusConflict, "conflict", "A canned response with this name already exists")
	default:
		return internalError(err)
	}
}
