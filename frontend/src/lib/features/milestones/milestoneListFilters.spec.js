import { describe, expect, it } from 'vitest';
import {
  DEFAULT_STATUS_FILTER,
  filterMilestones,
  milestoneWorkspaceOptions,
  parseStatusFilter,
} from './milestoneListFilters.js';

const milestones = [
  { id: 1, name: 'Beta launch', status: 'planning', is_global: true, workspace_id: null },
  {
    id: 2,
    name: 'Core release',
    status: 'in-progress',
    is_global: false,
    workspace_id: 15,
    workspace_name: 'Core',
  },
  {
    id: 3,
    name: 'Old release',
    status: 'completed',
    is_global: false,
    workspace_id: 15,
    workspace_name: 'Core',
  },
  { id: 4, name: 'Dropped idea', status: 'cancelled', is_global: false, workspace_id: 7 },
  {
    id: 5,
    name: 'Alpha docs',
    status: 'planning',
    is_global: false,
    workspace_id: 3,
    workspace_name: 'Docs',
  },
];

const ids = (list) => list.map((m) => m.id);

describe('filterMilestones', () => {
  it('hides completed and cancelled milestones with the default status filter', () => {
    expect(ids(filterMilestones(milestones, { statuses: DEFAULT_STATUS_FILTER }))).toEqual([
      1, 2, 5,
    ]);
  });

  it('does not restrict on empty selections', () => {
    expect(ids(filterMilestones(milestones, {}))).toEqual([1, 2, 3, 4, 5]);
  });

  it('filters by workspace, with global as its own option', () => {
    expect(ids(filterMilestones(milestones, { workspaceKeys: ['global'] }))).toEqual([1]);
    expect(ids(filterMilestones(milestones, { workspaceKeys: ['15', 'global'] }))).toEqual([
      1, 2, 3,
    ]);
  });

  it('searches names case-insensitively and combines with other filters', () => {
    expect(ids(filterMilestones(milestones, { search: '  RELEASE ' }))).toEqual([2, 3]);
    expect(
      ids(filterMilestones(milestones, { search: 'release', statuses: DEFAULT_STATUS_FILTER }))
    ).toEqual([2]);
  });
});

describe('milestoneWorkspaceOptions', () => {
  it('lists distinct workspaces of local milestones sorted by name', () => {
    expect(milestoneWorkspaceOptions(milestones, [{ id: 7, name: 'Archive' }])).toEqual([
      { key: '7', name: 'Archive' },
      { key: '15', name: 'Core' },
      { key: '3', name: 'Docs' },
    ]);
  });

  it('falls back to the id when no name is known', () => {
    expect(milestoneWorkspaceOptions([milestones[3]])).toEqual([{ key: '7', name: '#7' }]);
  });
});

describe('parseStatusFilter', () => {
  it('defaults to open statuses when nothing valid is stored', () => {
    expect(parseStatusFilter(null)).toEqual(DEFAULT_STATUS_FILTER);
    expect(parseStatusFilter('not json')).toEqual(DEFAULT_STATUS_FILTER);
    expect(parseStatusFilter('["planning","bogus"]')).toEqual(DEFAULT_STATUS_FILTER);
  });

  it('restores a stored selection, including an empty one', () => {
    expect(parseStatusFilter('["completed"]')).toEqual(['completed']);
    expect(parseStatusFilter('[]')).toEqual([]);
  });
});
