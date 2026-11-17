import { fetchV2Data } from './core.js';

// Built-in framework packs (WI-1141). System-administrator surface: list the
// packs embedded in the server, then apply or verify one against a new
// workspace named by workspace_name. Framework packs only apply to new
// workspaces; an existing name fails the workspace stage. The response is the
// same PackApplyReport the archive upload path produces.
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
