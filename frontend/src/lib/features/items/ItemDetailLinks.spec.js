/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ItemDetailLinks from './ItemDetailLinks.svelte';

vi.mock('../../api.js', () => ({ api: {} }));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

const itemLink = {
  id: 11,
  link_type_id: 2,
  source_type: 'item',
  source_id: 1,
  target_type: 'item',
  target_id: 2,
  target_title: 'Other item',
  target_item_number: 2,
  link_type_forward_label: 'relates to',
};

const pageLink = {
  id: 12,
  link_type_id: 3,
  source_type: 'item',
  source_id: 1,
  target_type: 'page',
  target_id: 5,
  target_title: 'Spec page',
};

afterEach(cleanup);

describe('ItemDetailLinks remove buttons', () => {
  it.each([
    ['linked-page-row', 'linked-page-delete', 12],
    ['linked-item-row', 'linked-item-delete', 11],
  ])(
    '%s: the remove button is in the tab order, named, and removes the link',
    async (rowId, buttonId, linkId) => {
      const onremovelink = vi.fn();
      render(ItemDetailLinks, {
        workspaceId: 1,
        itemId: '1',
        itemLinks: [itemLink, pageLink],
        onremovelink,
      });

      const row = screen.getByTestId(rowId);
      const button = within(row).getByTestId(buttonId);

      expect(button).toHaveAccessibleName('items.removeLink');
      expect(button).toHaveAttribute('type', 'button');
      // Hidden with opacity, not display:none, so keyboard users can reach it;
      // it shows on row hover and while focus is in the row.
      expect(button).not.toHaveClass('hidden');
      expect(button).toHaveClass(
        'opacity-0',
        'group-hover:opacity-100',
        'group-focus-within:opacity-100'
      );
      button.focus();
      expect(button).toHaveFocus();

      await fireEvent.click(button);
      expect(onremovelink).toHaveBeenCalledWith({ linkId });
    }
  );
});
