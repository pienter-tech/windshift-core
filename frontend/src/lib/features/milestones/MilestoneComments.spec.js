/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MilestoneComments from './MilestoneComments.svelte';

const mocks = vi.hoisted(() => ({ getComments: vi.fn() }));

vi.mock('../../api.js', () => ({
  api: { milestones: { getComments: mocks.getComments } },
}));
vi.mock('../../stores', () => ({ authStore: { currentUser: { id: 1, first_name: 'Ada' } } }));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../../composables/useConfirm.js', () => ({ confirm: vi.fn() }));
// The Markdown editor is irrelevant to ordering; render nothing for it.
vi.mock('../../editors/LazyMilkdownEditor.svelte', () => ({ default: () => {} }));

describe('MilestoneComments', () => {
  beforeEach(() => {
    vi.clearAllMocks();
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
});
