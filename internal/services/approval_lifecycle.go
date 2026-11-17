package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

func (s *ApprovalService) RequestApproval(ctx context.Context, itemID, statusID, fromStatusID, triggeredByUserID int) (*models.ApprovalRequest, error) {
	item, err := repository.NewItemRepository(s.db).FindByID(itemID)
	if err != nil {
		return nil, fmt.Errorf("load item: %w", err)
	}
	ass, err := s.GetApprovalSetStatusForItem(ctx, item.WorkspaceID, item.ItemTypeID, statusID)
	if err != nil {
		return nil, fmt.Errorf("resolve approval set: %w", err)
	}
	if ass == nil {
		return nil, nil
	}

	steps, err := s.templateRepo.FindStepsByStatusID(ctx, ass.ID)
	if err != nil {
		return nil, fmt.Errorf("load steps: %w", err)
	}
	if len(steps) == 0 {
		return nil, nil // misconfigured set; treat as no-op
	}

	requestID, err := database.WithTxResult(s.db, func(tx database.Tx) (int, error) {
		var fromStatusPtr *int
		if fromStatusID > 0 {
			fromStatusPtr = &fromStatusID
		}
		reqID, err := s.runtimeRepo.CreateRequest(ctx, tx, itemID, ass.ID, statusID, fromStatusPtr, triggeredByUserID)
		if err != nil {
			return 0, fmt.Errorf("insert approval_request: %w", err)
		}

		now := time.Now()
		stepInstanceIDs := make([]int, len(steps))
		for i, step := range steps {
			startedAt := sql.NullTime{}
			if ass.StepMode == models.ApprovalStepModeParallel || i == 0 {
				startedAt = sql.NullTime{Time: now, Valid: true}
			}
			var dueAt sql.NullTime
			if step.EscalationAfterHours != nil && startedAt.Valid {
				dueAt = sql.NullTime{Time: now.Add(time.Duration(*step.EscalationAfterHours) * time.Hour), Valid: true}
			}
			sid, err := s.runtimeRepo.CreateStepInstance(ctx, tx, reqID, step.ID, step.DisplayOrder, startedAt, dueAt)
			if err != nil {
				return 0, fmt.Errorf("insert step instance: %w", err)
			}
			stepInstanceIDs[i] = sid
		}

		for i, step := range steps {
			startedNow := ass.StepMode == models.ApprovalStepModeParallel || i == 0
			if !startedNow {
				continue
			}
			if err := s.resolveAndSnapshotApprovers(ctx, tx, stepInstanceIDs[i], step, item, triggeredByUserID); err != nil {
				return 0, fmt.Errorf("resolve approvers (step %d): %w", step.DisplayOrder, err)
			}
		}

		if _, err := s.runtimeRepo.WriteDecision(ctx, tx, reqID, nil, nil, nil, models.ApprovalDecisionRequested, "", nil, map[string]any{
			"triggered_by_user_id": triggeredByUserID,
			"approval_set_status":  ass.ID,
			"step_mode":            ass.StepMode,
			"step_count":           len(steps),
		}); err != nil {
			return 0, err
		}
		return reqID, nil
	})
	if err != nil {
		return nil, err
	}

	req, err := s.GetRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}

	// Notifications: broadcast that the request opened, and (for sequential mode)
	// page the active step's approver pool. For parallel mode, page every step
	// instance's pool.
	if s.eventCoordinator != nil {
		fullItem, _ := repository.NewItemRepository(s.db).FindByIDWithDetails(itemID)
		if fullItem != nil {
			s.eventCoordinator.EmitApprovalRequested(req, fullItem, triggeredByUserID)
			for i := range req.StepInstances {
				si := &req.StepInstances[i]
				if si.Status != models.ApprovalStepStatusPending || si.StartedAt == nil {
					continue
				}
				userIDs := approverUserIDs(si.Approvers)
				customerIDs := approverPortalCustomerIDs(si.Approvers)
				s.eventCoordinator.EmitApprovalStepStarted(req, si, userIDs, customerIDs, fullItem, triggeredByUserID)
			}
		}
	}
	return req, nil
}

// approverUserIDs extracts the internal user IDs from the active approvers of a
// step. Portal-customer approvers are skipped here — call approverPortalCustomerIDs
// for those.
func approverUserIDs(approvers []models.ApprovalStepApprover) []int {
	out := make([]int, 0, len(approvers))
	for _, a := range approvers {
		if a.IsActive && a.UserID != nil {
			out = append(out, *a.UserID)
		}
	}
	return out
}

