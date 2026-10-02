package jiraimport

import (
	"context"
	"encoding/json"
	"errors"

	"windshift/internal/models"
	"windshift/internal/repository"
)

func (s *Service) PortalBySlug(ctx context.Context, slug string) (*models.Channel, error) {
	channel, err := s.channels.FindInboundPortalBySlug(ctx, slug)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, repository.ErrNotFound
	}
	return channel, err
}

func (s *Service) CreatePortal(
	ctx context.Context,
	channel *models.Channel,
	managerUserID int,
) (int, error) {
	return s.channels.CreatePortalWithManager(ctx, channel, managerUserID)
}

func (s *Service) AddPortalRequestTypeSection(
	ctx context.Context,
	channelID int,
	requestTypeIDs []int,
	sectionID string,
) error {
	channel, err := s.channels.FindByID(ctx, channelID)
	if err != nil {
		return err
	}
	var config models.ChannelConfig
	if err := json.Unmarshal([]byte(channel.Config), &config); err != nil {
		return err
	}
	if len(config.PortalSections) == 0 {
		config.PortalSections = []models.PortalSection{{
			ID:             sectionID,
			Title:          "Requests",
			DisplayOrder:   0,
			RequestTypeIDs: requestTypeIDs,
			AssetReportIDs: []int{},
		}}
	} else {
		seen := make(map[int]struct{}, len(config.PortalSections[0].RequestTypeIDs))
		for _, id := range config.PortalSections[0].RequestTypeIDs {
			seen[id] = struct{}{}
		}
		for _, id := range requestTypeIDs {
			if _, ok := seen[id]; !ok {
				config.PortalSections[0].RequestTypeIDs = append(
					config.PortalSections[0].RequestTypeIDs,
					id,
				)
			}
		}
	}
	value, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return s.channels.SetConfig(ctx, channelID, string(value))
}

func (s *Service) EnsureRequestType(
	requestType *models.RequestType,
	config models.RequestTypeConfig,
) (id int, created bool, err error) {
	id, err = s.requestTypes.FindIDByName(requestType.ChannelID, requestType.Name)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return 0, false, err
	}
	id64, err := s.requestTypes.Create(requestType)
	if err != nil {
		return 0, false, err
	}
	id = int(id64)
	rawConfig, err := json.Marshal(config)
	if err != nil {
		return 0, false, err
	}
	if err := s.requestTypes.UpdateConfig(id, string(rawConfig)); err != nil {
		return 0, false, err
	}
	err = s.requestTypes.ReplaceFields(id, []models.RequestTypeField{
		{
			FieldIdentifier: "title",
			FieldType:       "default",
			DisplayOrder:    1,
			IsRequired:      true,
			StepNumber:      1,
		},
		{
			FieldIdentifier: "description",
			FieldType:       "default",
			DisplayOrder:    2,
			StepNumber:      1,
		},
	})
	return id, err == nil, err
}

type PortalCustomer struct {
	ID             int
	OrganisationID *int
	Created        bool
}

func (s *Service) EnsurePortalCustomer(
	name, email string,
	organisationID int,
) (PortalCustomer, error) {
	id, err := s.portalUsers.FindIDByEmail(email)
	created := false
	if errors.Is(err, repository.ErrNotFound) {
		// Import-created customers carry ticket-import provenance (WI-1553)
		// so they are visible to provenance reporting and cleanup filters.
		id, err = s.portalUsers.CreateWithProvenance(name, email, organisationID, models.CustomerCreatedViaTicketImport)
		created = err == nil
	}
	if err != nil {
		return PortalCustomer{}, err
	}
	previous, err := s.portalUsers.OrganisationID(id)
	if err != nil {
		return PortalCustomer{}, err
	}
	if organisationID > 0 && previous == nil {
		if _, err := s.portalUsers.AssignOrganisationIfUnset(id, organisationID); err != nil {
			return PortalCustomer{}, err
		}
	}
	return PortalCustomer{ID: id, OrganisationID: previous, Created: created}, nil
}

func (s *Service) EnsurePortalCustomerChannel(customerID, channelID int) (bool, error) {
	return s.portalUsers.EnsureChannelAccess(customerID, channelID)
}

func (s *Service) EnsurePortalCustomerRole(customerID int) (roleID int, created bool, err error) {
	return s.portalUsers.EnsureRole(customerID, "Portal Customer")
}

func (s *Service) EnsureCustomerOrganisation(
	name, description string,
) (id int, created bool, err error) {
	id, err = s.customerOrgs.FindIDByName(name)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return 0, false, err
	}
	id, _, err = s.customerOrgs.Create(&models.CustomerOrganisation{
		Name:        name,
		Description: description,
		Active:      true,
	})
	return id, err == nil, err
}
