package services

import (
	"encoding/json"
	"fmt"

	"windshift/internal/models"
	"windshift/internal/repository"
)

// CustomerNotifier emails a portal customer through the threaded reply
// transport. EmailReplyService implements it; tests substitute fakes.
type CustomerNotifier interface {
	// SendAutomationNotice emails the portal customer who created the item.
	// It reports whether mail was delivered and, when nothing was sent, a
	// short skip reason ("no_customer", "no_email", ...). A non-nil error
	// means the transport itself failed.
	SendAutomationNotice(itemID, actorID int, subject, message string) (delivered bool, skipReason string, err error)
}

// RegisterHelpdeskNodeExecutors registers the WI-1132/WI-1138 helpdesk node
// executors. Call once at startup after the canned-response, comment, and
// email-reply services exist.
func (as *ActionService) RegisterHelpdeskNodeExecutors(canned *CannedResponseService, comments *CommentService, notifier CustomerNotifier) {
	as.RegisterNodeExecutor(NewInsertCannedResponseExecutor(canned, comments, as))
	as.RegisterNodeExecutor(NewNotifyCustomerExecutor(notifier, as))
	as.RegisterNodeExecutor(NewAdjustLabelsExecutor(repository.NewLabelRepository(as.db), as))
}

// InsertCannedResponseExecutor renders a workspace canned response (WI-1138)
// and posts it as a comment on the trigger item. Private snippets are posted
// as private comments so they never reach portal customers; public snippets
// flow through the normal reply-email path.
type InsertCannedResponseExecutor struct {
	canned   *CannedResponseService
	comments *CommentService
	api      NodeAPI
}

// NewInsertCannedResponseExecutor returns an executor ready to register.
// Nil required deps surface as configuration errors at run time.
func NewInsertCannedResponseExecutor(canned *CannedResponseService, comments *CommentService, api NodeAPI) *InsertCannedResponseExecutor {
	return &InsertCannedResponseExecutor{canned: canned, comments: comments, api: api}
}

// NodeType pins the node-type dispatch key.
func (e *InsertCannedResponseExecutor) NodeType() models.ActionNodeType {
	return models.ActionNodeInsertCannedResponse
}

// Execute renders the referenced canned response against the trigger item
// and creates the comment. Archived or missing responses fail the step so
// run history shows the broken reference.
func (e *InsertCannedResponseExecutor) Execute(node *models.ActionNode, ctx *models.ExecutionContext, stepResult *models.StepResult) error {
	if e.canned == nil || e.comments == nil || e.api == nil {
		return fmt.Errorf("insert_canned_response executor missing deps (canned response / comment service / NodeAPI)")
	}
	itemID := currentActionItemID(ctx)
	workspaceID := currentActionWorkspaceID(ctx)
	if itemID <= 0 || workspaceID <= 0 {
		return fmt.Errorf("insert_canned_response requires an item context")
	}

	var config models.InsertCannedResponseNodeConfig
	if err := json.Unmarshal([]byte(node.NodeConfig), &config); err != nil {
		return fmt.Errorf("invalid insert_canned_response config: %w", err)
	}
	if config.CannedResponseID <= 0 {
		return fmt.Errorf("insert_canned_response: canned_response_id is required")
	}

	actor := ctx.EffectiveActorID
	rendered, isPrivate, err := e.canned.RenderForItem(workspaceID, config.CannedResponseID, itemID, true, AuditActor{UserID: actor})
	if err != nil {
		return fmt.Errorf("insert_canned_response: %w", err)
	}

	// A private/internal trigger comment must never turn a public snippet into
	// a customer-visible reply; post it as an internal note instead.
	privacyDowngraded := false
	if triggerCommentIsPrivate(ctx) && !isPrivate {
		isPrivate = true
		privacyDowngraded = true
	}

	result, err := e.comments.Create(CreateCommentParams{
		ItemID:        itemID,
		AuthorID:      actor,
		Content:       rendered,
		IsPrivate:     isPrivate,
		ActorUserID:   actor,
		EventMetadata: itemEventMetadata(actor, "automation", actionContextFromExecution(ctx)),
	})
	if err != nil {
		return fmt.Errorf("insert_canned_response: failed to create comment: %w", err)
	}
	e.canned.TouchUsage(config.CannedResponseID)

	stepResult.Output = map[string]any{
		"canned_response_id": config.CannedResponseID,
		"is_private":         isPrivate,
		"comment_id":         result.CommentID,
	}
	if privacyDowngraded {
		stepResult.Output["privacy_downgraded"] = true
	}
	return nil
}

// NotifyCustomerExecutor emails the portal customer who created the trigger
// item through the threaded reply transport (WI-1132).
type NotifyCustomerExecutor struct {
	notifier CustomerNotifier
	api      NodeAPI
}

// NewNotifyCustomerExecutor returns an executor ready to register. A nil
// notifier surfaces as a configuration error at run time.
func NewNotifyCustomerExecutor(notifier CustomerNotifier, api NodeAPI) *NotifyCustomerExecutor {
	return &NotifyCustomerExecutor{notifier: notifier, api: api}
}

