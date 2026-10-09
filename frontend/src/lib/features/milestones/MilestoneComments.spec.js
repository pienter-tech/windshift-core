/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { flushSync } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MilestoneComments from './MilestoneComments.svelte';

const mocks = vi.hoisted(() => ({
  getComments: vi.fn(),
  createComment: vi.fn(),
  // Props of every editor instance, in mount order.
  editors: [],
}));

vi.mock('../../api.js', () => ({
  api: { milestones: { getComments: mocks.getComments, createComment: mocks.createComment } },
}));
vi.mock('../../stores', () => ({ authStore: { currentUser: { id: 1, first_name: 'Ada' } } }));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../../composables/useConfirm.js', () => ({ confirm: vi.fn() }));
// Render nothing for the Markdown editor; record its props so a test can
// type into the composer through bind:content and spot remounts.
vi.mock('../../editors/LazyMilkdownEditor.svelte', () => ({
  default: (_anchor, props) => {
    mocks.editors.push(props);
    return { focus: () => {}, focusEnd: () => {}, clear: () => {} };
  },
}));

const composers = () =>
  mocks.editors.filter((props) => props.testId === 'milestone-comment-composer');

function deferred() {
  let resolve;
  const promise = new Promise((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

const thread = [
  { id: 1, author_id: 1, content: 'first', created_at: '2026-01-01T10:00:00Z' },
  { id: 2, author_id: 2, content: 'second', created_at: '2026-01-02T10:00:00Z' },
  { id: 3, author_id: 1, content: 'third', created_at: '2026-01-03T10:00:00Z' },
];
const posted = { id: 4, author_id: 1, content: 'fourth', created_at: '2026-01-04T10:00:00Z' };

describe('MilestoneComments', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.editors.length = 0;
    // The API returns the whole thread oldest first.
    mocks.getComments.mockResolvedValue(thread);
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

  it('shows the composer while the first comment load is pending', () => {
    mocks.getComments.mockReturnValue(deferred().promise);
    render(MilestoneComments, { milestoneId: 50 });

    expect(screen.getByTestId('milestone-comment-editor')).toBeInTheDocument();
    expect(screen.getByTestId('milestone-comment-submit')).toBeInTheDocument();
    expect(screen.getByText('common.loading')).toBeInTheDocument();
    expect(screen.queryByText('comments.noComments')).not.toBeInTheDocument();
  });

  it.each([
    ['includes it', [...thread, posted]],
    ['does not include it yet', thread],
  ])(
    'keeps one copy of a comment posted during the first load when the load %s',
    async (_, loaded) => {
      const load = deferred();
      mocks.getComments.mockReturnValue(load.promise);
      mocks.createComment.mockResolvedValue(posted);
      render(MilestoneComments, { milestoneId: 50 });

      flushSync(() => {
        composers()[0].content = 'fourth';
      });
      await fireEvent.click(screen.getByTestId('milestone-comment-submit'));
      await waitFor(() => expect(composers()[0].content).toBe(''));
      expect(mocks.createComment).toHaveBeenCalledWith(50, 'fourth');

      load.resolve(loaded);
      await waitFor(() => expect(screen.queryByText('common.loading')).not.toBeInTheDocument());

      const rows = screen.getAllByTestId('milestone-comment');
      expect(rows.map((row) => row.dataset.commentId)).toEqual(['4', '3', '2', '1']);
    }
  );

  it('keeps a draft through the first load and a milestone reload', async () => {
    const load = deferred();
    mocks.getComments.mockReturnValue(load.promise);
    const { rerender } = render(MilestoneComments, { milestoneId: 50, workspaceId: null });

    flushSync(() => {
      composers()[0].content = 'draft';
    });
    load.resolve(thread);
    await screen.findAllByTestId('milestone-comment');
    // MilestoneDetail keeps this component mounted across its reload and
    // only passes the reloaded milestone's workspace again.
    await rerender({ milestoneId: 50, workspaceId: 7 });

    expect(composers()).toHaveLength(1);
    expect(composers()[0].content).toBe('draft');
    expect(composers()[0].workspaceId).toBe(7);
    expect(screen.getByTestId('milestone-comment-submit')).not.toBeDisabled();
    expect(mocks.getComments).toHaveBeenCalledTimes(1);
  });
});
