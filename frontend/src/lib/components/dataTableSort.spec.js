import { describe, expect, it } from 'vitest';
import { nextColumnSort } from './dataTableSort.js';

const name = { key: 'name', sortable: true };
const updated = { key: 'last_updated_at', sortable: true };
const actions = { key: 'actions' };

describe('nextColumnSort', () => {
  it('cycles a column through ascending, descending, and unsorted', () => {
    const asc = nextColumnSort(null, name);
    expect(asc).toEqual({ key: 'name', direction: 'asc' });
    const desc = nextColumnSort(asc, name);
    expect(desc).toEqual({ key: 'name', direction: 'desc' });
    expect(nextColumnSort(desc, name)).toBeNull();
  });

  it('starts another column ascending', () => {
    expect(nextColumnSort({ key: 'name', direction: 'desc' }, updated)).toEqual({
      key: 'last_updated_at',
      direction: 'asc',
    });
  });

  it('clears an initial descending sort on the next click', () => {
    expect(nextColumnSort({ key: 'last_updated_at', direction: 'desc' }, updated)).toBeNull();
  });

  it('ignores columns that are not sortable', () => {
    /** @type {{ key: string, direction: 'asc' | 'desc' }} */
    const sort = { key: 'name', direction: 'asc' };
    expect(nextColumnSort(sort, actions)).toBe(sort);
    expect(nextColumnSort(null, actions)).toBeNull();
  });
});
