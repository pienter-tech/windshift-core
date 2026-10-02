package services

import (
	"context"
	"errors"
	"time"

	"windshift/internal/database"
	"windshift/internal/itemevents"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/sanitize"
)

const defaultLabelColor = "#3B82F6"

var (
	ErrLabelNameRequired = errors.New("label name is required")
	ErrLabelIDRequired   = errors.New("label ID is required")
)

// LabelUpdate describes the optional fields accepted by label updates.
type LabelUpdate struct {
	Name  *string
	Color *string
}

// LabelApplicationService owns global label and item-assignment behavior.
type LabelApplicationService struct {
	db     database.Database
	labels *repository.LabelRepository
}

// NewLabelApplicationService creates the shared label application boundary.
func NewLabelApplicationService(db database.Database) *LabelApplicationService {
	return &LabelApplicationService{
		db:     db,
		labels: repository.NewLabelRepository(db),
	}
}

// List returns the global label catalog.
func (s *LabelApplicationService) List() ([]models.Label, error) {
	return s.labels.ListAll()
}

// Get returns one global label.
func (s *LabelApplicationService) Get(id int) (*models.Label, error) {
	return s.labels.GetByID(id)
}

// Create validates and creates a global label.
func (s *LabelApplicationService) Create(actor AuditActor, name, color string) (*models.Label, error) {
	name = sanitize.ShortIdentifier.Sanitize(name)
	if name == "" {
		return nil, ErrLabelNameRequired
	}
	if color == "" {
		color = defaultLabelColor
	}

	id, _, err := s.labels.Create(name, color)
	if err != nil {
		return nil, err
	}
	label, err := s.labels.GetByID(int(id))
	if err != nil {
		return nil, err
	}
	s.emitAudit(actor, logger.ActionLabelCreate, label)
	return label, nil
}

// Update validates and applies a partial global-label update.
func (s *LabelApplicationService) Update(actor AuditActor, id int, update LabelUpdate) (*models.Label, error) {
	existing, err := s.labels.GetByID(id)
	if err != nil {
		return nil, err
	}

	name := existing.Name
	if update.Name != nil {
		name = sanitize.ShortIdentifier.Sanitize(*update.Name)
		if name == "" {
			return nil, ErrLabelNameRequired
		}
	}
	color := existing.Color
	if update.Color != nil {
		color = *update.Color
		if color == "" {
			color = defaultLabelColor
		}
	}

	if err := s.labels.Update(id, name, color); err != nil {
		return nil, err
	}

	updated, err := s.labels.GetByID(id)
	if err != nil {
		return nil, err
	}
	s.emitAudit(actor, logger.ActionLabelUpdate, updated)
	return updated, nil
}

// Delete removes a global label and its item assignments.
func (s *LabelApplicationService) Delete(actor AuditActor, id int) error {
	existing, err := s.labels.GetByID(id)
	if err != nil {
		return err
	}
	if err := s.labels.Delete(id); err != nil {
		return err
	}
	s.emitAudit(actor, logger.ActionLabelDelete, existing)
	return nil
}

// ListForItem returns the labels assigned to an item.
func (s *LabelApplicationService) ListForItem(itemID int) ([]models.Label, error) {
	return s.labels.ListForItem(itemID)
}

