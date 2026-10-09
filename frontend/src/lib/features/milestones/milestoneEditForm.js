import { preservePlanningScope } from '../../utils/planningScope.js';
import { milestoneWorkspaceId } from './milestoneScope.js';

// Edit form of the milestone detail page (WCORE-34). Name, description,
// target date, and status come from the progress report the page shows;
// category and scope come from the milestone record (`GET /milestones/{id}`),
// because the progress report carries neither. The PATCH is a merge-patch, so
// a null `category_id` clears the category: never send one the user did not
// choose.

/**
 * @param {{ progress: object, milestone: object | null, workspaceId?: string | number | null }} sources
 *   `workspaceId` is the route's workspace, used only when the record is missing.
 */
export function milestoneEditForm({ progress, milestone, workspaceId = null }) {
  const routeWorkspaceId = workspaceId ? parseInt(String(workspaceId), 10) : null;
  return {
    name: progress.milestone_name,
    description: progress.description || '',
    target_date: progress.target_date ? progress.target_date.split('T')[0] : '',
    status: progress.status,
    // Without the record, leave the category out of the patch (undefined is
    // dropped by JSON.stringify) instead of clearing it.
    category_id: milestone ? (milestone.category_id ?? null) : undefined,
    is_global: milestone?.is_global ?? !workspaceId,
    workspace_id: milestoneWorkspaceId(milestone) ?? routeWorkspaceId,
  };
}

/**
 * Payload for saving MilestoneFormDialog's form, shared by the list and
 * detail pages (WCORE-50). An empty target date is sent as null, and an
 * existing milestone keeps its scope: the server never moves a milestone
 * between scopes on update.
 *
 * @param {object} formData the dialog's form
 * @param {object | null} editingMilestone the record being edited; null when creating
 */
export function milestoneSaveData(formData, editingMilestone = null) {
  const data = preservePlanningScope(
    { ...formData, target_date: formData.target_date || null },
    editingMilestone
  );
  if (data.is_global) {
    data.workspace_id = null;
  }
  return data;
}
