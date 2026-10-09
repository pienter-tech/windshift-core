/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import DataTable from './DataTable.svelte';

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params) => (params ? `${key} ${JSON.stringify(params)}` : key),
}));

const columns = [
  { key: 'name', label: 'Name' },
  { key: 'actions', label: '' },
];
const data = [
  { id: 1, name: 'Spring release' },
  { id: 2, name: 'Beta launch' },
];
const actionItems = () => [{ id: 'edit', type: 'regular', label: 'Edit', onClick: () => {} }];

afterEach(cleanup);

describe('DataTable row action button', () => {
  it('has a plain "Actions" name by default', () => {
    render(DataTable, {
      columns,
      data,
      actionItems,
      actionTriggerTestid: (item) => `row-actions-${item.id}`,
    });

    for (const item of data) {
      expect(screen.getByTestId(`row-actions-${item.id}`)).toHaveAccessibleName('common.actions');
    }
  });

  it('takes a per-row name from actionTriggerLabel', () => {
    render(DataTable, {
      columns,
      data,
      actionItems,
      actionTriggerTestid: (item) => `row-actions-${item.id}`,
      actionTriggerLabel: (item) => `Actions for ${item.name}`,
    });

    expect(screen.getByTestId('row-actions-1')).toHaveAccessibleName('Actions for Spring release');
    expect(screen.getByTestId('row-actions-2')).toHaveAccessibleName('Actions for Beta launch');
    expect(screen.getByRole('button', { name: 'Actions for Beta launch' })).toBe(
      screen.getByTestId('row-actions-2')
    );
  });
});
