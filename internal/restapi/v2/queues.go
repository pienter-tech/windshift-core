package v2

import (
	"errors"
	"net/http"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
)

// registerQueueRoutes publishes scope-aware support queues. Queues live on a
// collection or on the workspace default view; built-in presets are virtual
// and only their dismissal is persisted. Reads require item view; writes
// require collection write scope and the service's own scope gate.
func registerQueueRoutes(builder *routeBuilder, app *services.ItemApplicationService) {
	const workspaceQueues = "/workspaces/{workspace_key}/queues"
	const workspaceBuiltin = workspaceQueues + "/builtins/{builtin_key}"
	const collectionQueues = "/collections/{collection_id}/queues"
	const collectionBuiltin = collectionQueues + "/builtins/{builtin_key}"
	const queue = "/queues/{queue_id}"

	builder.Read(workspaceQueues, AuthAuthenticated, []string{"items:read"}, listWorkspaceQueues(app))
	builder.JSON(http.MethodPost, workspaceQueues, http.StatusCreated, false, AuthAuthenticated, []string{"collections:write"}, createWorkspaceQueue(app))
	builder.JSON(http.MethodPut, workspaceQueues+"/order", http.StatusOK, false, AuthAuthenticated, []string{"collections:write"}, reorderWorkspaceQueues(app))
	builder.JSON(http.MethodPut, workspaceBuiltin, http.StatusOK, false, AuthAuthenticated, []string{"collections:write"}, setWorkspaceBuiltinHidden(app))

	builder.Read(collectionQueues, AuthAuthenticated, []string{"items:read"}, listCollectionQueues(app))
	builder.JSON(http.MethodPost, collectionQueues, http.StatusCreated, false, AuthAuthenticated, []string{"collections:write"}, createCollectionQueue(app))
	builder.JSON(http.MethodPut, collectionQueues+"/order", http.StatusOK, false, AuthAuthenticated, []string{"collections:write"}, reorderCollectionQueues(app))
	builder.JSON(http.MethodPut, collectionBuiltin, http.StatusOK, false, AuthAuthenticated, []string{"collections:write"}, setCollectionBuiltinHidden(app))

	builder.JSON(http.MethodPatch, queue, http.StatusOK, true, AuthAuthenticated, []string{"collections:write"}, updateQueue(app))
	builder.Command(http.MethodDelete, queue, AuthAuthenticated, []string{"collections:write"}, deleteQueue(app))
}

type queueWriteRequest struct {
	Name        string  `json:"name"`
	QLQuery     string  `json:"ql_query"`
	FilterState *string `json:"filter_state"`
}

type queueOrderRequest struct {
	IDs []int `json:"ids"`
}

type builtinQueueHiddenRequest struct {
	Hidden bool `json:"hidden"`
}

func listWorkspaceQueues(app *services.ItemApplicationService) readOperation[[]services.QueueView] {
	return func(r *http.Request) ([]services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return nil, err
		}
		workspaceID, err := resolveWorkspaceKey(app, r)
		if err != nil {
			return nil, err
		}
		queues, err := app.ListQueues(r.Context(), user.ID, workspaceID, nil)
		if err != nil {
			return nil, queueError(err)
		}
		return queues, nil
	}
}

func listCollectionQueues(app *services.ItemApplicationService) readOperation[[]services.QueueView] {
	return func(r *http.Request) ([]services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return nil, err
		}
		collectionID, err := pathID(r, "collection_id")
		if err != nil {
			return nil, err
		}
		queues, err := app.ListQueues(r.Context(), user.ID, 0, &collectionID)
		if err != nil {
			return nil, queueError(err)
		}
		return queues, nil
	}
}

func createWorkspaceQueue(app *services.ItemApplicationService) jsonOperation[queueWriteRequest, services.QueueView] {
	return func(r *http.Request, input queueWriteRequest) (services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return services.QueueView{}, err
		}
		workspaceID, err := resolveWorkspaceKey(app, r)
		if err != nil {
			return services.QueueView{}, err
		}
		return createQueue(r, app, user, workspaceID, nil, input)
	}
}

func createCollectionQueue(app *services.ItemApplicationService) jsonOperation[queueWriteRequest, services.QueueView] {
	return func(r *http.Request, input queueWriteRequest) (services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return services.QueueView{}, err
		}
		collectionID, err := pathID(r, "collection_id")
		if err != nil {
			return services.QueueView{}, err
		}
		return createQueue(r, app, user, 0, &collectionID, input)
	}
}

func createQueue(r *http.Request, app *services.ItemApplicationService, user *models.User, workspaceID int, collectionID *int, input queueWriteRequest) (services.QueueView, error) {
	view, err := app.CreateQueue(r.Context(), auditActor(r, user), workspaceID, collectionID, services.QueueInput{
		Name:        input.Name,
		QL:          input.QLQuery,
		FilterState: input.FilterState,
	})
	if err != nil {
		return services.QueueView{}, queueError(err)
	}
	return *view, nil
}

