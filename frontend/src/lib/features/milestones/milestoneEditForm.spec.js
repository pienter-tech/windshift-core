import { afterEach, describe, expect, it, vi } from 'vitest';
import { milestones } from '../../api/milestones.js';
import { milestoneEditForm, milestoneSaveData } from './milestoneEditForm.js';

// Shapes match the real v2 responses: the progress report has no category_id,
// is_global, or workspace_id; the milestone record has all three.
const progress = {
  milestone_id: 50,
  milestone_name: 'Beta',
  description: 'Ship it',
  target_date: '2026-11-01T00:00:00Z',
  status: 'in-progress',
  category_color: '#3b82f6',
  total_items: 0,
  completed_items: 0,
  percent_complete: 0,
  status_breakdown: [],
  items_by_category: {},
};
const record = {
  id: 50,
  name: 'Beta',
  status: 'in-progress',
  category_id: 3,
  is_global: false,
  workspace_id: 7,
};

describe('milestoneEditForm', () => {
  it('opens with the milestone category from the record', () => {
    const form = milestoneEditForm({ progress, milestone: record, workspaceId: '7' });

    expect(form).toEqual({
      name: 'Beta',
      description: 'Ship it',
      target_date: '2026-11-01',
      status: 'in-progress',
      category_id: 3,
      is_global: false,
      workspace_id: 7,
    });
  });

  it('opens with no category for an uncategorised milestone', () => {
    const form = milestoneEditForm({ progress, milestone: { ...record, category_id: undefined } });

    expect(form.category_id).toBeNull();
  });

  it('keeps global scope from the record', () => {
    const form = milestoneEditForm({
      progress,
      milestone: { id: 50, category_id: 3, is_global: true },
      workspaceId: '7',
    });

    expect(form.is_global).toBe(true);
    expect(form.category_id).toBe(3);
  });

  it('leaves the category out without the record instead of clearing it', () => {
    const form = milestoneEditForm({ progress, milestone: null, workspaceId: '7' });

    expect(form.category_id).toBeUndefined();
    expect(form.is_global).toBe(false);
    expect(form.workspace_id).toBe(7);
  });
});

describe('milestoneSaveData', () => {
  it('sends an empty target date as null', () => {
    const form = { ...milestoneEditForm({ progress, milestone: record }), target_date: '' };

    expect(milestoneSaveData(form, record)).toMatchObject({ target_date: null, category_id: 3 });
  });

  it('keeps the scope of an existing workspace milestone', () => {
    const form = {
      ...milestoneEditForm({ progress, milestone: record }),
      is_global: true,
      workspace_id: null,
    };

    expect(milestoneSaveData(form, record)).toMatchObject({ is_global: false, workspace_id: 7 });
  });

  it('keeps the scope of an existing global milestone', () => {
    const globalRecord = { ...record, is_global: true, workspace_id: null };
    const form = {
      ...milestoneEditForm({ progress, milestone: globalRecord }),
      is_global: false,
      workspace_id: 7,
    };

    expect(milestoneSaveData(form, globalRecord)).toMatchObject({
      is_global: true,
      workspace_id: null,
    });
  });

  it('uses the chosen scope when creating', () => {
    const form = {
      name: 'New',
      description: '',
      target_date: '',
      status: 'planning',
      category_id: null,
      is_global: true,
      workspace_id: 7,
    };

    expect(milestoneSaveData(form)).toMatchObject({
      is_global: true,
      workspace_id: null,
      target_date: null,
    });
  });
});

describe('milestone edit patch', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  async function patchBody(form) {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ data: {} }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      })
    );
    vi.stubGlobal('fetch', fetchMock);
    await milestones.update(50, form);
    const [url, options] = fetchMock.mock.calls[0];
    expect(url).toBe('/api/v2/milestones/50');
    expect(options.method).toBe('PATCH');
    return JSON.parse(options.body);
  }

  it('keeps the category when the user edits other fields', async () => {
    const form = { ...milestoneEditForm({ progress, milestone: record }), name: 'Beta 2' };

    expect(await patchBody(form)).toMatchObject({ name: 'Beta 2', category_id: 3 });
  });

  it('clears the category when the user picks no category', async () => {
    const form = { ...milestoneEditForm({ progress, milestone: record }), category_id: null };

    expect(await patchBody(form)).toHaveProperty('category_id', null);
  });

  it('omits the category without the record', async () => {
    const form = milestoneEditForm({ progress, milestone: null, workspaceId: '7' });

    expect(await patchBody(form)).not.toHaveProperty('category_id');
  });
});
