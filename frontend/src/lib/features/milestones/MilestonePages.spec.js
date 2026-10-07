/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MilestonePages from './MilestonePages.svelte';

const mocks = vi.hoisted(() => ({
  getPageLinks: vi.fn(),
  linkPage: vi.fn(),
  unlinkPage: vi.fn(),
  searchPages: vi.fn(),
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
vi.mock('../../stores/toasts.svelte.js', () => ({ errorToast: vi.fn() }));

async function openPicker() {
  render(MilestonePages, { milestoneId: 1, workspaceId: 7, canEdit: true });
  const add = await screen.findByTestId('milestone-page-add');
  await fireEvent.click(add);
  const input = await screen.findByTestId('milestone-page-picker');
  await waitFor(() => expect(input).toHaveFocus());
  return { add, input };
}

describe('MilestonePages picker', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getPageLinks.mockResolvedValue([]);
    mocks.searchPages.mockResolvedValue({ results: [] });
  });

  afterEach(() => cleanup());

  it('closes on Escape from the search box and refocuses "+ Add"', async () => {
    const { add, input } = await openPicker();
    expect(add).toHaveAttribute('aria-expanded', 'true');

    await fireEvent.keyDown(input, { key: 'Escape' });

    await waitFor(() => expect(screen.queryByTestId('milestone-page-picker')).toBeNull());
    expect(add).toHaveAttribute('aria-expanded', 'false');
    expect(add).toHaveFocus();
  });

  it('closes on Escape with the dropdown open and closes the dropdown too', async () => {
    const { add, input } = await openPicker();
    await fireEvent.click(input);
    await waitFor(() => expect(screen.getByTestId('picker-dropdown')).toBeInTheDocument());

    await fireEvent.keyDown(input, { key: 'Escape' });

    await waitFor(() => expect(screen.queryByTestId('milestone-page-picker')).toBeNull());
    expect(screen.queryByTestId('picker-dropdown')).toBeNull();
    expect(add).toHaveAttribute('aria-expanded', 'false');
    expect(add).toHaveFocus();
  });

  it('closes on Escape even when the search box stops the event from bubbling', async () => {
    const { add, input } = await openPicker();
    // The picker must not depend on Escape bubbling out of the combobox input.
    input.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') event.stopPropagation();
    });

    await fireEvent.keyDown(input, { key: 'Escape' });

    await waitFor(() => expect(screen.queryByTestId('milestone-page-picker')).toBeNull());
    expect(add).toHaveAttribute('aria-expanded', 'false');
    expect(add).toHaveFocus();
  });

  it('still closes the picker after linking a page', async () => {
    const page = { id: 42, title: 'Spec page' };
    mocks.searchPages.mockResolvedValue({ results: [page] });
    mocks.linkPage.mockResolvedValue({
      id: 9,
      page_id: 42,
      page_title: 'Spec page',
      workspace_id: 7,
    });
    const { add, input } = await openPicker();

    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'Spec' } });
    await fireEvent.click(await screen.findByText('Spec page', {}, { timeout: 2000 }));

    await waitFor(() => expect(mocks.linkPage).toHaveBeenCalledWith(1, 42));
    await waitFor(() => expect(screen.queryByTestId('milestone-page-picker')).toBeNull());
    expect(add).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getAllByTestId('milestone-page-row')).toHaveLength(1);
  });

  it('still toggles the picker from "+ Add"', async () => {
    const { add } = await openPicker();

    await fireEvent.click(add);
    await waitFor(() => expect(screen.queryByTestId('milestone-page-picker')).toBeNull());
    expect(add).toHaveAttribute('aria-expanded', 'false');

    await fireEvent.click(add);
    expect(await screen.findByTestId('milestone-page-picker')).toBeInTheDocument();
    expect(add).toHaveAttribute('aria-expanded', 'true');
  });
});
