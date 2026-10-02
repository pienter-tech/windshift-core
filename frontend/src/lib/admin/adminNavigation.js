import {
  IconActivity,
  IconAlertCircle,
  IconArrowsShuffle,
  IconBarrierBlock,
  IconBell,
  IconBolt,
  IconCloudDownload,
  IconDatabase,
  IconFileText,
  IconGitBranch,
  IconGitMerge,
  IconHierarchy2,
  IconKey,
  IconLayout,
  IconLayoutGrid,
  IconLifebuoy,
  IconLink,
  IconMail,
  IconMessage,
  IconPackage,
  IconPalette,
  IconPaperclip,
  IconPlug,
  IconPuzzle,
  IconRosetteDiscountCheck,
  IconRubberStamp,
  IconSettings,
  IconSettings2,
  IconShield,
  IconShieldLock,
  IconSparkles,
  IconStack2,
  IconUserCheck,
  IconUserStar,
  IconUsers,
} from '@tabler/icons-svelte-runes';

/**
 * @typedef {Object} AdminTab
 * @property {string} id
 * @property {string} labelKey                 i18n key for the tab label.
 * @property {string} [labelFallback]          Optional fallback string passed to t().
 * @property {string} [descriptionKey]
 * @property {string} [descriptionFallback]
 * @property {any}    icon
 */

/**
 * @typedef {Object} AdminGroup
 * @property {string}      id
 * @property {string}      labelKey
 * @property {any}         icon
 * @property {AdminTab[]}  items
 */

