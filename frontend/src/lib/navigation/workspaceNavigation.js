import { GanttChart } from '@lucide/svelte';
import {
  IconAdjustments as Adjustments,
  IconUserStar as AgentIcon,
  IconChartBar as BarChart3,
  IconBook as Book,
  IconCalendar as Calendar,
  IconClock as Clock,
  IconFileCheck as FileCheck,
  IconFileStack as FileStack,
  IconGitBranch as GitBranch,
  IconInbox,
  IconKey as Key,
  IconList as List,
  IconListTree as ListTree,
  IconMapPin as MapPin,
  IconMessageCircle2 as MessageSquareText,
  IconFlag as Milestone,
  IconPackage as Package,
  IconPlayerPlay as Play,
  IconRefresh as Refresh,
  IconRepeat as Repeat,
  IconLayoutRows as Rows_3,
  IconSettings as SettingsCog,
  IconLayoutKanban as SquareKanban,
  IconTags as Tags,
  IconTrash as Trash,
  IconTrendingUp as TrendingUp,
  IconUsers as Users,
  IconBolt as Zap,
} from '@tabler/icons-svelte-runes';

/**
 * @typedef {Object} WorkspaceView
 * @property {string} id
 * @property {string} labelKey
 * @property {any}    icon
 * @property {string} [tooltipKey]
 * @property {string} [testId]
 * @property {string[]} [activeViews]  Route view names that highlight this item.
 */

/**
 * Collection-scoped workspace views (visible inside collections too). The
 * queue trails the board views: its route follows the selected collection so
 * collection-scoped queues are reachable, while its visibility toggle stays
 * in the workspace nav set because queue definitions are workspace-owned.
 * @type {WorkspaceView[]}
 */
export const workspaceViewItems = [
  {
    id: 'backlog',
    labelKey: 'workspaceSettings.views.backlog',
    icon: Rows_3,
    testId: 'workspace-nav-backlog',
  },
  {
    id: 'board',
    labelKey: 'workspaceSettings.views.board',
    icon: SquareKanban,
    testId: 'workspace-nav-board',
  },
  {
    id: 'list',
    labelKey: 'workspaceSettings.views.list',
    icon: List,
    testId: 'workspace-nav-list',
  },
  {
    id: 'tree',
    labelKey: 'workspaceSettings.views.tree',
    icon: ListTree,
    testId: 'workspace-nav-tree',
  },
  {
    id: 'map',
    labelKey: 'workspaceSettings.views.map',
    icon: MapPin,
    testId: 'workspace-nav-map',
  },
  {
    id: 'roadmap',
    labelKey: 'collections.roadmap',
    icon: GanttChart,
    testId: 'workspace-nav-roadmap',
  },
  {
    id: 'queue',
    labelKey: 'supportQueue.title',
    tooltipKey: 'supportQueue.description',
    icon: IconInbox,
    testId: 'workspace-nav-queue',
    activeViews: ['workspace-queue'],
  },
];

/**
 * Collection-scoped view ids, the only entries a collection-scope navigation
 * override may toggle; mirrors models.BoardViewIDs on the backend. The queue
 * deliberately stays out: it renders in the views group but only the
 * workspace scope can toggle it.
 * @type {Set<string>}
 */
export const COLLECTION_VIEW_IDS = new Set(['backlog', 'board', 'list', 'tree', 'map', 'roadmap']);

/**
 * Workspace tools which are not scoped to a collection.
 * @type {WorkspaceView[]}
 */
export const workspaceOnlyViews = [
  {
    id: 'agents',
    labelKey: 'users.agents.title',
    tooltipKey: 'users.agents.description',
    icon: AgentIcon,
    testId: 'workspace-nav-agents',
    activeViews: ['workspace-agents', 'workspace-agent-profile', 'workspace-agent-create'],
  },
  {
    id: 'iterations',
    labelKey: 'commandPalette.commands.iterations.label',
    tooltipKey: 'commandPalette.commands.iterations.description',
    icon: Calendar,
  },
  {
    id: 'milestones',
    labelKey: 'commandPalette.commands.milestones.label',
    tooltipKey: 'commandPalette.commands.milestones.description',
    icon: Milestone,
  },
  {
    id: 'analytics',
    labelKey: 'commandPalette.commands.analytics.label',
    tooltipKey: 'commandPalette.commands.analytics.description',
    icon: TrendingUp,
  },
  { id: 'actions', labelKey: 'actions.title', icon: Zap, testId: 'workspace-nav-actions' },
  {
    id: 'pages',
    labelKey: 'pages.treeHeading',
    icon: Book,
    activeViews: ['workspace-pages'],
    testId: 'workspace-nav-pages',
  },
];

/**
 * Test management navigation items, visible only when the test-management
 * module is enabled AND the user has view permission.
 * @type {WorkspaceView[]}
 */