// approverPortalCustomerIDs returns the active portal-customer ids of a step.
func approverPortalCustomerIDs(approvers []models.ApprovalStepApprover) []int {
	out := make([]int, 0, len(approvers))
	for _, a := range approvers {
		if a.IsActive && a.PortalCustomerID != nil {
			out = append(out, *a.PortalCustomerID)
		}
	}
	return out
}

// ----------------------------------------------------------------------------
// Decide: record an approver's decision and advance the state machine.
// ----------------------------------------------------------------------------

// DecideOptions are optional inputs to Decide.
type DecideOptions struct {
	// ItemRepo can be supplied to avoid re-instantiating it; nil is fine.
	ItemRepo *repository.ItemRepository
	// ChannelID scopes portal decisions to approval items in the resolved route
	// channel. Internal approval routes leave it nil.
	ChannelID *int
}

// Decide records a decision against the active step of an approval request. On
// final outcome (approve or reject), it commits the configured transition via
// WorkflowService.CommitTransition inside the same tx.
//
// decision must be one of: ApprovalDecisionApprove, ApprovalDecisionReject,
// ApprovalDecisionComment. (Delegate / cancel / refresh-approvers are separate
// methods so each can carry its own validation.) The comment is optional for
// approve/reject and required for a comment decision.
func (s *ApprovalService) Decide(ctx context.Context, requestID, actorUserID int, decision, comment string, opts DecideOptions) (*models.ApprovalDecision, *models.ApprovalRequest, error) {
	return s.decideAs(ctx, requestID, actorFromUser(actorUserID), decision, comment, opts)
}

// DecideAsCustomer is the portal-side entry point: a portal customer decides
// on an approval where they're in the active pool. Same state machine as the
// internal Decide, but actor attribution flows through actor_portal_customer_id
// and the downstream CommitTransition uses the request's triggered_by_user_id
// for item_history attribution (since item_history requires a user actor).
func (s *ApprovalService) DecideAsCustomer(ctx context.Context, requestID, actorPortalCustomerID int, decision, comment string, opts DecideOptions) (*models.ApprovalDecision, *models.ApprovalRequest, error) {
	return s.decideAs(ctx, requestID, actorFromCustomer(actorPortalCustomerID), decision, comment, opts)
}

