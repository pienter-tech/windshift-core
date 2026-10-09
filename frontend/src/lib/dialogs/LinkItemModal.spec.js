/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api.js';
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

const page = { id: 42, title: 'Spec page' };

// Types into the Target Page field and waits for the page option.
async function searchPage(input) {
  await fireEvent.click(input);
  await fireEvent.input(input, { target: { value: 'Spec' } });
  const dropdown = await screen.findByTestId('picker-dropdown');
  return within(dropdown).findByText('Spec page', {}, { timeout: 2000 });
}

async function pickWithKeyboard(input) {
  await searchPage(input);
  await fireEvent.keyDown(input, { key: 'Enter' });
}

async function pickWithMouse(input) {
  const option = await searchPage(input);
  // A mouse press in the dropdown, which lives outside the dialog, blurs
  // the field before the click picks the page.
  await fireEvent.mouseDown(option);
  input.blur();
  await fireEvent.mouseUp(option);
  await fireEvent.click(option);
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

  describe.each([
    ['keyboard', pickWithKeyboard],
    ['mouse', pickWithMouse],
  ])('after a %s pick', (_, pick) => {
    beforeEach(() => {
      api.pages.searchPages.mockResolvedValue({ results: [page] });
    });

    it('keeps focus in the dialog on the Clear button', async () => {
      const { dialog, input } = await openPageDialog();

      await pick(input);

      const clear = await within(dialog).findByRole('button', { name: 'common.clear' });
      await waitFor(() => expect(clear).toHaveFocus());
      expect(within(dialog).getByText('Spec page')).toBeInTheDocument();
    });

    it('submits on Enter', async () => {
      const onsubmit = vi.fn();
      const { dialog, input } = await openPageDialog({ onsubmit });
      await pick(input);
      await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));

      await fireEvent.keyDown(document.activeElement, { key: 'Enter' });

      expect(onsubmit).toHaveBeenCalledWith({
        link_type_id: pageLinkType.id,
        target_id: page.id,
        target_type: 'page',
      });
      await expectDialogClosed();
    });

    it('closes on Escape', async () => {
      const onsubmit = vi.fn();
      const { dialog, input } = await openPageDialog({ onsubmit });
      await pick(input);
      await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));

      await fireEvent.keyDown(document.activeElement, { key: 'Escape' });

      await expectDialogClosed();
      expect(onsubmit).not.toHaveBeenCalled();
    });
  });

  // WCORE-67: the dropdown lives outside the dialog. A press on its empty
  // space blurs the field and runs no picker code; focus must come back.
  it('returns focus to the field after a press on empty space in the dropdown', async () => {
    const { input } = await openPageDialog();
    await fireEvent.click(input);
    const dropdown = await screen.findByTestId('picker-dropdown');
    const empty = within(dropdown).getByText('pickers.noItemsFound');

    await fireEvent.mouseDown(empty);
    input.blur();
    await fireEvent.mouseUp(empty);
    await fireEvent.click(empty);

    await waitFor(() => expect(input).toHaveFocus());

    await fireEvent.keyDown(input, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId('picker-dropdown')).toBeNull());
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('moves focus to the dialog when the pressed field is gone', async () => {
    const { dialog, input } = await openPageDialog();
    await fireEvent.click(input);
    const dropdown = await screen.findByTestId('picker-dropdown');
    const empty = within(dropdown).getByText('pickers.noItemsFound');

    await fireEvent.mouseDown(empty);
    input.blur();
    input.remove();
    await fireEvent.mouseUp(empty);

    await waitFor(() => expect(dialog).toHaveFocus());
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