export const testNavigationItems = [
  {
    id: 'test-cases',
    labelKey: 'commandPalette.commands.testCases.label',
    tooltipKey: 'commandPalette.commands.testCases.description',
    icon: FileCheck,
    activeViews: ['test-cases', 'test-case-detail', 'test-steps'],
  },
  {
    id: 'test-sets',
    labelKey: 'commandPalette.commands.testPlans.label',
    tooltipKey: 'commandPalette.commands.testPlans.description',
    icon: Package,
    activeViews: ['test-sets', 'test-set-detail'],
  },
  {
    id: 'test-templates',
    labelKey: 'commandPalette.commands.testTemplates.label',
    tooltipKey: 'commandPalette.commands.testTemplates.description',
    icon: FileStack,
    activeViews: ['test-templates', 'test-template-detail'],
  },
  {
    id: 'test-runs',
    labelKey: 'commandPalette.commands.testRuns.label',
    tooltipKey: 'commandPalette.commands.testRuns.description',
    icon: Play,
    activeViews: ['test-runs', 'test-run-detail', 'test-execution'],
  },
  {
    id: 'test-reports',
    labelKey: 'commandPalette.commands.testReports.label',
    tooltipKey: 'commandPalette.commands.testReports.description',
    icon: BarChart3,
    activeViews: ['test-reports'],
  },
];

/**
 * @typedef {Object} WorkspaceSettingsItem
 * @property {string}  id        Module id (matches the `/settings/<id>` route segment).
 * @property {string}  labelKey  i18n key for the module label (resolve with `t()`).
 * @property {any}     icon      Icon component.
 * @property {string}  view      Route view name that highlights this item.
 * @property {boolean} [danger]  Styled as a destructive action when true.
 * @property {string}  [permission] Workspace permission key required to see the module.
 */

/**
 * Workspace admin (Settings) modules, in display order. Drives the folded
 * admin sidebar (`WorkspaceAdminNav`) and its collapsed icon rail. Routes are
 * `/workspaces/:id/settings/<id>` — use `workspaceSettingsRoute(workspaceId, id)`.
 * @type {WorkspaceSettingsItem[]}
 */
export const workspaceSettingsItems = [
  {
    id: 'general',
    labelKey: 'workspaceSettings.tabs.general',
    icon: SettingsCog,
    view: 'workspace-settings-general',
  },
  {
    id: 'categories',
    labelKey: 'workspaceSettings.tabs.categories',
    icon: Tags,
    view: 'workspace-settings-categories',
  },
  {
    id: 'members',
    labelKey: 'workspaceSettings.tabs.members',
    icon: Users,
    view: 'workspace-settings-members',
  },
  {
    id: 'configuration',
    labelKey: 'workspaceSettings.tabs.configurationSets',
    icon: Adjustments,
    view: 'workspace-settings-configuration',
  },
  {
    id: 'source-control',
    labelKey: 'workspaceSettings.tabs.sourceControl',
    icon: GitBranch,
    view: 'workspace-settings-source-control',
  },
  {
    id: 'issue-sync',
    labelKey: 'workspaceSettings.tabs.issueSync',
    icon: Refresh,
    view: 'workspace-settings-issue-sync',
  },
  {
    id: 'action-credentials',
    labelKey: 'workspaceSettings.tabs.actionCredentials',
    icon: Key,
    view: 'workspace-settings-action-credentials',
    permission: 'action.credential.manage',
  },
  {
    id: 'recurrence',
    labelKey: 'workspaceSettings.tabs.recurrence',
    icon: Repeat,
    view: 'workspace-settings-recurrence',
  },
  {
    id: 'templates',
    labelKey: 'workspaceSettings.tabs.templates',
    icon: FileStack,
    view: 'workspace-settings-templates',
  },
  {
    id: 'canned-responses',
    labelKey: 'workspaceSettings.tabs.cannedResponses',
    icon: MessageSquareText,
    view: 'workspace-settings-canned-responses',
  },
  {
    id: 'service-levels',
    labelKey: 'workspaceSettings.tabs.serviceLevels',
    icon: Clock,
    view: 'workspace-settings-service-levels',
  },
  {
    id: 'danger',
    labelKey: 'workspaceSettings.tabs.removeWorkspace',
    icon: Trash,
    view: 'workspace-settings-danger',
    danger: true,
  },
];

/** All route view names that belong to the workspace admin area. */
export const workspaceSettingsViews = [
  'workspace-settings',
  ...workspaceSettingsItems.map((item) => item.view),
];

/** Settings modules visible to the current user in a workspace. Modules with
 * a `permission` require that workspace permission; the rest only need
 * workspace.admin (the settings area's own gate). */
export function visibleWorkspaceSettingsItems(workspaceId, hasPermission) {
  return workspaceSettingsItems.filter(
    (item) => !item.permission || hasPermission(workspaceId, item.permission)
  );
}

/** Build the route for a settings module. */
export function workspaceSettingsRoute(workspaceId, id) {
  return `/workspaces/${workspaceId}/settings/${id}`;
}