// NodeType pins the node-type dispatch key.
func (e *NotifyCustomerExecutor) NodeType() models.ActionNodeType {
	return models.ActionNodeNotifyCustomer
}

// Execute renders the configured subject and message and hands them to the
// customer notifier. Items without a customer, email, or SMTP setup skip
// with a recorded reason instead of failing the action.
func (e *NotifyCustomerExecutor) Execute(node *models.ActionNode, ctx *models.ExecutionContext, stepResult *models.StepResult) error {
	if e.notifier == nil || e.api == nil {
		return fmt.Errorf("notify_customer executor missing deps (customer notifier / NodeAPI)")
	}
	itemID := currentActionItemID(ctx)
	if itemID <= 0 {
		return fmt.Errorf("notify_customer requires an item context")
	}

	var config models.NotifyCustomerNodeConfig
	if err := json.Unmarshal([]byte(node.NodeConfig), &config); err != nil {
		return fmt.Errorf("invalid notify_customer config: %w", err)
	}
	if config.Message == "" {
		return fmt.Errorf("notify_customer: message is required")
	}

	// A private/internal trigger comment must never email the customer.
	if triggerCommentIsPrivate(ctx) {
		stepResult.Output = map[string]any{"delivered": false, "skip_reason": "trigger_comment_private"}
		return nil
	}

	message := e.api.SubstituteVariables(config.Message, ctx)
	subject := e.api.SubstituteVariables(config.Subject, ctx)

	delivered, skipReason, err := e.notifier.SendAutomationNotice(itemID, ctx.EffectiveActorID, subject, message)
	if err != nil {
		return fmt.Errorf("notify_customer: %w", err)
	}
	output := map[string]any{"delivered": delivered}
	if skipReason != "" {
		output["skip_reason"] = skipReason
	}
	stepResult.Output = output
	return nil
}

// AdjustLabelsExecutor adds and removes labels on the trigger item (WI-1132).
type AdjustLabelsExecutor struct {
	labels *repository.LabelRepository
	api    NodeAPI
}

// NewAdjustLabelsExecutor returns an executor ready to register.
func NewAdjustLabelsExecutor(labels *repository.LabelRepository, api NodeAPI) *AdjustLabelsExecutor {
	return &AdjustLabelsExecutor{labels: labels, api: api}
}

// NodeType pins the node-type dispatch key.
func (e *AdjustLabelsExecutor) NodeType() models.ActionNodeType {
	return models.ActionNodeAdjustLabels
}

// Execute applies the configured label changes. Referenced labels that no
// longer exist are skipped and reported rather than failing the run.
func (e *AdjustLabelsExecutor) Execute(node *models.ActionNode, ctx *models.ExecutionContext, stepResult *models.StepResult) error {
	if e.labels == nil || e.api == nil {
		return fmt.Errorf("adjust_labels executor missing deps (label repository / NodeAPI)")
	}
	itemID := currentActionItemID(ctx)
	workspaceID := currentActionWorkspaceID(ctx)
	if itemID <= 0 || workspaceID <= 0 {
		return fmt.Errorf("adjust_labels requires an item context")
	}

	var config models.AdjustLabelsNodeConfig
	if err := json.Unmarshal([]byte(node.NodeConfig), &config); err != nil {
		return fmt.Errorf("invalid adjust_labels config: %w", err)
	}
	if len(config.AddLabelIDs) == 0 && len(config.RemoveLabelIDs) == 0 {
		return fmt.Errorf("adjust_labels: at least one of add_label_ids or remove_label_ids is required")
	}
	if err := e.api.AuthorizeWorkspaceMutation(ctx.EffectiveActorID, workspaceID, models.PermissionItemEdit); err != nil {
		return err
	}

	output := map[string]any{"added": []int{}, "removed": []int{}, "missing": []int{}}
	added := []int{}
	removed := []int{}
	missing := []int{}
	apply := func(ids []int, add bool) error {
		for _, labelID := range ids {
			if labelID <= 0 {
				continue
			}
			if _, err := e.labels.GetByID(labelID); err != nil {
				if err == repository.ErrNotFound {
					missing = append(missing, labelID)
					continue
				}
				return fmt.Errorf("adjust_labels: load label %d: %w", labelID, err)
			}
			var err error
			if add {
				err = e.labels.AddItemLabel(itemID, labelID)
			} else {
				err = e.labels.RemoveItemLabel(itemID, labelID)
			}
			if err != nil && err != repository.ErrDuplicateEntry {
				return fmt.Errorf("adjust_labels: apply label %d: %w", labelID, err)
			}
			if add {
				added = append(added, labelID)
			} else {
				removed = append(removed, labelID)
			}
		}
		return nil
	}
	if err := apply(config.AddLabelIDs, true); err != nil {
		return err
	}
	if err := apply(config.RemoveLabelIDs, false); err != nil {
		return err
	}

	output["added"], output["removed"], output["missing"] = added, removed, missing
	stepResult.Output = output
	return nil
}
