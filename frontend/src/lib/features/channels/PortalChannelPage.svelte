<script>
  import { onMount } from 'svelte';
  import { IconSettings, IconUsers } from '@tabler/icons-svelte-runes';
  import { currentRoute } from '../../router.js';
  import { api } from '../../api.js';
  import { t } from '../../stores/i18n.svelte.js';
  import { errorToast, successToast } from '../../stores/toasts.svelte.js';
  import { isSystemAdmin } from '../../stores/permissions.svelte.js';
  import { channelCategoriesStore } from '../../stores/channelCategories.js';
  import ChannelAdminShell from './ChannelAdminShell.svelte';
  import ChannelAdminSettings from './ChannelAdminSettings.svelte';
  import ChannelPortalConfig from './ChannelPortalConfig.svelte';
  import ChannelManagersTab from '../../settings/ChannelManagersTab.svelte';
  import { channelBasicFormData, parseChannelConfig, saveChannelSettings } from './channelAdmin.js';

  let channel = $state(null);
  let loading = $state(true);
  let saving = $state(false);
  let activeTab = $state('settings');
  let loadSequence = 0;

  let portalConfigRef = $state(null);

  let channelFormData = $state({
    name: '',
    description: '',
    category_id: null,
  });

  let portalFormData = $state({
    slug: '',
    workspace_ids: [],
    enabled: false,
    title: '',
    description: '',
    registration_mode: 'open',
    allowed_domains: '',
  });

  // Email channels whose email_connected_portal_id points at this portal.
  let connectedMailboxes = $state([]);

  let channelId = $derived(parseInt($currentRoute.path.match(/\/admin\/channels\/(\d+)\/portal/)?.[1]));

  onMount(async () => {
    await channelCategoriesStore.init();
  });

  $effect(() => {
    const id = channelId;
    if (Number.isInteger(id) && id > 0) void loadChannel(id);
  });

  async function loadChannel(id) {
    const requestSequence = ++loadSequence;
    try {
      loading = true;
      const loadedChannel = await api.channels.get(id);
      if (loadedChannel.type !== 'portal' || loadedChannel.direction !== 'inbound') {
        throw new Error('This route requires an inbound portal channel');
      }
      const config = parseChannelConfig(loadedChannel.config);
      if (requestSequence !== loadSequence) return;
      channel = loadedChannel;

      channelFormData = channelBasicFormData(channel);
      portalFormData = {
        slug: config.portal_slug || '',
        workspace_ids: config.portal_workspace_ids || [],
        enabled: channel.status === 'enabled',
        title: config.portal_title || '',
        description: config.portal_description || '',
        registration_mode: config.portal_registration_mode === 'manual' ? 'manual' : 'open',
        allowed_domains: Array.isArray(config.portal_allowed_domains)
          ? config.portal_allowed_domains.join(', ')
          : '',
      };
      await loadConnectedMailboxes(id, requestSequence);
    } catch (err) {
      if (requestSequence !== loadSequence) return;
      console.error('Failed to load channel:', err);
      channel = null;
      errorToast(err.message || t('channel.failedToLoad', 'Failed to load channel'));
    } finally {
      if (requestSequence === loadSequence) loading = false;
    }
  }

  // Email channels linked to this portal, derived from each channel's
  // email_connected_portal_id config. Best-effort: a load failure leaves the
  // previous (or empty) list rather than blocking the settings page.
  async function loadConnectedMailboxes(portalId, requestSequence) {
    try {
      const emailChannels = await api.channels.getAll({
        type: 'email',
        direction: 'inbound',
        include_disabled: true,
      });
      if (requestSequence !== loadSequence) return;
      connectedMailboxes = emailChannels.filter((ch) => {
        const chConfig = parseChannelConfig(ch.config) || {};
        return chConfig.email_connected_portal_id === portalId;
      });
    } catch (err) {
      console.error('Failed to load connected mailboxes:', err);
      if (requestSequence === loadSequence) connectedMailboxes = [];
    }
  }

  async function handleSaveSettings() {
    if (!channel) return;

    if (portalConfigRef) {
      const validation = portalConfigRef.validate();
      if (!validation.valid) {
        errorToast(validation.message);
        return;
      }
    }

    try {
      saving = true;

      await saveChannelSettings({
        channel,
        channelFormData,
        configRef: portalConfigRef,
        enabled: portalFormData.enabled,
      });

      await loadChannel(channelId);
      successToast(t('common.saved'));
    } catch (err) {
      console.error('Failed to save:', err);
      errorToast(err.message || t('common.error'));
    } finally {
      saving = false;
    }
  }

  let tabs = $derived([
    { id: 'settings', label: () => t('channel.configuration'), icon: IconSettings },
    ...($isSystemAdmin
      ? [{ id: 'managers', label: () => t('channel.managers'), icon: IconUsers }]
      : []),
  ]);
</script>

<ChannelAdminShell
  {loading}
  {channel}
  bind:activeTab
  {tabs}
  subtitle={t('channels.portal', 'Portal Channel')}
  openUrl={portalFormData.slug ? `/portal/${portalFormData.slug}` : ''}
  openLabel={t('channel.openPortal')}
>
  {#snippet children(tabId)}
    {#if tabId === 'settings'}
      <ChannelAdminSettings bind:channelFormData {saving} onSave={handleSaveSettings}>
        <ChannelPortalConfig
          bind:this={portalConfigRef}
          bind:formData={portalFormData}
          {connectedMailboxes}
        />
      </ChannelAdminSettings>
    {:else if tabId === 'managers' && $isSystemAdmin}
      <div class="px-16 py-8">
        <ChannelManagersTab
          channelId={channel.id}
          channelName={channel.name}
          isDefault={channel.is_default}
        />
      </div>
    {/if}
  {/snippet}
</ChannelAdminShell>