func reorderWorkspaceQueues(app *services.ItemApplicationService) jsonOperation[queueOrderRequest, []services.QueueView] {
	return func(r *http.Request, input queueOrderRequest) ([]services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return nil, err
		}
		workspaceID, err := resolveWorkspaceKey(app, r)
		if err != nil {
			return nil, err
		}
		if err := app.ReorderQueues(auditActor(r, user), workspaceID, nil, input.IDs); err != nil {
			return nil, queueError(err)
		}
		queues, err := app.ListQueues(r.Context(), user.ID, workspaceID, nil)
		if err != nil {
			return nil, queueError(err)
		}
		return queues, nil
	}
}

func reorderCollectionQueues(app *services.ItemApplicationService) jsonOperation[queueOrderRequest, []services.QueueView] {
	return func(r *http.Request, input queueOrderRequest) ([]services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return nil, err
		}
		collectionID, err := pathID(r, "collection_id")
		if err != nil {
			return nil, err
		}
		if err := app.ReorderQueues(auditActor(r, user), 0, &collectionID, input.IDs); err != nil {
			return nil, queueError(err)
		}
		queues, err := app.ListQueues(r.Context(), user.ID, 0, &collectionID)
		if err != nil {
			return nil, queueError(err)
		}
		return queues, nil
	}
}

func setWorkspaceBuiltinHidden(app *services.ItemApplicationService) jsonOperation[builtinQueueHiddenRequest, []services.QueueView] {
	return func(r *http.Request, input builtinQueueHiddenRequest) ([]services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return nil, err
		}
		workspaceID, err := resolveWorkspaceKey(app, r)
		if err != nil {
			return nil, err
		}
		return setBuiltinHidden(r, app, user, workspaceID, nil, input.Hidden)
	}
}

func setCollectionBuiltinHidden(app *services.ItemApplicationService) jsonOperation[builtinQueueHiddenRequest, []services.QueueView] {
	return func(r *http.Request, input builtinQueueHiddenRequest) ([]services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return nil, err
		}
		collectionID, err := pathID(r, "collection_id")
		if err != nil {
			return nil, err
		}
		return setBuiltinHidden(r, app, user, 0, &collectionID, input.Hidden)
	}
}

func setBuiltinHidden(r *http.Request, app *services.ItemApplicationService, user *models.User, workspaceID int, collectionID *int, hidden bool) ([]services.QueueView, error) {
	if err := app.SetBuiltinQueueHidden(auditActor(r, user), workspaceID, collectionID, r.PathValue("builtin_key"), hidden); err != nil {
		return nil, queueError(err)
	}
	queues, err := app.ListQueues(r.Context(), user.ID, workspaceID, collectionID)
	if err != nil {
		return nil, queueError(err)
	}
	return queues, nil
}

func updateQueue(app *services.ItemApplicationService) jsonOperation[queueWriteRequest, services.QueueView] {
	return func(r *http.Request, input queueWriteRequest) (services.QueueView, error) {
		user, err := principal(r)
		if err != nil {
			return services.QueueView{}, err
		}
		queueID, err := pathID(r, "queue_id")
		if err != nil {
			return services.QueueView{}, err
		}
		view, err := app.UpdateQueue(r.Context(), auditActor(r, user), queueID, services.QueueInput{
			Name:        input.Name,
			QL:          input.QLQuery,
			FilterState: input.FilterState,
		})
		if err != nil {
			return services.QueueView{}, queueError(err)
		}
		return *view, nil
	}
}

func deleteQueue(app *services.ItemApplicationService) commandOperation {
	return func(r *http.Request) error {
		user, err := principal(r)
		if err != nil {
			return err
		}
		queueID, err := pathID(r, "queue_id")
		if err != nil {
			return err
		}
		return queueError(app.DeleteQueue(auditActor(r, user), queueID))
	}
}

func resolveWorkspaceKey(app *services.ItemApplicationService, r *http.Request) (int, error) {
	workspaceID, err := app.ResolveWorkspaceIDByKey(r.PathValue("workspace_key"))
	if errors.Is(err, repository.ErrNotFound) {
		return 0, newError(http.StatusNotFound, "not_found", "Workspace was not found")
	}
	if err != nil {
		return 0, internalError(err)
	}
	return workspaceID, nil
}

func queueError(err error) error {
	if err == nil {
		return nil
	}
	var validation *services.QueueValidationError
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return newError(http.StatusNotFound, "not_found", "Queue was not found")
	case errors.As(err, &validation):
		return newError(http.StatusBadRequest, "invalid_request", validation.Message)
	case errors.Is(err, services.ErrQLQuery):
		return newError(http.StatusBadRequest, "invalid_request", err.Error())
	default:
		return internalError(err)
	}
}