// SetForItem validates and replaces an item's complete label set. The swap
// and its change fact commit together so SLA evaluation and item history see
// label changes exactly once.
func (s *LabelApplicationService) SetForItem(actor AuditActor, itemID int, labelIDs []int) ([]models.Label, error) {
	if err := s.requireLabels(labelIDs); err != nil {
		return nil, err
	}
	item, err := repository.NewItemRepository(s.db).FindByIDWithDetails(itemID)
	if err != nil {
		return nil, err
	}
	var result []models.Label
	err = database.WithTx(s.db, func(tx database.Tx) error {
		before, err := s.labels.ListForItemTx(tx, itemID)
		if err != nil {
			return err
		}
		if err := s.labels.ReplaceItemLabelsTx(context.Background(), tx, itemID, labelIDs); err != nil {
			return err
		}
		after, err := s.labels.ListForItemTx(tx, itemID)
		if err != nil {
			return err
		}
		if labelSetsDiffer(before, after) {
			if err := recordLabelChangeFact(s.db, tx, item, before, after, actor); err != nil {
				return err
			}
		}
		result = after
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// AddToItem validates and assigns one label to an item.
func (s *LabelApplicationService) AddToItem(actor AuditActor, itemID, labelID int) ([]models.Label, error) {
	if labelID == 0 {
		return nil, ErrLabelIDRequired
	}
	if err := s.requireLabels([]int{labelID}); err != nil {
		return nil, err
	}
	item, err := repository.NewItemRepository(s.db).FindByIDWithDetails(itemID)
	if err != nil {
		return nil, err
	}
	var result []models.Label
	err = database.WithTx(s.db, func(tx database.Tx) error {
		before, err := s.labels.ListForItemTx(tx, itemID)
		if err != nil {
			return err
		}
		changed, err := s.labels.AddItemLabelTx(tx, itemID, labelID)
		if err != nil {
			return err
		}
		after, err := s.labels.ListForItemTx(tx, itemID)
		if err != nil {
			return err
		}
		if changed {
			if err := recordLabelChangeFact(s.db, tx, item, before, after, actor); err != nil {
				return err
			}
		}
		result = after
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RemoveFromItem detaches one label from an item.
func (s *LabelApplicationService) RemoveFromItem(actor AuditActor, itemID, labelID int) error {
	item, err := repository.NewItemRepository(s.db).FindByIDWithDetails(itemID)
	if err != nil {
		return err
	}
	return database.WithTx(s.db, func(tx database.Tx) error {
		before, err := s.labels.ListForItemTx(tx, itemID)
		if err != nil {
			return err
		}
		changed, err := s.labels.RemoveItemLabelTx(tx, itemID, labelID)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		after, err := s.labels.ListForItemTx(tx, itemID)
		if err != nil {
			return err
		}
		return recordLabelChangeFact(s.db, tx, item, before, after, actor)
	})
}

// labelSetsDiffer compares two label lists by membership.
func labelSetsDiffer(before, after []models.Label) bool {
	if len(before) != len(after) {
		return true
	}
	ids := make(map[int]bool, len(before))
	for _, label := range before {
		ids[label.ID] = true
	}
	for _, label := range after {
		if !ids[label.ID] {
			return true
		}
	}
	return false
}

// labelNames projects labels to their ordered names for change facts.
func labelNames(labels []models.Label) []string {
	names := make([]string, len(labels))
	for i, label := range labels {
		names[i] = label.Name
	}
	return names
}

// recordLabelChangeFact appends the item-updated fact for a label change so
// SLA evaluation, automation, and item history observe it.
func recordLabelChangeFact(db database.Database, tx database.Tx, item *models.Item, before, after []models.Label, actor AuditActor) error {
	metadata := itemEventMetadata(actor.UserID, "application", nil)
	if metadata.OccurredAt.IsZero() {
		metadata.OccurredAt = time.Now()
	}
	_, err := itemevents.NewRecorder(db).Updated(context.Background(), tx, item, []itemevents.FieldChange{
		{Field: "labels", OldValue: labelNames(before), NewValue: labelNames(after)},
	}, metadata)
	return err
}

func (s *LabelApplicationService) requireLabels(labelIDs []int) error {
	for _, labelID := range labelIDs {
		if _, err := s.labels.GetByID(labelID); err != nil {
			return err
		}
	}
	return nil
}

func (s *LabelApplicationService) emitAudit(actor AuditActor, action string, label *models.Label) {
	if actor.UserID == 0 {
		return
	}
	labelID := label.ID
	emitServiceAudit(s.db, actor, action, logger.ResourceLabel, &labelID, label.Name, nil)
}
