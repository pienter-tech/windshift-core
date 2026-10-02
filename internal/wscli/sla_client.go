package wscli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"windshift/internal/models"
)

// GetItemSLA returns the SLA state for one work item.
func (c *Client) GetItemSLA(itemID int) ([]models.ItemSLA, error) {
	var states []models.ItemSLA
	if err := c.GET(fmt.Sprintf("/rest/api/v2/items/%d/sla", itemID), &states); err != nil {
		return nil, err
	}
	return states, nil
}

// GetSLAReport returns the completed-cycle compliance report for a workspace.
// from and to are optional RFC3339 or YYYY-MM-DD bounds.
func (c *Client) GetSLAReport(workspaceID int, from, to string) (*models.SLAReport, error) {
	path := fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/report", workspaceID)
	query := url.Values{}
	if from != "" {
		query.Set("from", from)
	}
	if to != "" {
		query.Set("to", to)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var report models.SLAReport
	if err := c.GET(path, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

// ListSLAMetrics returns a workspace's SLA metrics.
func (c *Client) ListSLAMetrics(workspaceID int) ([]models.SLAMetric, error) {
	var metrics []models.SLAMetric
	if err := c.GET(fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), &metrics); err != nil {
		return nil, err
	}
	return metrics, nil
}

// GetSLAMetric returns one SLA metric.
func (c *Client) GetSLAMetric(workspaceID, metricID int) (*models.SLAMetric, error) {
	var metric models.SLAMetric
	if err := c.GET(fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics/%d", workspaceID, metricID), &metric); err != nil {
		return nil, err
	}
	return &metric, nil
}

// CreateSLAMetric posts a metric payload and returns the persisted metric.
func (c *Client) CreateSLAMetric(workspaceID int, payload json.RawMessage) (*models.SLAMetric, error) {
	var metric models.SLAMetric
	if err := c.POST(fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics", workspaceID), payload, &metric); err != nil {
		return nil, err
	}
	return &metric, nil
}

// UpdateSLAMetric replaces a metric payload and returns the persisted metric.
func (c *Client) UpdateSLAMetric(workspaceID, metricID int, payload json.RawMessage) (*models.SLAMetric, error) {
	var metric models.SLAMetric
	if err := c.PUT(fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics/%d", workspaceID, metricID), payload, &metric); err != nil {
		return nil, err
	}
	return &metric, nil
}

// DeleteSLAMetric removes a metric.
func (c *Client) DeleteSLAMetric(workspaceID, metricID int) error {
	return c.DELETE(fmt.Sprintf("/rest/api/v2/workspaces/%d/sla/metrics/%d", workspaceID, metricID))
}

// ListSLAItems runs an SLA QL filter over the item list and returns the page.
func (c *Client) ListSLAItems(ql string, workspaceID *int, sort string, limit int) (*PaginatedResponse[Item], error) {
	params := url.Values{}
	params.Set("ql", ql)
	if sort != "" {
		params.Set("sort", sort)
	}
	if limit > 0 {
		params.Set("page_size", strconv.Itoa(limit))
	}
	if workspaceID != nil {
		params.Set("workspace_id", strconv.Itoa(*workspaceID))
	}
	var resp PaginatedResponse[Item]
	if err := c.GET("/rest/api/v2/items?"+params.Encode(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
