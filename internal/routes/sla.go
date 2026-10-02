package routes

import "net/http"

// RegisterSLARoutes registers SLA configuration and item SLA state routes.
func RegisterSLARoutes(deps *Deps) {
	if deps.SLA == nil {
		return
	}
	api := deps.API
	auth := deps.AuthMiddleware.RequireAuth

	// Item SLA state.
	api.HandleH("GET /items/{id}/sla", auth(http.HandlerFunc(deps.SLA.GetItemSLA)))
	api.HandleH("GET /workspaces/{id}/sla/items", auth(http.HandlerFunc(deps.SLA.GetWorkspaceItemsSLA)))

	// Workspace calendars.
	api.HandleH("GET /workspaces/{id}/sla/calendars", auth(http.HandlerFunc(deps.SLA.ListWorkspaceCalendars)))
	api.HandleH("POST /workspaces/{id}/sla/calendars", auth(http.HandlerFunc(deps.SLA.CreateWorkspaceCalendar)))
	api.HandleH("PUT /workspaces/{id}/sla/calendars/{calendarId}", auth(http.HandlerFunc(deps.SLA.UpdateWorkspaceCalendar)))
	api.HandleH("DELETE /workspaces/{id}/sla/calendars/{calendarId}", auth(http.HandlerFunc(deps.SLA.DeleteWorkspaceCalendar)))
	api.HandleH("GET /workspaces/{id}/sla/calendars/{calendarId}/impact", auth(http.HandlerFunc(deps.SLA.GetWorkspaceCalendarImpact)))
	api.HandleH("GET /workspaces/{id}/sla/coverage-preview", auth(http.HandlerFunc(deps.SLA.GetCoveragePreview)))
	api.HandleH("GET /workspaces/{id}/sla/available-calendars", auth(http.HandlerFunc(deps.SLA.ListAvailableCalendars)))

	// Team service-hours calendars.
	api.HandleH("GET /teams/{id}/working-calendars", auth(http.HandlerFunc(deps.SLA.ListTeamCalendars)))
	api.HandleH("POST /teams/{id}/working-calendars", auth(http.HandlerFunc(deps.SLA.CreateTeamCalendar)))
	api.HandleH("PUT /teams/{id}/working-calendars/{calendarId}", auth(http.HandlerFunc(deps.SLA.UpdateTeamCalendar)))
	api.HandleH("DELETE /teams/{id}/working-calendars/{calendarId}", auth(http.HandlerFunc(deps.SLA.DeleteTeamCalendar)))
	api.HandleH("GET /teams/{id}/working-calendars/{calendarId}/impact", auth(http.HandlerFunc(deps.SLA.GetTeamCalendarImpact)))

	// Team-workspace bindings. Creating or deleting requires both team and
	// workspace administration; listing requires the relevant side's admin.
	api.HandleH("GET /workspaces/{id}/team-bindings", auth(http.HandlerFunc(deps.SLA.ListWorkspaceTeamBindings)))
	api.HandleH("POST /workspaces/{id}/team-bindings", auth(http.HandlerFunc(deps.SLA.CreateWorkspaceTeamBinding)))
	api.HandleH("DELETE /workspaces/{id}/team-bindings/{bindingId}", auth(http.HandlerFunc(deps.SLA.DeleteWorkspaceTeamBinding)))
	api.HandleH("GET /teams/{id}/workspace-bindings", auth(http.HandlerFunc(deps.SLA.ListTeamWorkspaceBindings)))
	api.HandleH("POST /teams/{id}/workspace-bindings", auth(http.HandlerFunc(deps.SLA.CreateTeamWorkspaceBinding)))
	api.HandleH("DELETE /teams/{id}/workspace-bindings/{bindingId}", auth(http.HandlerFunc(deps.SLA.DeleteTeamWorkspaceBinding)))

	// Metrics and goals.
	api.HandleH("GET /workspaces/{id}/sla/metrics", auth(http.HandlerFunc(deps.SLA.ListMetrics)))
	api.HandleH("POST /workspaces/{id}/sla/metrics", auth(http.HandlerFunc(deps.SLA.CreateMetric)))
	api.HandleH("GET /workspaces/{id}/sla/metrics/{metricId}", auth(http.HandlerFunc(deps.SLA.GetMetric)))
	api.HandleH("PUT /workspaces/{id}/sla/metrics/{metricId}", auth(http.HandlerFunc(deps.SLA.UpdateMetric)))
	api.HandleH("DELETE /workspaces/{id}/sla/metrics/{metricId}", auth(http.HandlerFunc(deps.SLA.DeleteMetric)))

	// Jira SLA configuration and cycle import.
	api.HandleH("POST /workspaces/{id}/sla/import/preview", auth(http.HandlerFunc(deps.SLA.PreviewSLAImport)))
	api.HandleH("POST /workspaces/{id}/sla/import", auth(http.HandlerFunc(deps.SLA.ImportSLA)))

	// Recalculation progress.
	api.HandleH("GET /workspaces/{id}/sla/report", auth(http.HandlerFunc(deps.SLA.GetReport)))
	api.HandleH("GET /workspaces/{id}/sla/recalculations", auth(http.HandlerFunc(deps.SLA.ListRecalculations)))
	api.HandleH("POST /workspaces/{id}/sla/recalculations", auth(http.HandlerFunc(deps.SLA.StartRecalculation)))

	// Warning thresholds.
	api.HandleH("GET /workspaces/{id}/sla/warning-thresholds", auth(http.HandlerFunc(deps.SLA.ListWarningThresholds)))
	api.HandleH("POST /workspaces/{id}/sla/warning-thresholds", auth(http.HandlerFunc(deps.SLA.CreateWarningThreshold)))
	api.HandleH("PUT /workspaces/{id}/sla/warning-thresholds/{thresholdId}", auth(http.HandlerFunc(deps.SLA.UpdateWarningThreshold)))
	api.HandleH("DELETE /workspaces/{id}/sla/warning-thresholds/{thresholdId}", auth(http.HandlerFunc(deps.SLA.DeleteWarningThreshold)))
}
