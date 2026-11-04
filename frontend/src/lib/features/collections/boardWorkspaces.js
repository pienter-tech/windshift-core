/**
 * Resolve the workspaces a board may read or create into.
 *
 * The workspace directory caches only the first server page (WI-1442), so a
 * workspace-scoped board whose workspace sorts beyond that page would lose its
 * quick-add target. The board's own workspace is always known from the route
 * bootstrap, so merge it back in whenever the board scope lists it.
 */
export function resolveBoardWorkspaces({
  scopeLoaded,
  allowsAllWorkspaces,
  directoryWorkspaces,
  scopedIds,
  currentWorkspace,
}) {
  if (!scopeLoaded) return [];
  const directory = directoryWorkspaces || [];
  if (allowsAllWorkspaces) return directory;
  const scoped = directory.filter((candidate) => scopedIds.includes(candidate.id));
  if (
    currentWorkspace?.id &&
    scopedIds.includes(currentWorkspace.id) &&
    !scoped.some((candidate) => candidate.id === currentWorkspace.id)
  ) {
    return [currentWorkspace, ...scoped];
  }
  return scoped;
}
