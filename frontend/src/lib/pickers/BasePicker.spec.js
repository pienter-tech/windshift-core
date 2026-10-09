/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import BasePicker from './BasePicker.svelte';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

const items = [
  { id: 1, name: 'Alpha' },
  { id: 2, name: 'Beta' },
];

function renderPicker(props = {}) {
  const callbacks = { onCancel: vi.fn(), onEscape: vi.fn(), onSelect: vi.fn() };
  render(BasePicker, { items, inputTestid: 'picker-input', ...callbacks, ...props });
  return { input: screen.queryByTestId('picker-input'), ...callbacks };
}

async function openDropdown(input) {
  input.focus();
  await fireEvent.click(input);
  return screen.findByTestId('picker-dropdown');
}

function expectDropdownClosed() {
  return waitFor(() => expect(screen.queryByTestId('picker-dropdown')).toBeNull());
}

// WCORE-56: onEscape is Escape-only; onCancel keeps its catch-all contract.
describe('BasePicker onEscape and onCancel', () => {
  afterEach(() => cleanup());

  it('calls onEscape and then onCancel on Escape with the dropdown open', async () => {
    const { input, onEscape, onCancel } = renderPicker();
    await openDropdown(input);
    onCancel.mockImplementation(() => expect(onEscape).toHaveBeenCalledTimes(1));

    await fireEvent.keyDown(input, { key: 'Escape' });
    await expectDropdownClosed();

    expect(onEscape).toHaveBeenCalledTimes(1);
    expect(onEscape).toHaveBeenCalledWith({ open: true });
    expect(onCancel).toHaveBeenCalled();
  });

  it('calls onEscape with open false on Escape with the dropdown closed', async () => {
    const { input, onEscape, onCancel } = renderPicker();
    input.focus();

    await fireEvent.keyDown(input, { key: 'Escape' });

    expect(onEscape).toHaveBeenCalledTimes(1);
    expect(onEscape).toHaveBeenCalledWith({ open: false });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('calls onCancel but not onEscape when Tab closes the dropdown', async () => {
    const { input, onEscape, onCancel } = renderPicker();
    await openDropdown(input);

    await fireEvent.keyDown(input, { key: 'Tab' });
    await expectDropdownClosed();

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onEscape).not.toHaveBeenCalled();
  });

  it('calls onCancel but not onEscape on a click outside', async () => {
    const { input, onEscape, onCancel } = renderPicker();
    await openDropdown(input);

    await fireEvent.pointerDown(document.body);
    await fireEvent.pointerUp(document.body);
    await expectDropdownClosed();

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onEscape).not.toHaveBeenCalled();
  });

  it('calls onCancel but not onEscape after a keyboard pick', async () => {
    const { input, onEscape, onCancel, onSelect } = renderPicker();
    await openDropdown(input);

    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'Enter' });
    await expectDropdownClosed();

    expect(onSelect).toHaveBeenCalledWith(items[1]);
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onEscape).not.toHaveBeenCalled();
  });

  it('does not call onEscape after a mouse pick', async () => {
    const { input, onEscape, onSelect } = renderPicker();
    const dropdown = await openDropdown(input);

    await fireEvent.click(within(dropdown).getByText('Beta'));
    await expectDropdownClosed();

    expect(onSelect).toHaveBeenCalledWith(items[1]);
    expect(onEscape).not.toHaveBeenCalled();
  });

  it('keeps calling onCancel on Escape without an onEscape prop', async () => {
    const onCancel = vi.fn();
    render(BasePicker, { items, inputTestid: 'picker-input', onCancel });
    const input = screen.getByTestId('picker-input');
    await openDropdown(input);

    await fireEvent.keyDown(input, { key: 'Escape' });
    await expectDropdownClosed();

    // Legacy contract: once for the key, once for the close.
    expect(onCancel).toHaveBeenCalledTimes(2);
  });

  it('calls onEscape and onCancel on Escape in the popover search field', async () => {
    const children = createRawSnippet(() => ({ render: () => '<span>Trigger</span>' }));
    const { onEscape, onCancel } = renderPicker({ children, searchTestid: 'picker-search' });
    await fireEvent.click(screen.getByText('Trigger'));
    const search = await screen.findByTestId('picker-search');

    await fireEvent.keyDown(search, { key: 'Escape' });
    await expectDropdownClosed();

    expect(onEscape).toHaveBeenCalledTimes(1);
    expect(onEscape).toHaveBeenCalledWith({ open: true });
    expect(onCancel).toHaveBeenCalled();
  });
});
