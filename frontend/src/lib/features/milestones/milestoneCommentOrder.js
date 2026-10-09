// Display order for milestone comments. The API and the CLI list
// them oldest first; the milestone page shows them newest first, so a new
// comment lands right below the composer.

function createdTime(comment) {
  const time = Date.parse(comment?.created_at ?? '');
  return Number.isNaN(time) ? 0 : time;
}

/**
 * A newest-first copy of the comments: by creation time, then id, both
 * descending (the API's oldest-first order reversed). Editing a comment
 * leaves its creation time alone, so it keeps its place.
 */
export function newestCommentsFirst(comments) {
  return [...(comments ?? [])].sort(
    (a, b) => createdTime(b) - createdTime(a) || (b.id ?? 0) - (a.id ?? 0)
  );
}