/** @type {AdminGroup[]} */
export const adminGroups = [
  {
    id: 'content-structure',
    labelKey: 'settings.adminGroups.contentStructure',
    icon: IconStack2,
    items: [
      {
        id: 'custom-fields',
        labelKey: 'settings.adminItems.customFields.title',
        icon: IconDatabase,
        descriptionKey: 'settings.adminItems.customFields.description',
      },
      {
        id: 'screens',
        labelKey: 'settings.adminItems.screens.title',
        icon: IconLayout,
        descriptionKey: 'settings.adminItems.screens.description',
      },
      {
        id: 'hierarchy-levels',
        labelKey: 'settings.adminItems.hierarchyLevels.title',
        icon: IconHierarchy2,
        descriptionKey: 'settings.adminItems.hierarchyLevels.description',
      },
      {
        id: 'item-types',
        labelKey: 'settings.adminItems.itemTypes.title',
        icon: IconFileText,
        descriptionKey: 'settings.adminItems.itemTypes.description',
      },
      {
        id: 'priorities',
        labelKey: 'settings.adminItems.priorities.title',
        icon: IconAlertCircle,
        descriptionKey: 'settings.adminItems.priorities.description',
      },
      {
        id: 'configuration-sets',
        labelKey: 'settings.adminItems.configurationSets.title',
        icon: IconSettings,
        descriptionKey: 'settings.adminItems.configurationSets.description',
      },
    ],
  },
  {
    id: 'workflow-process',
    labelKey: 'settings.adminGroups.workflowProcess',
    icon: IconSettings2,
    items: [
      {
        id: 'statuses',
        labelKey: 'settings.adminItems.statuses.title',
        icon: IconGitBranch,
        descriptionKey: 'settings.adminItems.statuses.description',
      },
      {
        id: 'workflows',
        labelKey: 'settings.adminItems.workflows.title',
        icon: IconArrowsShuffle,
        descriptionKey: 'settings.adminItems.workflows.description',
      },
      {
        id: 'condition-sets',
        labelKey: 'settings.adminItems.conditionSets.title',
        icon: IconBarrierBlock,
        descriptionKey: 'settings.adminItems.conditionSets.description',
      },
      {
        id: 'approval-sets',
        labelKey: 'settings.adminItems.approvalSets.title',
        icon: IconRubberStamp,
        descriptionKey: 'settings.adminItems.approvalSets.description',
      },
    ],
  },
  {
    id: 'integration-links',
    labelKey: 'settings.adminGroups.integrationLinks',
    icon: IconPlug,
    items: [
      {
        id: 'llm-connections',
        labelKey: 'settings.adminItems.llmConnections.title',
        icon: IconSparkles,
        descriptionKey: 'settings.adminItems.llmConnections.description',
      },
      {
        id: 'action-capabilities',
        labelKey: 'settings.adminItems.actionCapabilities.title',
        icon: IconBolt,
        descriptionKey: 'settings.adminItems.actionCapabilities.description',
      },
      {
        id: 'scm-providers',
        labelKey: 'settings.adminItems.scmProviders.title',
        icon: IconGitMerge,
        descriptionKey: 'settings.adminItems.scmProviders.description',
      },
      {
        id: 'integration-providers',
        labelKey: 'settings.adminItems.integrationProviders.title',
        labelFallback: 'Integrations',
        icon: IconPlug,
        descriptionFallback:
          'Manage outbound integrations Windshift connects to and inbound apps that authorize via OAuth.',
      },
      {
        id: 'system-import',
        labelKey: 'settings.adminItems.systemImport.title',
        icon: IconCloudDownload,
        descriptionKey: 'settings.adminItems.systemImport.description',
      },
      {
        id: 'link-types',
        labelKey: 'settings.adminItems.linkTypes.title',
        icon: IconLink,
        descriptionKey: 'settings.adminItems.linkTypes.description',
      },
      {
        id: 'attachments',
        labelKey: 'settings.adminItems.attachments.title',
        icon: IconPaperclip,
        descriptionKey: 'settings.adminItems.attachments.description',
      },
      {
        id: 'modules',
        labelKey: 'settings.adminItems.modules.title',
        icon: IconPuzzle,
        descriptionKey: 'settings.adminItems.modules.description',
      },
      {
        id: 'themes',
        labelKey: 'settings.adminItems.themes.title',
        icon: IconPalette,
        descriptionKey: 'settings.adminItems.themes.description',
      },
    ],
  },
  {
    id: 'users-access',
    labelKey: 'settings.adminGroups.usersAccess',
    icon: IconUserCheck,
    items: [
      {
        id: 'users',
        labelKey: 'settings.adminItems.users.title',
        icon: IconUsers,
        descriptionKey: 'settings.adminItems.users.description',
      },
      {
        id: 'groups',
        labelKey: 'settings.adminItems.groups.title',
        icon: IconUserStar,
        descriptionKey: 'settings.adminItems.groups.description',
      },
      {
        id: 'permissions',
        labelKey: 'settings.adminItems.permissions.title',
        icon: IconShield,
        descriptionKey: 'settings.adminItems.permissions.description',
      },
      {
        id: 'workspace-roles',
        labelKey: 'settings.adminItems.workspaceRoles.title',
        icon: IconRosetteDiscountCheck,
        descriptionKey: 'settings.adminItems.workspaceRoles.description',
      },
      {
        id: 'sso',
        labelKey: 'settings.adminItems.sso.title',
        icon: IconKey,
        descriptionKey: 'settings.adminItems.sso.description',
      },
      {
        id: 'security',
        labelKey: 'settings.adminItems.security.title',
        icon: IconShieldLock,
        descriptionKey: 'settings.adminItems.security.description',
      },
      {
        id: 'workspaces',
        labelKey: 'settings.adminItems.workspaces.title',
        icon: IconLayoutGrid,
        descriptionKey: 'settings.adminItems.workspaces.description',
      },
    ],
  },
  {
    id: 'communication',
    labelKey: 'settings.adminGroups.communication',
    icon: IconMessage,
    items: [
      {
        id: 'channels',
        labelKey: 'settings.adminItems.channels.title',
        labelFallback: 'Channels',
        icon: IconLifebuoy,
        descriptionKey: 'settings.adminItems.channels.description',
        descriptionFallback: 'Configure inbound and outbound channels, portals, and webhooks',
      },
      {
        id: 'notification-settings',
        labelKey: 'settings.adminItems.notificationSettings.title',
        icon: IconBell,
        descriptionKey: 'settings.adminItems.notificationSettings.description',
      },
      {
        id: 'email-templates',
        labelKey: 'settings.adminItems.emailTemplates.title',
        labelFallback: 'Email Templates',
        icon: IconMail,
        descriptionKey: 'settings.adminItems.emailTemplates.description',
        descriptionFallback: 'Customize the subject and body of transactional emails',
      },
    ],
  },
  {
    id: 'asset-management',
    labelKey: 'settings.adminGroups.assetManagement',
    icon: IconPackage,
    items: [
      {
        id: 'assets',
        labelKey: 'settings.adminItems.assets.title',
        icon: IconPackage,
        descriptionKey: 'settings.adminItems.assets.description',
      },
    ],
  },
  {
    id: 'system',
    labelKey: 'settings.adminGroups.system',
    icon: IconActivity,
    items: [
      {
        id: 'diagnostics',
        labelKey: 'settings.adminItems.diagnostics.title',
        icon: IconActivity,
        descriptionKey: 'settings.adminItems.diagnostics.description',
      },
      {
        id: 'packs',
        labelKey: 'settings.adminItems.packs.title',
        icon: IconPackage,
        descriptionKey: 'settings.adminItems.packs.description',
      },
    ],
  },
];

/**
 * Resolve labels/descriptions using a t() function. Returns a new structure
 * with .label and .description filled in. Return type is `any[]` because
 * downstream code merges plugin-provided items that carry additional fields
 * (isPlugin, pluginData) outside this registry's schema.
 *
 * @param {(key:string, fallback?:string) => string} t
 * @returns {any[]}
 */
export function resolveAdminGroups(t) {
  return adminGroups.map((g) => ({
    id: g.id,
    label: t(g.labelKey),
    icon: g.icon,
    items: g.items.map((it) => ({
      id: it.id,
      label: it.labelFallback ? t(it.labelKey, it.labelFallback) : t(it.labelKey),
      description: it.descriptionFallback
        ? t(it.descriptionKey || '', it.descriptionFallback)
        : it.descriptionKey
          ? t(it.descriptionKey)
          : '',
      icon: it.icon,
    })),
  }));
}
