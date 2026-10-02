import { fetchV2Data } from './core.js';

// Built-in framework packs (WI-1141). System-administrator surface: list the
// packs embedded in the server, then apply or verify one against an existing
// workspace (workspace_id) or create/reuse one by name (workspace_name). The
// response is the same PackApplyReport the archive upload path produces.
export const packs = {
  list: () => fetchV2Data('/packs'),
  apply: (name, target) =>
    fetchV2Data(`/packs/${encodeURIComponent(name)}/apply`, {
      method: 'POST',
      body: JSON.stringify(target),
    }),
  verify: (name, target) =>
    fetchV2Data(`/packs/${encodeURIComponent(name)}/verify`, {
      method: 'POST',
      body: JSON.stringify(target),
    }),
};
