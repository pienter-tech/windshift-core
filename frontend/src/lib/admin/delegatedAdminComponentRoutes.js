// Lazy loaders for the delegated-admin surfaces. Keys mirror the item ids in
// navigation/delegatedAdminNavigation.js.
export const DELEGATED_ADMIN_COMPONENT_LOADERS = {
  channels: () => import('../features/channels/ManagerChannels.svelte'),
  assets: () => import('../features/assets/AssetManager.svelte'),
};