func (s *ApprovalService) decideAs(ctx context.Context, requestID int, actor approvalActor, decision, comment string, opts DecideOptions) (*models.ApprovalDecision, *models.ApprovalRequest, error) {
	switch decision {
	case models.ApprovalDecisionApprove, models.ApprovalDecisionReject:
		// ok — the comment is optional reasoning alongside the outcome.
	case models.ApprovalDecisionComment:
		// A comment decision carries no outcome, so an empty body would record
		// a blank entry on the timeline and notify the pool for nothing.
		if strings.TrimSpace(comment) == "" {
			return nil, nil, fmt.Errorf("a comment decision requires a comment")
		}
	default:
		return nil, nil, fmt.Errorf("invalid decision %q", decision)
	}

	itemRepo := opts.ItemRepo
	if itemRepo == nil {
		itemRepo = repository.NewItemRepository(s.db)
	}

	type decideOutcome struct {
		decision                   *models.ApprovalDecision
		priorRequestStatus         string
		newlyStartedStepInstanceID *int
		effectiveActorUserID       int
	}

	out, err := database.WithTxResult(s.db, func(tx database.Tx) (decideOutcome, error) {
		var zero decideOutcome
		var req *models.ApprovalRequest
		var err error
		if opts.ChannelID != nil {
			req, err = s.runtimeRepo.LoadRequestByIDInChannelInTx(ctx, tx, requestID, *opts.ChannelID)
		} else {
			req, err = s.runtimeRepo.LoadRequestByIDInTx(ctx, tx, requestID)
		}
		if err != nil {
			return zero, fmt.Errorf("load request: %w", err)
		}
		if req.Status != models.ApprovalRequestStatusPending {
			return zero, fmt.Errorf("approval request %d is not pending (status=%s)", requestID, req.Status)
		}

		stepInstance, err := s.findActiveStepForActor(ctx, tx, requestID, actor)
		if err != nil {
			return zero, err
		}
		if stepInstance == nil {
			return zero, fmt.Errorf("actor is not an active approver of request %d", requestID)
		}

		step, err := s.templateRepo.FindStepByIDInTx(ctx, tx, stepInstance.ApprovalStepID)
		if err != nil {
			return zero, err
		}

		// Self-approval guard only applies to internal users — triggered_by_user_id
		// is users-only, so a customer-actor can never collide with it.
		if actor.UserID != nil && !step.AllowSelfApproval && *actor.UserID == req.TriggeredByUserID && decision != models.ApprovalDecisionComment {
			return zero, fmt.Errorf("self-approval is not allowed for this step")
		}

		priorRequestStatus := req.Status

		var effectiveActorUserID int
		if actor.UserID != nil {
			effectiveActorUserID = *actor.UserID
		} else {
			effectiveActorUserID = req.TriggeredByUserID
		}

		if decision == models.ApprovalDecisionComment {
			commentDecision, err := s.runtimeRepo.WriteDecision(ctx, tx, requestID, &stepInstance.ID, actor.UserID, actor.PortalCustomerID, decision, comment, nil, nil)
			if err != nil {
				return zero, err
			}
			return decideOutcome{
				decision:             commentDecision,
				priorRequestStatus:   priorRequestStatus,
				effectiveActorUserID: effectiveActorUserID,
			}, nil
		}

		decisionRow, err := s.runtimeRepo.WriteDecision(ctx, tx, requestID, &stepInstance.ID, actor.UserID, actor.PortalCustomerID, decision, comment, nil, nil)
		if err != nil {
			return zero, err
		}

		stepNewStatus, err := s.evaluateStepStatus(ctx, tx, stepInstance.ID, step)
		if err != nil {
			return zero, err
		}
		if stepNewStatus != stepInstance.Status {
			if err := s.runtimeRepo.UpdateStepInstanceStatusComplete(ctx, tx, stepInstance.ID, stepNewStatus); err != nil {
				return zero, err
			}
		}

		var newlyStartedStepInstanceID *int
		if stepNewStatus == models.ApprovalStepStatusApproved || stepNewStatus == models.ApprovalStepStatusRejected {
			nextID, err := s.advanceRequestAfterStep(ctx, tx, req, stepInstance, stepNewStatus, effectiveActorUserID, itemRepo)
			if err != nil {
				return zero, err
			}
			newlyStartedStepInstanceID = nextID
		}

		return decideOutcome{
			decision:                   decisionRow,
			priorRequestStatus:         priorRequestStatus,
			newlyStartedStepInstanceID: newlyStartedStepInstanceID,
			effectiveActorUserID:       effectiveActorUserID,
		}, nil
	})
	if err != nil {
		return nil, nil, err
	}

	full, err := s.GetRequest(ctx, requestID)
	if err != nil {
		return nil, nil, err
	}
	// Live-update publish (WI-483): if this decision finalized the request, the
	// driven status transition committed inside the decision tx (finalizeRequest
	// → CommitTransition). Publish independently of the notification coordinator.
	if full != nil && full.Status != out.priorRequestStatus &&
		(full.Status == models.ApprovalRequestStatusApproved || full.Status == models.ApprovalRequestStatusRejected) {
		PublishItemChange(full.ItemID)
	}
	s.emitDecisionEvents(out.decision, full, out.priorRequestStatus, out.newlyStartedStepInstanceID, out.effectiveActorUserID)
	return out.decision, full, nil
}

// emitDecisionEvents fires post-commit notifications for a Decide call:
// always EmitApprovalDecided; if a new step just started (sequential mode
// advance only — parallel mode opens all steps at request time), emit
// EmitApprovalStepStarted for that step; if the request finalized, emit
// EmitApprovalCompleted.
func (s *ApprovalService) emitDecisionEvents(decision *models.ApprovalDecision, req *models.ApprovalRequest, priorRequestStatus string, newlyStartedStepInstanceID *int, actorUserID int) {
	if s.eventCoordinator == nil || req == nil {
		return
	}
	item, err := repository.NewItemRepository(s.db).FindByIDWithDetails(req.ItemID)
	if err != nil || item == nil {
		return
	}
	s.eventCoordinator.EmitApprovalDecided(req, decision, item)

	if newlyStartedStepInstanceID != nil {
		for i := range req.StepInstances {
			si := &req.StepInstances[i]
			if si.ID != *newlyStartedStepInstanceID {
				continue
			}
			s.eventCoordinator.EmitApprovalStepStarted(req, si, approverUserIDs(si.Approvers), approverPortalCustomerIDs(si.Approvers), item, actorUserID)
			break
		}
	}

	if req.Status != priorRequestStatus &&
		(req.Status == models.ApprovalRequestStatusApproved || req.Status == models.ApprovalRequestStatusRejected) {
		s.eventCoordinator.EmitApprovalCompleted(req, item, actorUserID)
	}
}

