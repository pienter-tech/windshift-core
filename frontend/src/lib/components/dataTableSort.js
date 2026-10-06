// Column sort state of DataTable: `{ key, direction: 'asc' | 'desc' }`, or
// null when the rows keep their given order.

/**
 * The sort after a click on a column header. A new column starts ascending;
 * clicking the sorted column again sorts descending, then clears the sort.
 * Columns without `sortable` leave the sort unchanged.
 * @param {{ key: string, direction: 'asc' | 'desc' } | null} sort
 * @param {{ key: string, sortable?: boolean }} column
 * @returns {{ key: string, direction: 'asc' | 'desc' } | null}
 */
export function nextColumnSort(sort, column) {
  if (!column.sortable) return sort;
  if (sort?.key !== column.key) return { key: column.key, direction: 'asc' };
  if (sort.direction === 'asc') return { key: column.key, direction: 'desc' };
  return null;
}
