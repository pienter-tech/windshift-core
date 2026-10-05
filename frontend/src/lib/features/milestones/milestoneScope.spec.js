import { describe, expect, it } from 'vitest';
import { canManageMilestone, milestoneWorkspaceId } from './milestoneScope.js';

// Shapes match the real v2 responses: the milestone record carries the scope,
// the progress report does not (WCORE-29).
const workspaceMilestone = {
  id: 50,
  name: 'Beta',
  status: 'planning',
  is_global: false,
  workspace_id: 7,
};
const globalMilestone = { id: 51, name: 'Launch', status: 'planning', is_global: true };
const progressReport = {
  milestone_id: 50,
  milestone_name: 'Beta',
  description: '',
  status: 'planning',
  total_items: 0,
  completed_items: 0,
  percent_complete: 0,
  status_breakdown: [],
  items_by_category: {},
};

function access({ admin = false, global = [], workspace = {} } = {}) {
  return {
    isSystemAdmin: admin,
    hasGlobalPermission: (key) => global.includes(key),
    hasWorkspacePermission: (workspaceId, key) => (workspace[workspaceId] ?? []).includes(key),
  };
}

describe('milestoneWorkspaceId', () => {
  it('returns the workspace of a workspace milestone', () => {
    expect(milestoneWorkspaceId(workspaceMilestone)).toBe(7);
  });

  it('returns null for a global milestone', () => {
    expect(milestoneWorkspaceId(globalMilestone)).toBeNull();
    expect(milestoneWorkspaceId({ ...globalMilestone, workspace_id: 7 })).toBeNull();
  });

  it('returns null without a milestone record', () => {
    expect(milestoneWorkspaceId(null)).toBeNull();
    // The progress report has no scope fields, so it never yields a workspace.
    expect(milestoneWorkspaceId(progressReport)).toBeNull();
  });
});

describe('canManageMilestone', () => {
  it('lets a workspace editor manage a workspace milestone without being a system admin', () => {
    expect(
      canManageMilestone(workspaceMilestone, access({ workspace: { 7: ['item.edit'] } }))
    ).toBe(true);
    expect(
      canManageMilestone(workspaceMilestone, access({ workspace: { 7: ['workspace.admin'] } }))
    ).toBe(true);
  });

  it('denies users without edit rights in the milestone workspace', () => {
    expect(
      canManageMilestone(workspaceMilestone, access({ workspace: { 7: ['item.view'] } }))
    ).toBe(false);
    expect(
      canManageMilestone(workspaceMilestone, access({ workspace: { 8: ['item.edit'] } }))
    ).toBe(false);
  });

  it('requires milestone.create for global milestones', () => {
    expect(canManageMilestone(globalMilestone, access({ global: ['milestone.create'] }))).toBe(
      true
    );
    expect(canManageMilestone(globalMilestone, access({ workspace: { 7: ['item.edit'] } }))).toBe(
      false
    );
  });

  it('lets system admins manage any milestone', () => {
    expect(canManageMilestone(workspaceMilestone, access({ admin: true }))).toBe(true);
    expect(canManageMilestone(globalMilestone, access({ admin: true }))).toBe(true);
  });

  it('denies management before the milestone record loads', () => {
    expect(canManageMilestone(null, access({ admin: true }))).toBe(false);
  });
});
