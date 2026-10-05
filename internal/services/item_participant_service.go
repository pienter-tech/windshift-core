package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"

	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/repository"
)

var (
	// ErrParticipantIdentityRequired reports a missing participant identity.
	ErrParticipantIdentityRequired = errors.New("a participant email or portal customer id is required")
	// ErrParticipantEmailInvalid reports a malformed participant email.
	ErrParticipantEmailInvalid = errors.New("participant email is not valid")
)

// ParticipantInput identifies the external person to attach to a ticket.
// PortalCustomerID takes precedence over Email when both are set.
type ParticipantInput struct {
	Email            string
	Name             string
	PortalCustomerID *int
}

// ParticipantNotifier delivers a customer-facing notice when an external
// participant is added. Optional; a nil notifier disables the notice.
type ParticipantNotifier interface {
	SendParticipantAddedNotice(itemID, customerID, actorID int) (bool, string, error)
}

// ItemParticipantService owns external request participants (WI-1136):
// resolving the person, attaching or detaching them, and recording item
// history. Participants are always portal customers; internal users are not
// participants and never reach this service.
type ItemParticipantService struct {
	db           database.Database
	participants *repository.ItemParticipantRepository
	customers    *repository.PortalCustomerRepository
	items        *repository.ItemRepository
	notifier     ParticipantNotifier
}

// SetNotifier wires the customer-facing notifier used when a participant is
// added. Call before serving requests.
func (s *ItemParticipantService) SetNotifier(notifier ParticipantNotifier) {
	s.notifier = notifier
}

// NewItemParticipantService creates the participant application boundary.
func NewItemParticipantService(db database.Database) *ItemParticipantService {
	return &ItemParticipantService{
		db:           db,
		participants: repository.NewItemParticipantRepository(db),
		customers:    repository.NewPortalCustomerRepository(db),
		items:        repository.NewItemRepository(db),
	}
}

// List returns the external participants of an item, oldest first.
func (s *ItemParticipantService) List(itemID int) ([]models.ItemParticipant, error) {
	return s.participants.ListByItem(itemID)
}

// Add resolves the external person, attaches them to the item, and records an
// item-history entry only when a new participant is actually added. It returns
// the full participant list and whether a new row was created.
func (s *ItemParticipantService) Add(ctx context.Context, actorID, itemID int, input ParticipantInput) ([]models.ItemParticipant, bool, error) {
	customerID, email, err := s.resolveCustomer(ctx, input)
	if err != nil {
		return nil, false, err
	}
	created, err := s.participants.Add(itemID, customerID, &actorID)
	if err != nil {
		return nil, false, err
	}
	if created {
		if err := s.items.RecordHistory(s.db, repository.HistoryEntry{
			ItemID:    itemID,
			UserID:    actorID,
			FieldName: "participant_added",
			NewValue:  email,
		}); err != nil {
			return nil, false, err
		}
		// Best-effort customer-facing notice; a delivery failure must not undo
		// the participant add.
		if s.notifier != nil {
			if _, _, err := s.notifier.SendParticipantAddedNotice(itemID, customerID, actorID); err != nil {
				slog.Warn("failed to notify added participant",
					"component", "item_participant_service",
					"item_id", itemID,
					"customer_id", customerID,
					"error", err)
			}
		}
	}
	list, err := s.participants.ListByItem(itemID)
	if err != nil {
		return nil, false, err
	}
	return list, created, nil
}

// Remove detaches a customer and records item history when a row was removed.
// It returns the remaining participant list.
func (s *ItemParticipantService) Remove(ctx context.Context, actorID, itemID, customerID int) ([]models.ItemParticipant, error) {
	_, email, err := s.customers.GetIdentity(ctx, customerID)
	if err != nil {
		return nil, err
	}
	removed, err := s.participants.Remove(itemID, customerID)
	if err != nil {
		return nil, err
	}
	if removed {
		if err := s.items.RecordHistory(s.db, repository.HistoryEntry{
			ItemID:    itemID,
			UserID:    actorID,
			FieldName: "participant_removed",
			OldValue:  email,
		}); err != nil {
			return nil, err
		}
	}
	return s.participants.ListByItem(itemID)
}

// resolveCustomer maps the input to a portal customer id. An unknown email
// creates the customer with agent provenance (WI-1553); existing customers are
// reused. It also returns the customer email for history.
func (s *ItemParticipantService) resolveCustomer(ctx context.Context, input ParticipantInput) (customerID int, email string, err error) {
	if input.PortalCustomerID != nil {
		if *input.PortalCustomerID <= 0 {
			return 0, "", ErrParticipantIdentityRequired
		}
		_, email, err = s.customers.GetIdentity(ctx, *input.PortalCustomerID)
		if err != nil {
			return 0, "", err
		}
		return *input.PortalCustomerID, email, nil
	}

	email = strings.ToLower(strings.TrimSpace(input.Email))
	if email == "" {
		return 0, "", ErrParticipantIdentityRequired
	}
	if _, err = mail.ParseAddress(email); err != nil {
		return 0, "", ErrParticipantEmailInvalid
	}
	customerID, _, err = s.customers.FindOrCreateByEmail(ctx, strings.TrimSpace(input.Name), email, models.CustomerCreatedViaAgent)
	if err != nil {
		return 0, "", fmt.Errorf("resolve participant customer: %w", err)
	}
	return customerID, email, nil
}
