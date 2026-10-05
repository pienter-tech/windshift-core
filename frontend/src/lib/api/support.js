import { fetchAPIV2 } from './core.js';

// Serializes aggregate params; workspace_id is repeatable so the global
// widget can request its configured workspaces in one call.
function metricsQuery(params) {
  const query = new URLSearchParams();
  const { workspace_ids = [], ...rest } = params;
  for (const id of workspace_ids) {
    query.append('workspace_id', id);
  }
  for (const [key, value] of Object.entries(rest)) {
    if (value !== null && value !== undefined && value !== '') {
      query.append(key, value);
    }
  }
  const qs = query.toString();
  return qs ? `?${qs}` : '';
}

export const support = {
  /** Support metrics aggregate document (WI-1133); served as a raw document. */
  getMetricsAggregate: (params = {}) =>
    fetchAPIV2(`/support/metrics/aggregate${metricsQuery(params)}`),
};
