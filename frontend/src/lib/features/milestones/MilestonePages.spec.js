/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MilestonePages from './MilestonePages.svelte';

const mocks = vi.hoisted(() => ({
  getPageLinks: vi.fn(),
  linkPage: vi.fn(),
  unlinkPage: vi.fn(),
  searchPages: vi.fn(),
  errorToast: vi.fn(),
}));

vi.mock('../../api.js', () => ({
  api: {
    milestones: {
      getPageLinks: mocks.getPageLinks,
      linkPage: mocks.linkPage,
      unlinkPage: mocks.unlinkPage,
    },
    pages: { searchPages: mocks.searchPages },
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../../stores/toasts.svelte.js', () => ({ errorToast: mocks.errorToast }));

const page = { id: 42, title: 'Spec page' };

async function openDialog(props = {}) {
  render(MilestonePages, { milestoneId: 1, workspaceId: 7, canEdit: true, ...props });
  const add = await screen.findByTestId('milestone-page-add');
  await fireEvent.click(add);
  const dialog = await screen.findByTestId('milestone-page-link-modal');
  const input = within(dialog).getByTestId('milestone-page-picker');
  // Modal focuses the picker shortly after opening.
  await waitFor(() => expect(input).toHaveFocus());
  return { add, dialog, input };
}

async function choosePage(input) {
  await fireEvent.click(input);
  await fireEvent.input(input, { target: { value: 'Spec' } });
  const dropdown = await screen.findByTestId('picker-dropdown');
  await fireEvent.click(await within(dropdown).findByText('Spec page', {}, { timeout: 2000 }));
}

// A mouse press as the browser handles it: unless the page prevents the
// default action, the press moves focus off the field. The dropdown renders
// inside the dialog element, so focus goes to the dialog (WCORE-64).
async function press(el, field, dialog) {
  if (await fireEvent.mouseDown(el)) {
    field.blur();
    dialog.focus();
  }
  await fireEvent.mouseUp(el);
  await fireEvent.click(el);
}

function expectDialogClosed() {
  return waitFor(() => expect(screen.queryByTestId('milestone-page-link-modal')).toBeNull());
}

describe('MilestonePages link dialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getPageLinks.mockResolvedValue([]);
    mocks.searchPages.mockResolvedValue({ results: [page] });
    mocks.linkPage.mockResolvedValue({
      id: 9,
      page_id: 42,
      page_title: 'Spec page',
      workspace_id: 7,
    });
  });

  afterEach(() => cleanup());

  it('opens a modal dialog from "+ Add" instead of an inline picker', async () => {
    const { add, dialog } = await openDialog();

    expect(add).toHaveAttribute('aria-haspopup', 'dialog');
    expect(dialog).toHaveAttribute('role', 'dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(within(dialog).getByRole('heading', { name: 'items.addPage' })).toBeInTheDocument();
    // The dialog is named by its heading (WCORE-62).
    expect(dialog).toHaveAccessibleName('items.addPage');
    expect(within(dialog).getByTestId('milestone-page-link-cancel')).toHaveTextContent(
      'common.cancel'
    );
    const confirm = within(dialog).getByTestId('milestone-page-link-confirm');
    expect(confirm).toHaveTextContent('items.addPage');
    expect(confirm).toBeDisabled();
    // The picker lives in the dialog, not inline in the header card.
    expect(
      within(screen.getByTestId('milestone-pages')).queryByTestId('milestone-page-picker')
    ).toBeNull();
  });

  // WCORE-64: aria-modal hides everything outside the dialog element, so the
  // results must render inside it, as one listbox holding the options.
  it('lists the results as one listbox inside the dialog', async () => {
    const { dialog, input } = await openDialog();
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'Spec' } });
    await within(dialog).findByText('Spec page', {}, { timeout: 2000 });

    const listboxes = screen.getAllByRole('listbox');
    expect(listboxes).toHaveLength(1);
    const [listbox] = listboxes;
    expect(dialog).toContainElement(listbox);
    expect(input).toHaveAttribute('aria-controls', listbox.id);
    expect(
      within(listbox)
        .getAllByRole('option')
        .map((o) => o.textContent.trim())
    ).toEqual(['Spec page']);
  });

  it('searches pages in the milestone workspace only', async () => {
    const { input } = await openDialog();
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'Spec' } });

    await waitFor(() => expect(mocks.searchPages).toHaveBeenCalled());
    expect(mocks.searchPages.mock.calls.every(([workspaceId]) => workspaceId === 7)).toBe(true);
  });

  it('links the chosen page, closes and lists it', async () => {
    const { add, dialog, input } = await openDialog();
    await choosePage(input);

    const confirm = within(dialog).getByTestId('milestone-page-link-confirm');
    await waitFor(() => expect(confirm).toBeEnabled());
    await fireEvent.click(confirm);

    await waitFor(() => expect(mocks.linkPage).toHaveBeenCalledWith(1, 42));
    await expectDialogClosed();
    const rows = screen.getAllByTestId('milestone-page-row');
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveTextContent('Spec page');
    expect(add).toHaveFocus();
  });

  it('keeps the dialog open when linking fails', async () => {
    mocks.linkPage.mockRejectedValue(new Error('Page is already linked to this milestone'));
    const { dialog, input } = await openDialog();
    await choosePage(input);

    const confirm = within(dialog).getByTestId('milestone-page-link-confirm');
    await waitFor(() => expect(confirm).toBeEnabled());
    await fireEvent.click(confirm);

    await waitFor(() =>
      expect(mocks.errorToast).toHaveBeenCalledWith(
        'Page is already linked to this milestone',
        'errors.failedToUpdate'
      )
    );
    expect(screen.getByTestId('milestone-page-link-modal')).toBeInTheDocument();
    expect(screen.queryAllByTestId('milestone-page-row')).toHaveLength(0);
  });

  it('rejects a page that is already linked', async () => {
    mocks.getPageLinks.mockResolvedValue([
      { id: 9, page_id: 42, page_title: 'Spec page', workspace_id: 7 },
    ]);
    const { dialog, input } = await openDialog();
    await choosePage(input);

    expect(await within(dialog).findByTestId('milestone-page-already-linked')).toHaveTextContent(
      'pickers.alreadyLinked'
    );
    const confirm = within(dialog).getByTestId('milestone-page-link-confirm');
    expect(confirm).toBeDisabled();
    await fireEvent.click(confirm);
    expect(mocks.linkPage).not.toHaveBeenCalled();
  });

  it('closes on Cancel without linking and refocuses "+ Add"', async () => {
    const { add, dialog, input } = await openDialog();
    await choosePage(input);

    await fireEvent.click(within(dialog).getByTestId('milestone-page-link-cancel'));

    await expectDialogClosed();
    expect(mocks.linkPage).not.toHaveBeenCalled();
    expect(add).toHaveFocus();
  });

  it('closes on Escape from the search box and refocuses "+ Add"', async () => {
    const { add, input } = await openDialog();

    await fireEvent.keyDown(input, { key: 'Escape' });

    await expectDialogClosed();
    expect(mocks.linkPage).not.toHaveBeenCalled();
    expect(add).toHaveFocus();
  });

  it('closes only the dropdown on the first Escape, then the dialog', async () => {
    const { add, input } = await openDialog();
    await fireEvent.click(input);
    await waitFor(() => expect(screen.getByTestId('picker-dropdown')).toBeInTheDocument());

    await fireEvent.keyDown(input, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId('picker-dropdown')).toBeNull());
    expect(screen.getByTestId('milestone-page-link-modal')).toBeInTheDocument();

    await fireEvent.keyDown(input, { key: 'Escape' });
    await expectDialogClosed();
    expect(add).toHaveFocus();
  });

  // WCORE-65: in the browser the first Escape can leave the search box
  // blurred. Focus must then stay in the dialog, so that the next Escape,
  // which the browser sends to the focused element, still closes it.
  it('closes on the next Escape after the first one blurs the search box', async () => {
    const { add, dialog, input } = await openDialog();
    await fireEvent.click(input);
    await waitFor(() => expect(screen.getByTestId('picker-dropdown')).toBeInTheDocument());

    await fireEvent.keyDown(input, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId('picker-dropdown')).toBeNull());
    expect(dialog).toBeInTheDocument();
    // The field blurs after the picker has finished with the Escape (its own
    // focus restore runs on the next animation frame).
    await new Promise((resolve) => requestAnimationFrame(resolve));
    input.blur();
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    expect(document.activeElement).not.toBe(document.body);

    await fireEvent.keyDown(document.activeElement, { key: 'Escape' });
    await expectDialogClosed();
    expect(add).toHaveFocus();
  });

  it('closes on the next Escape after the search box loses focus with its dropdown closed', async () => {
    const { add, dialog, input } = await openDialog();

    input.blur();
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    expect(document.activeElement).not.toBe(document.body);

    await fireEvent.keyDown(document.activeElement, { key: 'Escape' });
    await expectDialogClosed();
    expect(add).toHaveFocus();
  });

  it('returns focus to the search box after a page is picked with the mouse', async () => {
    const { dialog, input } = await openDialog();
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'Spec' } });
    const dropdown = await screen.findByTestId('picker-dropdown');
    const option = await within(dropdown).findByText('Spec page', {}, { timeout: 2000 });

    // The press keeps focus on the field.
    await press(option, input, dialog);

    await waitFor(() => expect(input).toHaveFocus());
    expect(within(dialog).getByTestId('milestone-page-link-confirm')).toBeEnabled();
  });

  // WCORE-67: a press on the dropdown's empty space runs no picker code.
  // Focus must stay on the field, so Escape still closes the dropdown and
  // then the dialog.
  it('keeps focus in the dialog after a press on empty space in the dropdown', async () => {
    const { add, dialog, input } = await openDialog();
    await fireEvent.click(input);
    const dropdown = await screen.findByTestId('picker-dropdown');
    const empty = within(dropdown).getByText('pickers.noItemsFound');

    await press(empty, input, dialog);

    await waitFor(() => expect(input).toHaveFocus());
    expect(screen.getByTestId('picker-dropdown')).toBeInTheDocument();

    await fireEvent.keyDown(input, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId('picker-dropdown')).toBeNull());
    expect(screen.getByTestId('milestone-page-link-modal')).toBeInTheDocument();

    await fireEvent.keyDown(input, { key: 'Escape' });
    await expectDialogClosed();
    expect(add).toHaveFocus();
  });

  it('closes on the X button and refocuses "+ Add"', async () => {
    const { add, dialog } = await openDialog();

    await fireEvent.click(within(dialog).getByRole('button', { name: 'aria.close' }));

    await expectDialogClosed();
    expect(mocks.linkPage).not.toHaveBeenCalled();
    expect(add).toHaveFocus();
  });

  it('closes on a click outside and refocuses "+ Add"', async () => {
    const { add, dialog } = await openDialog();

    // The Modal backdrop is the dialog element itself.
    await fireEvent.click(dialog);

    await expectDialogClosed();
    expect(mocks.linkPage).not.toHaveBeenCalled();
    expect(add).toHaveFocus();
  });

  // The dropdown renders in the dialog element, so a drag from it that is
  // released on the backdrop clicks the backdrop (WCORE-64).
  it('stays open when a press in the dropdown is released on the backdrop', async () => {
    const { dialog, input } = await openDialog();
    await fireEvent.click(input);
    const dropdown = await screen.findByTestId('picker-dropdown');
    expect(dialog).toContainElement(dropdown);

    await fireEvent.mouseDown(within(dropdown).getByText('pickers.noItemsFound'));
    await fireEvent.mouseUp(dialog);
    await fireEvent.click(dialog);

    expect(screen.getByTestId('milestone-page-link-modal')).toBeInTheDocument();
    expect(input).toHaveFocus();
  });

  it('opens a fresh dialog after closing', async () => {
    const { dialog, input } = await openDialog();
    await choosePage(input);
    await fireEvent.click(within(dialog).getByTestId('milestone-page-link-cancel'));
    await expectDialogClosed();

    await fireEvent.click(screen.getByTestId('milestone-page-add'));
    const reopened = await screen.findByTestId('milestone-page-link-modal');
    expect(within(reopened).getByTestId('milestone-page-link-confirm')).toBeDisabled();
  });

  it('gives viewers no "+ Add" and no remove buttons', async () => {
    mocks.getPageLinks.mockResolvedValue([
      { id: 9, page_id: 42, page_title: 'Spec page', workspace_id: 7 },
    ]);
    render(MilestonePages, { milestoneId: 1, workspaceId: 7, canEdit: false });

    expect(await screen.findByTestId('milestone-page-row')).toBeInTheDocument();
    expect(screen.queryByTestId('milestone-page-add')).toBeNull();
    expect(screen.queryByTestId('milestone-page-unlink')).toBeNull();
    expect(screen.queryByTestId('milestone-page-link-modal')).toBeNull();
  });
});
