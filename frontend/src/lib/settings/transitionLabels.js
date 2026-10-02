// Human-readable "From" side of a workflow transition. Both initial
// transitions (from_status_id IS NULL) and from_all_statuses rows have no
// from_status_name, so the from_all_statuses flag is what tells them apart.
export function transitionFromLabel(transition) {
  if (!transition) return '';
  if (transition.from_all_statuses) return 'Any status';
  return transition.from_status_name || 'Initial';
}
