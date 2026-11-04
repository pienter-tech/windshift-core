package services

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// PlanningValidationError is returned by the shared milestone/iteration
// service boundary. Every adapter can map it to its native invalid-input
// response without parsing database errors or duplicating domain rules.
type PlanningValidationError struct {
	Field   string
	Message string
	Cause   error
}

func (e *PlanningValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func (e *PlanningValidationError) Unwrap() error {
	return e.Cause
}

func AsPlanningValidationError(err error) (*PlanningValidationError, bool) {
	var validationErr *PlanningValidationError
	ok := errors.As(err, &validationErr)
	return validationErr, ok
}

func planningValidationError(field, message string) error {
	return &PlanningValidationError{Field: field, Message: message}
}

func planningValidationErrorWithCause(field, message string, cause error) error {
	return &PlanningValidationError{Field: field, Message: message, Cause: cause}
}

const cancelledPlanningStatus = "cancelled" //nolint:misspell // Persisted API status.

// normalizePlanningStatus maps the US spelling "canceled" onto the canonical
// persisted planning status. Clients send either spelling; validation,
// filters, and storage all use the canonical one. Any other value passes
// through so validation can reject it.
func normalizePlanningStatus(status string) string {
	if strings.EqualFold(strings.TrimSpace(status), "canceled") {
		return cancelledPlanningStatus
	}
	return status
}

func validMilestoneStatus(status string) bool {
	switch status {
	case "planning", "in-progress", "completed", cancelledPlanningStatus:
		return true
	default:
		return false
	}
}

func validIterationStatus(status string) bool {
	switch status {
	case "planned", "active", "completed", cancelledPlanningStatus:
		return true
	default:
		return false
	}
}

const milestoneStatusValidationMessage = "must be planning, in-progress, completed, or cancelled" //nolint:misspell // API contract.
const iterationStatusValidationMessage = "must be planned, active, completed, or cancelled"       //nolint:misspell // API contract.

func parsePlanningDate(field, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, planningValidationError(field, "is required")
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, planningValidationError(field, "must use YYYY-MM-DD")
	}
	return parsed, nil
}

func (s *PlanningService) validateMilestoneMutation(params CreateMilestoneParams) error {
	if strings.TrimSpace(params.Name) == "" {
		return planningValidationError("name", "is required")
	}
	if !validMilestoneStatus(params.Status) {
		return planningValidationError("status", milestoneStatusValidationMessage)
	}
	if params.TargetDate != nil {
		if _, err := parsePlanningDate("target_date", *params.TargetDate); err != nil {
			return err
		}
	}
	if err := validatePlanningScope(params.IsGlobal, params.WorkspaceID); err != nil {
		return err
	}
	if params.WorkspaceID != nil {
		exists, err := s.WorkspaceExists(*params.WorkspaceID)
		if err != nil {
			return err
		}
		if !exists {
			return planningValidationError("workspace_id", "does not reference an existing workspace")
		}
	}
	if params.CategoryID != nil {
		exists, err := s.CategoryExists(*params.CategoryID)
		if err != nil {
			return err
		}
		if !exists {
			return planningValidationError("category_id", "does not reference an existing milestone category")
		}
	}
	return nil
}

func (s *PlanningService) validateIterationMutation(params CreateIterationParams) error {
	if strings.TrimSpace(params.Name) == "" {
		return planningValidationError("name", "is required")
	}
	start, err := parsePlanningDate("start_date", params.StartDate)
	if err != nil {
		return err
	}
	end, err := parsePlanningDate("end_date", params.EndDate)
	if err != nil {
		return err
	}
	if end.Before(start) {
		return planningValidationError("end_date", "must be on or after start_date")
	}
	if !validIterationStatus(params.Status) {
		return planningValidationError("status", iterationStatusValidationMessage)
	}
	if err := validatePlanningScope(params.IsGlobal, params.WorkspaceID); err != nil {
		return err
	}
	if params.WorkspaceID != nil {
		exists, err := s.WorkspaceExists(*params.WorkspaceID)
		if err != nil {
			return err
		}
		if !exists {
			return planningValidationError("workspace_id", "does not reference an existing workspace")
		}
	}
	if params.TypeID != nil {
		exists, err := s.IterationTypeExists(*params.TypeID)
		if err != nil {
			return err
		}
		if !exists {
			return planningValidationError("type_id", "does not reference an existing iteration type")
		}
	}
	return nil
}

// CategoryExists reports whether a milestone category exists by ID.
func (s *PlanningService) CategoryExists(categoryID int) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM milestone_categories WHERE id = ?", categoryID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check category: %w", err)
	}
	return count > 0, nil
}

// WorkspaceExists reports whether a workspace exists by ID.
func (s *PlanningService) WorkspaceExists(workspaceID int) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM workspaces WHERE id = ?", workspaceID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check workspace: %w", err)
	}
	return count > 0, nil
}

// IterationTypeExists reports whether an iteration type exists by ID.
func (s *PlanningService) IterationTypeExists(typeID int) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM iteration_types WHERE id = ?", typeID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check iteration type: %w", err)
	}
	return count > 0, nil
}
