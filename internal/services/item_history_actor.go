package services

import (
	"strconv"
	"time"

	"windshift/internal/itemevents"
	"windshift/internal/repository"
)

// historyActorFromEventMetadata maps an itemevents actor onto item-history
// attribution. User and portal-customer actors carry their reference; every
// other kind (system, integration, agent, import) collapses to a system row
// because item_history only stores user and portal-customer references.
func historyActorFromEventMetadata(metadata itemevents.Metadata) (kind string, userID int, portalCustomerID *int) {
	switch metadata.ActorKind {
	case "user":
		id, err := strconv.Atoi(metadata.ActorRef)
		if err == nil && id > 0 {
			return repository.HistoryActorUser, id, nil
		}
	case "portal_customer":
		id, err := strconv.Atoi(metadata.ActorRef)
		if err == nil && id > 0 {
			return repository.HistoryActorPortalCustomer, 0, &id
		}
	}
	return repository.HistoryActorSystem, 0, nil
}

// historyEntryForChange builds one item-history row for a field change,
// attributed to the metadata actor.
func historyEntryForChange(itemID int, fieldName, oldValue, newValue string, changedAt time.Time, metadata itemevents.Metadata) repository.HistoryEntry {
	kind, userID, customerID := historyActorFromEventMetadata(metadata)
	entry := repository.HistoryEntry{
		ItemID:    itemID,
		FieldName: fieldName,
		OldValue:  oldValue,
		NewValue:  newValue,
		ChangedAt: changedAt,
		ActorKind: kind,
		UserID:    userID,
	}
	if customerID != nil {
		entry.ActorPortalCustomerID = customerID
	}
	return entry
}