// advanceRequestAfterStep starts sequential successors or finalizes requests.
// Parallel rejection skips peers; parallel approval waits for every step.
func (s *ApprovalService) advanceRequestAfterStep(ctx context.Context, tx database.Tx, req *models.ApprovalRequest, stepInstance *models.ApprovalStepInstance, stepStatus string, actorUserID int, itemRepo *repository.ItemRepository) (*int, error) {
	ass, err := s.templateRepo.FindStatusByIDInTx(ctx, tx, req.ApprovalSetStatusID)
	if err != nil {
		return nil, err
	}

	if ass.StepMode == models.ApprovalStepModeParallel {
		return nil, s.evaluateParallelRequestState(ctx, tx, req, ass, stepInstance, stepStatus, actorUserID, itemRepo)
	}

	if stepStatus == models.ApprovalStepStatusRejected {
		return nil, s.finalizeRequest(ctx, tx, req, ass, models.ApprovalRequestStatusRejected, ass.DenyTransitionID, actorUserID, itemRepo)
	}

	nextStepInstanceID, nextStepID, found, err := s.runtimeRepo.FindNextPendingStep(ctx, tx, req.ID, stepInstance.DisplayOrder)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, s.finalizeRequest(ctx, tx, req, ass, models.ApprovalRequestStatusApproved, ass.ApproveTransitionID, actorUserID, itemRepo)
	}

	now := time.Now()
	nextStep, err := s.templateRepo.FindStepByIDInTx(ctx, tx, nextStepID)
	if err != nil {
		return nil, err
	}
	var dueAt sql.NullTime
	if nextStep.EscalationAfterHours != nil {
		dueAt = sql.NullTime{Time: now.Add(time.Duration(*nextStep.EscalationAfterHours) * time.Hour), Valid: true}
	}
	if err := s.runtimeRepo.StartStepInstance(ctx, tx, nextStepInstanceID, now, dueAt); err != nil {
		return nil, err
	}

	item, err := itemRepo.FindByID(req.ItemID)
	if err != nil {
		return nil, fmt.Errorf("reload item: %w", err)
	}
	if err := s.resolveAndSnapshotApprovers(ctx, tx, nextStepInstanceID, *nextStep, item, req.TriggeredByUserID); err != nil {
		return nil, fmt.Errorf("snapshot next-step approvers: %w", err)
	}
	return &nextStepInstanceID, nil
}

// finalizeRequest commits the configured approve/deny transition and marks
// the request as approved/rejected.
func (s *ApprovalService) finalizeRequest(ctx context.Context, tx database.Tx, req *models.ApprovalRequest, ass *models.ApprovalSetStatus, finalStatus string, transitionID, actorUserID int, itemRepo *repository.ItemRepository) error {
	_, toStatusID, err := s.runtimeRepo.GetTransitionEndpoints(ctx, tx, transitionID)
	if err != nil {
		return fmt.Errorf("load transition: %w", err)
	}

	if err := s.runtimeRepo.UpdateRequestStatusComplete(ctx, tx, req.ID, finalStatus); err != nil {
		return fmt.Errorf("finalize request: %w", err)
	}

	// History requires a user, so system decisions use the requestor; audit
	// decisions retain nil actor provenance.
	historyActor := actorUserID
	if historyActor == 0 {
		historyActor = req.TriggeredByUserID
	}
	metadata := itemEventMetadata(historyActor, "approval", nil)
	if err := s.workflowService.CommitTransition(ctx, tx, itemRepo, req.ItemID, ass.StatusID, toStatusID, historyActor, metadata); err != nil {
		return fmt.Errorf("commit driven transition: %w", err)
	}

	if _, err := s.runtimeRepo.WriteDecision(ctx, tx, req.ID, nil, nil, nil, models.ApprovalDecisionCompleted, "", nil, map[string]any{
		"final_status":  finalStatus,
		"transition_id": transitionID,
		"to_status_id":  toStatusID,
	}); err != nil {
		return err
	}
	return nil
}

// evaluateParallelRequestState rejects and skips peers on any rejection; it
// approves only when every step has terminated successfully.
func (s *ApprovalService) evaluateParallelRequestState(ctx context.Context, tx database.Tx, req *models.ApprovalRequest, ass *models.ApprovalSetStatus, stepInstance *models.ApprovalStepInstance, stepStatus string, actorUserID int, itemRepo *repository.ItemRepository) error {
	if stepStatus == models.ApprovalStepStatusRejected {
		if err := s.runtimeRepo.SkipPendingPeerSteps(ctx, tx, req.ID, stepInstance.ID); err != nil {
			return err
		}
		return s.finalizeRequest(ctx, tx, req, ass, models.ApprovalRequestStatusRejected, ass.DenyTransitionID, actorUserID, itemRepo)
	}

	pending, total, err := s.runtimeRepo.CountStepStates(ctx, tx, req.ID)
	if err != nil {
		return err
	}
	if total == 0 {
		return errors.New("parallel approval has no step instances")
	}
	if pending == 0 {
		return s.finalizeRequest(ctx, tx, req, ass, models.ApprovalRequestStatusApproved, ass.ApproveTransitionID, actorUserID, itemRepo)
	}
	return nil
}

