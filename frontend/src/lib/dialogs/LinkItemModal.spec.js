/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import LinkItemModal from './LinkItemModal.svelte';

vi.mock('../api.js', () => ({
  api: {
    links: { search: vi.fn().mockResolvedValue([]) },
    pages: { searchPages: vi.fn().mockResolvedValue({ results: [] }) },
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

const pageLinkType = { id: 5, name: 'Page', allowed_entity_types: ['page'] };

let opener;

// The "+ Add" control lives outside the dialog; it has focus when the
// dialog opens, as after a keyboard activation.
async function openPageDialog(props = {}) {
  opener = document.createElement('button');
  opener.textContent = '+ Add';
  document.body.appendChild(opener);
  opener.focus();
  render(LinkItemModal, {
    isOpen: true,
    linkTypes: [pageLinkType],
    currentItemId: 1,
    workspaceId: 7,
    preselectLinkTypeId: pageLinkType.id,
    ...props,
  });
  const dialog = await screen.findByRole('dialog');
  const input = await waitFor(() => {
    const el = dialog.querySelector('#link-target-page-picker');
    expect(el).not.toBeNull();
    return el;
  });
  await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
  return { dialog, input };
}

function expectDialogClosed() {
  return waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
}

describe('LinkItemModal in Page mode', () => {
  beforeEach(() => vi.clearAllMocks());

  afterEach(() => {
    cleanup();
    opener?.remove();
    opener = null;
  });

  it('labels the Target Page field', async () => {
    const { input } = await openPageDialog();

    expect([...input.labels].map((label) => label.textContent.trim())).toEqual([
      'items.targetPage',
    ]);
    expect(input).toHaveAccessibleName('items.targetPage');
  });

  it('returns focus to the opener on Cancel', async () => {
    const { dialog } = await openPageDialog();

    await fireEvent.click(within(dialog).getByRole('button', { name: /common\.cancel/ }));

    await expectDialogClosed();
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it('returns focus to the opener on Escape', async () => {
    const { dialog } = await openPageDialog();
    // The pickers keep Escape from their search boxes; in the browser the
    // field then blurs and the next Escape reaches the dialog (WCORE-65).
    document.activeElement.blur();
    await waitFor(() => expect(dialog).toHaveFocus());

    await fireEvent.keyDown(document.activeElement, { key: 'Escape' });

    await expectDialogClosed();
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it('leaves focus alone when the opener is gone', async () => {
    const { dialog } = await openPageDialog();
    opener.remove();

    await fireEvent.click(within(dialog).getByRole('button', { name: /common\.cancel/ }));

    await expectDialogClosed();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(document.activeElement).toBe(document.body);
  });

  it('keeps focus where the caller moved it on close', async () => {
    const other = document.createElement('button');
    document.body.appendChild(other);
    try {
      const { dialog } = await openPageDialog({ oncancel: () => other.focus() });

      await fireEvent.click(within(dialog).getByRole('button', { name: /common\.cancel/ }));

      await expectDialogClosed();
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(other).toHaveFocus();
    } finally {
      other.remove();
    }
  });
});
