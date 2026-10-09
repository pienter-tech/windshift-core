/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MilestoneComments from './MilestoneComments.svelte';

const mocks = vi.hoisted(() => ({ getComments: vi.fn(), editors: [] }));

vi.mock('../../api.js', () => ({
  api: { milestones: { getComments: mocks.getComments } },
}));
vi.mock('../../stores', () => ({ authStore: { currentUser: { id: 1, first_name: 'Ada' } } }));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../../composables/useConfirm.js', () => ({ confirm: vi.fn() }));
// Render nothing for the Markdown editor; record the props each one gets.
vi.mock('../../editors/LazyMilkdownEditor.svelte', () => ({
  default: (_anchor, props) => {
    mocks.editors.push({
      readonly: props.readonly ?? false,
      ariaLabel: props.ariaLabel ?? null,
      ariaLabelledBy: props.ariaLabelledBy ?? null,
    });
    return { focus: () => {}, focusEnd: () => {} };
  },
}));

describe('MilestoneComments', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.editors.length = 0;
    // The API returns the whole thread oldest first.
    mocks.getComments.mockResolvedValue([
      { id: 1, author_id: 1, content: 'first', created_at: '2026-01-01T10:00:00Z' },
      { id: 2, author_id: 2, content: 'second', created_at: '2026-01-02T10:00:00Z' },
      { id: 3, author_id: 1, content: 'third', created_at: '2026-01-03T10:00:00Z' },
    ]);
  });

  afterEach(() => cleanup());

  it('lists comments newest first below the composer', async () => {
    render(MilestoneComments, { milestoneId: 50 });

    const rows = await screen.findAllByTestId('milestone-comment');
    expect(rows.map((row) => row.dataset.commentId)).toEqual(['3', '2', '1']);

    const composer = screen.getByTestId('milestone-comment-editor');
    expect(
      composer.compareDocumentPosition(rows[0]) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(mocks.getComments).toHaveBeenCalledWith(50);
  });

  it('names the editable editors and leaves read-only renders unnamed', async () => {
    render(MilestoneComments, { milestoneId: 50 });
    await screen.findAllByTestId('milestone-comment');

    const editable = () => mocks.editors.filter((editor) => !editor.readonly);
    expect(editable()).toEqual([
      { readonly: false, ariaLabel: 'comments.comment', ariaLabelledBy: null },
    ]);
    const readOnly = mocks.editors.filter((editor) => editor.readonly);
    expect(readOnly).toHaveLength(3);
    for (const editor of readOnly) {
      expect(editor.ariaLabel).toBeNull();
      expect(editor.ariaLabelledBy).toBeNull();
    }

    await fireEvent.click(screen.getAllByTestId('milestone-comment-edit')[0]);
    expect(editable().at(-1)).toEqual({
      readonly: false,
      ariaLabel: 'comments.editComment',
      ariaLabelledBy: null,
    });
  });
});
