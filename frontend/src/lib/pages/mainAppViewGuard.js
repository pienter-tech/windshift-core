import { viewSettingsStore } from '../stores/viewSettings.svelte.js';

// The collection-scoped views this guard protects, keyed by the
// workspace-<view> route suffix.
const GUARDED_VIEW_IDS = new Set(viewSettingsStore.allViewIds);

/**
 * Resolves the redirect for a route pointing at a collection-scoped view the
 * current scope has disabled, or null when the route may stay. The workspace
 * default view wins when it is still enabled; otherwise the first enabled view
 * takes over.
 *
 * Workspace-only nav entries (agents, analytics, pages, …) are visibility
 * toggles: hiding one from the sidebar must not make its route unreachable,
 * so they are never redirected.
 */
export function getDisabledViewRedirect(route, workspace, viewSettings = viewSettingsStore) {
  const view = route?.view;
  if (!view?.startsWith('workspace-')) return null;
  const viewId = view.slice('workspace-'.length);
  if (!GUARDED_VIEW_IDS.has(viewId)) return null;

  const workspaceId = route?.params?.id;
  if (!workspaceId) return null;

  const collectionId = route?.params?.collectionId ?? null;

  const enabled = viewSettings.enabledViewIds(workspaceId, collectionId);
  if (enabled.includes(viewId)) return null;

  const workspaceDefault = workspace?.default_view;
  const target =
    workspaceDefault && enabled.includes(workspaceDefault) ? workspaceDefault : enabled[0];
  if (!target) return null;

  const prefix = collectionId ? `/collections/${collectionId}` : '';
  return `/workspaces/${workspaceId}${prefix}/${target}`;
}