// ----------------------------------------------------------------------------
// Cancel: caller-initiated termination of a pending request.
// ----------------------------------------------------------------------------

// Cancel marks a pending request canceled. reason is a short string surfaced
// in the audit log: "left_status", "manual", "superseded", etc.
//
// Cancel also reverts the item to the status it was in before the inbound
// approval-triggering transition (snapshotted in approval_requests.from_status_id),
// so the item is never left stuck in the gated status with no active gate.
// The revert is skipped — and the reason recorded in audit metadata — when:
//   - from_status_id is NULL (request pre-dates the column, or the prior
//     status was deleted; the FK is ON DELETE SET NULL), or
//   - the item has since drifted to a different status (already left the gated one).
//
// The revert calls WorkflowService.CommitTransition directly (not PerformTransition),
// so it bypasses gating logic — going backwards via a system action must not
// re-trigger an approval gate on the prior status.
func (s *ApprovalService) Cancel(ctx context.Context, requestID, actorUserID int, comment, reason string) error {
	if s == nil {
		return nil
	}
	type cancelOutcome struct {
		ran        bool
		itemID     int
		toStatusID int
		revertTo   int
	}

	outcome, err := database.WithTxResult(s.db, func(tx database.Tx) (cancelOutcome, error) {
		var out cancelOutcome
		req, err := s.runtimeRepo.LoadRequestByIDInTx(ctx, tx, requestID)
		if err != nil {
			return out, err
		}
		if req.Status != models.ApprovalRequestStatusPending {
			return out, nil // already finalized; nothing to do
		}
		out.itemID = req.ItemID
		out.toStatusID = req.StatusID

		if err := s.runtimeRepo.UpdateRequestStatusComplete(ctx, tx, requestID, models.ApprovalRequestStatusCancelled); err != nil {
			return out, err
		}

		auditMeta := map[string]any{"reason": reason}
		if req.FromStatusID == nil {
			auditMeta["skipped_revert_reason"] = "pre_migration"
		} else {
			currentStatusID, err := s.runtimeRepo.GetItemCurrentStatusID(ctx, tx, req.ItemID)
			if err != nil {
				return out, fmt.Errorf("load item status: %w", err)
			}
			if currentStatusID != req.StatusID {
				auditMeta["skipped_revert_reason"] = "status_drift"
			} else {
				out.revertTo = *req.FromStatusID
			}
		}

		if out.revertTo != 0 {
			itemRepo := repository.NewItemRepository(s.db)
			metadata := itemEventMetadata(actorUserID, "approval", nil)
			if err := s.workflowService.CommitTransition(ctx, tx, itemRepo, req.ItemID, req.StatusID, out.revertTo, actorUserID, metadata); err != nil {
				return out, fmt.Errorf("revert item status: %w", err)
			}
			auditMeta["reverted_to_status_id"] = out.revertTo
		}

		actor := &actorUserID
		if actorUserID == 0 {
			actor = nil
		}
		if _, err := s.runtimeRepo.WriteDecision(ctx, tx, requestID, nil, actor, nil, models.ApprovalDecisionCancel, comment, nil, auditMeta); err != nil {
			return out, err
		}
		out.ran = true
		return out, nil
	})
	if err != nil {
		return err
	}
	if !outcome.ran {
		return nil
	}

	// Live-update publish (WI-483): the cancel committed; if it reverted the
	// item's status, announce the change independently of the coordinator.
	if outcome.revertTo != 0 {
		PublishItemChange(outcome.itemID)
	}

	if s.eventCoordinator != nil {
		req, err := s.GetRequest(ctx, requestID)
		if err == nil && req != nil {
			item, _ := repository.NewItemRepository(s.db).FindByIDWithDetails(req.ItemID)
			if item != nil {
				s.eventCoordinator.EmitApprovalCancelled(req, item, reason, actorUserID)
				if outcome.revertTo != 0 {
					oldStatus := outcome.toStatusID
					newStatus := outcome.revertTo
					s.eventCoordinator.EmitStatusChanged(item, &oldStatus, &newStatus, actorUserID)
				}
			}
		}
	}
	return nil
}

// Escalate handles timeout or admin escalation. Reassignment updates the
// approver pool and re-arms its deadline; skip and reject advance or reject the
// request. No longer-pending steps are no-ops.
