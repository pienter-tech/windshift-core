// Workflow transitions the approval-set and condition-set editors may target.
// Initial transitions (from_status_id IS NULL, not from-all) create items and
// are not selectable moves.
export function isSelectableTransition(transition) {
  return transition.from_status_id != null || transition.from_all_statuses;
}

// Transitions usable out of a specific status: the directed rows plus from-all
// rows, minus from-all rows shadowed by a directed one to the same target
// (matching WorkflowService.GetTransitionsForItem).
export function transitionsFromStatus(transitions, statusId) {
  if (!statusId) return [];
  const direct = transitions.filter((tr) => tr.from_status_id === statusId);
  const directTargets = new Set(direct.map((tr) => tr.to_status_id));
  return [
    ...direct,
    ...transitions.filter((tr) => tr.from_all_statuses && !directTargets.has(tr.to_status_id)),
  ];
}
