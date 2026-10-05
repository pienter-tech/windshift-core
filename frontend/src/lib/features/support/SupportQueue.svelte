<script>
  import { onMount } from 'svelte';
  import { t } from '../../stores/i18n.svelte.js';
  import { api } from '../../api.js';
  import { fetchV2Data } from '../../api/core.js';
  import { errorToast, successToast } from '../../stores/toasts.svelte.js';
  import { authStore, workspaceDataStore } from '../../stores';
  import { navigate } from '../../router.js';
  import { confirm } from '../../composables/useConfirm.js';
  import { ArrowLeft, ArrowRight, EyeOff, MoreHorizontal, Pencil, Plus, Trash2 } from '@lucide/svelte';
  import Button from '../../components/Button.svelte';
  import Input from '../../components/Input.svelte';
  import Modal from '../../dialogs/Modal.svelte';
  import DialogFooter from '../../dialogs/DialogFooter.svelte';
  import { createDeleteItemHandler, createItemActionsBuilder } from '../../utils/workItemTableHelpers.js';
  import { getListColumnLabel, listGridMinWidth, listGridTemplateColumns } from '../../utils/workItemListColumns.js';
  import { useGradientStyles } from '../../stores/workspaceGradient.svelte.js';
  import { workspacePermissions } from '../../stores/workspacePermissions.svelte.js';
  import { collectionEditorOptions } from '../../stores/collectionEditorOptions.svelte.js';
  import ViewHeader from '../../layout/ViewHeader.svelte';
  import StaticViewBackground from '../../layout/StaticViewBackground.svelte';
  import EmptyState from '../../components/EmptyState.svelte';
  import Spinner from '../../components/Spinner.svelte';
  import SearchInput from '../../components/SearchInput.svelte';
  import TableHeaderBar from '../../components/TableHeaderBar.svelte';
  import DropdownMenu from '../../layout/DropdownMenu.svelte';
  import LazyRender from '../../components/LazyRender.svelte';
  import ListCellRenderer from '../collections/ListCellRenderer.svelte';
  import QlFilterBuilder from '../shared/QlFilterBuilder.svelte';

  let { workspaceId, collectionId = null, queue = null } = $props();

  // Resolved queue catalog for the current scope (built-ins + custom).
  let queues = $state([]);
  let activeRef = $state('');
  let loadingQueues = $state(true);

  // Ticket rows for the active queue.
  let items = $state([]);
  let loadingItems = $state(false);
  let itemsTruncated = $state(false);
  let itemsLoadId = 0;

  // Bulk selection state.
  let selectedIds = $state(new Set());
  let bulkRunning = $state(false);
  let bulkTeamId = $state('');
  let teams = $state([]);

  // Client-side search over the loaded rows, matching the list view.
  let searchQuery = $state('');

  // Queue authoring dialog.
  let editorOpen = $state(false);
  let editorQueueId = $state(null);
  let editorName = $state('');
  let editorQl = $state('');
  let editorFilterState = $state(null);
  let editorSaving = $state(false);

  // Collection metadata gates management in a collection context; workspace
  // scope mirrors the board-configuration admin gate.
  let collection = $state(null);

  // Reference data from the shared workspace store.
  let workspace = $derived(workspaceDataStore.workspace);
  let users = $derived(workspaceDataStore.users ?? []);
  let statuses = $derived(workspaceDataStore.statuses ?? []);
  let statusCategories = $derived(workspaceDataStore.statusCategories ?? []);
  let priorities = $derived(workspaceDataStore.priorities ?? []);
  let milestones = $derived(workspaceDataStore.milestones ?? []);
  let iterations = $derived(workspaceDataStore.iterations ?? []);
  let projects = $derived(workspaceDataStore.projects ?? []);
  let itemTypes = $derived(workspaceDataStore.itemTypes ?? []);
  let customFieldDefinitions = $derived(workspaceDataStore.customFieldDefinitions ?? []);

  let activeQueue = $derived(queues.find((entry) => queueRef(entry) === activeRef) ?? null);
  let activeQueueCount = $derived(activeQueue?.count ?? 0);
  let allSelected = $derived(items.length > 0 && items.every((item) => selectedIds.has(item.id)));
  let currentUser = $derived(authStore.currentUser);
  let canEdit = $derived(workspacePermissions.canEdit(workspaceId));
  let canManage = $derived.by(() => {
    if (collectionId) {
      return Boolean(
        collection &&
          collection.created_by != null &&
          String(collection.created_by) === String(currentUser?.id)
      );
    }
    return workspacePermissions.canAdminWorkspace(workspaceId);
  });

  // The queue renders list-view rows, so its columns reuse the list column
  // shape. The set is fixed: queues are workspace-level triage views, not
  // configurable boards. Assignee and updated keep the queue's own labels.
  const QUEUE_COLUMNS = [
    { field_identifier: 'key', field_type: 'system', display_order: 0, width: 1 },
    { field_identifier: 'title', field_type: 'system', display_order: 1, width: 4 },
    { field_identifier: 'status', field_type: 'system', display_order: 2, width: 2 },
    { field_identifier: 'priority', field_type: 'system', display_order: 3, width: 2 },
    { field_identifier: 'assignee', field_type: 'system', display_order: 4, width: 2 },
    { field_identifier: 'due_date', field_type: 'system', display_order: 5, width: 2 },
    { field_identifier: 'updated_at', field_type: 'system', display_order: 6, width: 2 },
  ];
  const QUEUE_COLUMN_LABEL_OVERRIDES = {
    assignee: 'supportQueue.columnOwner',
    updated_at: 'supportQueue.columnUpdated',
  };

  function columnLabel(column) {
    const override = QUEUE_COLUMN_LABEL_OVERRIDES[column.field_identifier];
    return override ? t(override) : getListColumnLabel(column, customFieldDefinitions);
  }

  // A leading selection track sits in front of the shared list grid tracks.
  const SELECT_TRACK = '2rem';
  let gridTemplateColumns = $derived(`${SELECT_TRACK} ${listGridTemplateColumns(QUEUE_COLUMNS)}`);
  let gridMinWidth = $derived(`calc(${SELECT_TRACK} + ${listGridMinWidth(QUEUE_COLUMNS)})`);

  const styles = useGradientStyles();

  const QUEUE_PAGE_SIZE = 100;

  let filteredItems = $derived.by(() => {
    if (!searchQuery.trim()) return items;
    const query = searchQuery.toLowerCase();
    return items.filter((item) => {
      if (item.title.toLowerCase().includes(query)) return true;
      const itemKey = `${item.workspace_key || ''}-${item.workspace_item_number}`.toLowerCase();
      return itemKey.includes(query);
    });
  });

  // Built-in presets are addressed by their stable key; custom queues by id.
  function queueRef(entry) {
    return entry.builtin ? entry.key : String(entry.id);
  }

  function queuesPath() {
    if (collectionId) return `/collections/${collectionId}/queues`;
    const key = workspaceDataStore.workspace?.key;
    return key ? `/workspaces/${key}/queues` : null;
  }

  // A workspace-scoped list already has these option sets in the shared
  // workspace store. Prime the row-editor cache so opening a cell does not
  // repeat those requests, exactly like the list view does.
  $effect(() => {
    const id = Number(workspaceId);
    if (
      !Number.isInteger(id) ||
      id <= 0 ||
      !workspaceDataStore.initialized ||
      Number(workspaceDataStore.workspaceId) !== id
    ) {
      return;
    }

    collectionEditorOptions.prime(id, {
      statuses,
      users,
      milestones,
      iterations,
      projects,
    });
  });

  async function loadCollection() {
    if (!collectionId) {
      collection = null;
      return;
    }
    try {
      collection = await api.collections.get(collectionId);
    } catch (error) {
      collection = null;
    }
  }

  async function loadQueues() {
    try {
      await workspaceDataStore.initialize(workspaceId);
      const path = queuesPath();
      if (!path) throw new Error('workspace bootstrap unavailable');
      queues = (await fetchV2Data(path)) ?? [];
      resolveActiveRef();
    } catch (error) {
      errorToast(t('supportQueue.loadFailed'));
    } finally {
      loadingQueues = false;
    }
  }

  // Keep the selected queue when it still exists; otherwise honor the deep
  // link, then fall back to the unassigned preset, then the first queue.
  function resolveActiveRef() {
    const refs = queues.map(queueRef);
    if (activeRef && refs.includes(activeRef)) return;
    if (queue && refs.includes(queue)) {
      activeRef = queue;
      return;
    }
    activeRef = refs.includes('unassigned') ? 'unassigned' : (refs[0] ?? '');
  }

  async function loadItems() {
    if (!activeQueue?.ql) {
      items = [];
      return;
    }
    const loadId = ++itemsLoadId;
    loadingItems = true;
    try {
      const query = new URLSearchParams({
        workspace_id: String(workspaceId),
        ql: activeQueue.ql,
        fields: 'summary',
        page_size: String(QUEUE_PAGE_SIZE),
        sort: '-updated_at',
      });
      const result = (await fetchV2Data(`/items?${query.toString()}`)) ?? [];
      if (loadId !== itemsLoadId) return;
      items = result;
      itemsTruncated = activeQueueCount > items.length;
      selectedIds = new Set(
        [...selectedIds].filter((id) => items.some((item) => item.id === id))
      );
    } catch (error) {
      if (loadId !== itemsLoadId) return;
      items = [];
      errorToast(t('supportQueue.loadFailed'));
    } finally {
      if (loadId === itemsLoadId) loadingItems = false;
    }
  }

  async function refreshQueuesAndItems() {
    await loadQueues();
    await loadItems();
  }

  function selectQueue(ref) {
    if (ref === activeRef) return;
    activeRef = ref;
    selectedIds = new Set();
    searchQuery = '';
    const url = new URL(window.location.href);
    url.searchParams.set('queue', ref);
    window.history.replaceState(null, '', `${url.pathname}${url.search}`);
    void loadItems();
  }

  function toggleItem(id) {
    const next = new Set(selectedIds);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    selectedIds = next;
  }

  function toggleAll() {
    selectedIds = allSelected ? new Set() : new Set(items.map((item) => item.id));
  }

  function clearSelection() {
    selectedIds = new Set();
  }

  async function runBulkUpdate(fields) {
    const ids = [...selectedIds];
    if (ids.length === 0 || bulkRunning) return;
    bulkRunning = true;
    try {
      await api.items.bulkUpdate(ids, fields);
      successToast(t('supportQueue.bulkSuccess', { n: ids.length }));
      clearSelection();
      await refreshQueuesAndItems();
    } catch (error) {
      errorToast(error?.message || t('supportQueue.loadFailed'));
    } finally {
      bulkRunning = false;
    }
  }

  function bulkAssignToMe() {
    if (currentUser?.id == null) return;
    void runBulkUpdate({ assignee_id: currentUser.id });
  }

  function bulkAssignToTeam() {
    if (!bulkTeamId) return;
    void runBulkUpdate({ team_id: Number(bulkTeamId) });
  }

  function viewItem(item) {
    navigate(`/workspaces/${workspaceId}/items/${item.id}`);
  }

  // Single-cell edits replace the row in place; counts may drift until the
  // next bulk refresh, matching the list view's update behavior.
  function handleItemUpdated(data) {
    items = items.map((item) => (item.id === data.item.id ? { ...item, ...data.item } : item));
  }

  function handleUpdateError(data) {
    const { error, field, value } = data;
    console.error(`Failed to update ${field}:`, error);
    errorToast(t('dialogs.alerts.failedToUpdate', { error: `${field}: ${value ?? ''} ${error}` }));
  }

  const deleteItem = createDeleteItemHandler({
    confirmMessage: (item) => t('collections.confirmDeleteItem', { title: item.title }),
    onDeleted: () => refreshQueuesAndItems(),
  });

  const buildItemActions = createItemActionsBuilder({ viewItem, deleteItem });

  // ===== Queue management =====

  function openCreate() {
    editorQueueId = null;
    editorName = '';
    editorQl = '';
    editorFilterState = null;
    editorOpen = true;
  }

  function openEdit(entry) {
    editorQueueId = entry.id;
    editorName = entry.name;
    editorQl = entry.ql;
    editorFilterState = entry.filter_state ?? null;
    editorOpen = true;
  }

  function closeEditor() {
    if (editorSaving) return;
    editorOpen = false;
  }

  function handleEditorChange({ ql_query, filter_state }) {
    editorQl = ql_query;
    editorFilterState = filter_state;
  }

  async function saveEditor() {
    if (!editorName.trim() || !editorQl.trim() || editorSaving) return;
    editorSaving = true;
    const body = JSON.stringify({
      name: editorName.trim(),
      ql_query: editorQl,
      filter_state: editorFilterState,
    });
    try {
      if (editorQueueId) {
        await fetchV2Data(`/queues/${editorQueueId}`, { method: 'PATCH', body });
        successToast(t('supportQueue.updated'));
      } else {
        const path = queuesPath();
        await fetchV2Data(path, { method: 'POST', body });
        successToast(t('supportQueue.created'));
      }
      editorOpen = false;
      await refreshQueuesAndItems();
    } catch (error) {
      errorToast(error?.message || t('supportQueue.saveFailed'));
    } finally {
      editorSaving = false;
    }
  }

  async function removeQueue(entry) {
    const confirmed = await confirm({
      title: t('supportQueue.delete'),
      message: t('supportQueue.deleteConfirm', { name: entry.name }),
      confirmText: t('common.delete'),
      variant: 'danger',
    });
    if (!confirmed) return;
    try {
      await fetchV2Data(`/queues/${entry.id}`, { method: 'DELETE' });
      successToast(t('supportQueue.deleted'));
      await refreshQueuesAndItems();
    } catch (error) {
      errorToast(error?.message || t('supportQueue.saveFailed'));
    }
  }

  async function dismissBuiltin(entry) {
    const path = queuesPath();
    try {
      await fetchV2Data(`${path}/builtins/${encodeURIComponent(entry.key)}`, {
        method: 'PUT',
        body: JSON.stringify({ hidden: true }),
      });
      successToast(t('supportQueue.builtinRemoved'));
      await refreshQueuesAndItems();
    } catch (error) {
      errorToast(error?.message || t('supportQueue.saveFailed'));
    }
  }

  async function moveQueue(entry, direction) {
    const custom = queues.filter((item) => !item.builtin);
    const index = custom.findIndex((item) => item.id === entry.id);
    const target = direction === 'left' ? index - 1 : index + 1;
    if (index < 0 || target < 0 || target >= custom.length) return;
    const reordered = [...custom];
    [reordered[index], reordered[target]] = [reordered[target], reordered[index]];
    try {
      await fetchV2Data(`${queuesPath()}/order`, {
        method: 'PUT',
        body: JSON.stringify({ ids: reordered.map((item) => item.id) }),
      });
      await refreshQueuesAndItems();
    } catch (error) {
      errorToast(error?.message || t('supportQueue.saveFailed'));
    }
  }

  function queueMenuItems(entry) {
    if (entry.builtin) {
      return [
        {
          id: 'remove-builtin',
          type: 'regular',
          icon: EyeOff,
          title: t('supportQueue.removeBuiltin'),
          testid: `support-queue-remove-builtin-${entry.key}`,
          onClick: () => dismissBuiltin(entry),
        },
      ];
    }
    const custom = queues.filter((item) => !item.builtin);
    const index = custom.findIndex((item) => item.id === entry.id);
    return [
      {
        id: 'edit',
        type: 'regular',
        icon: Pencil,
        title: t('supportQueue.edit'),
        testid: 'support-queue-edit',
        onClick: () => openEdit(entry),
      },
      {
        id: 'move-left',
        type: 'regular',
        icon: ArrowLeft,
        title: t('supportQueue.moveLeft'),
        disabled: index <= 0,
        testid: 'support-queue-move-left',
        onClick: () => moveQueue(entry, 'left'),
      },
      {
        id: 'move-right',
        type: 'regular',
        icon: ArrowRight,
        title: t('supportQueue.moveRight'),
        disabled: index === custom.length - 1,
        testid: 'support-queue-move-right',
        onClick: () => moveQueue(entry, 'right'),
      },
      { type: 'divider' },
      {
        id: 'delete',
        type: 'regular',
        icon: Trash2,
        title: t('supportQueue.delete'),
        color: 'var(--ds-text-danger)',
        hoverClass: 'hover-danger',
        testid: 'support-queue-delete',
        onClick: () => removeQueue(entry),
      },
    ];
  }

  onMount(() => {
    void loadCollection();
    void loadQueues().then(loadItems);
    void workspaceDataStore.initialize(workspaceId);
    api.teams
      .getAll()
      .then((list) => {
        teams = list ?? [];
      })
      .catch(() => {
        teams = [];
      });
  });
</script>

<StaticViewBackground
  backgroundStyle={styles.backgroundStyle}
  contextVars={styles.contextVars}
  testid="support-queue-view"
>
  <div class="mb-6">
    <ViewHeader
      viewName={t('supportQueue.title')}
      itemCount={activeQueueCount}
      shownCount={loadingItems ? null : filteredItems.length}
    />
  </div>

  {#if loadingQueues}
    <div class="flex items-center gap-2 py-8" data-testid="support-queue-loading">
      <Spinner class="w-4 h-4" />
      <span style="color: var(--ctx-text-subtle, var(--ds-text-subtle));">{t('supportQueue.loading')}</span>
    </div>
  {:else}
    <div class="flex flex-wrap items-center justify-between gap-4 mb-4">
      <div class="flex flex-wrap items-center gap-2" role="tablist" data-testid="support-queue-tabs">
        {#each queues as entry (queueRef(entry))}
          <div
            class="flex items-center rounded-md border transition-colors"
            style={queueRef(entry) === activeRef
              ? 'color: var(--ctx-active-text, var(--ds-accent-blue)); background-color: var(--ctx-active-bg, var(--ds-accent-blue-subtler)); border-color: var(--ctx-border, var(--ds-border)); backdrop-filter: var(--ctx-backdrop, none);'
              : 'color: var(--ctx-text-subtle, var(--ds-text-subtle)); background-color: transparent; border-color: var(--ctx-border, var(--ds-border)); backdrop-filter: var(--ctx-backdrop, none);'}
          >
            <button
              type="button"
              role="tab"
              aria-selected={queueRef(entry) === activeRef}
              class="px-3 py-1.5 text-sm"
              data-testid={`support-queue-tab-${queueRef(entry)}`}
              onclick={() => selectQueue(queueRef(entry))}
            >
              {entry.name}
              <span
                class="ml-1.5 inline-block rounded-full text-xs px-1.5"
                style="background-color: var(--ctx-surface-overlay, var(--ds-surface-overlay)); color: var(--ds-text); backdrop-filter: var(--ctx-backdrop, none);"
                data-testid={`support-queue-count-${queueRef(entry)}`}
              >
                {entry.count}
              </span>
            </button>
            {#if canManage && queueRef(entry) === activeRef}
              <DropdownMenu
                triggerText=""
                triggerIcon={MoreHorizontal}
                iconOnly
                triggerClass="p-1.5 rounded action-btn transition-colors"
                triggerTestid={`support-queue-menu-${queueRef(entry)}`}
                items={queueMenuItems(entry)}
              />
            {/if}
          </div>
        {/each}

        {#if canManage}
          <!-- shortcut-guard-exempt: contextual queue-tab action; no global shortcut. -->
          <Button
            variant="ghost"
            size="small"
            icon={Plus}
            dataTestid="support-queue-add"
            onclick={openCreate}
          >
            {t('supportQueue.add')}
          </Button>
        {/if}
      </div>

      <SearchInput
        bind:value={searchQuery}
        placeholder={t('common.search')}
      />
    </div>

    {#if selectedIds.size > 0}
      <div
        class="flex flex-wrap items-center gap-2 mb-3 px-3 py-2 rounded-md border"
        style="border-color: var(--ctx-border, var(--ds-border)); background-color: var(--ctx-surface, var(--ds-background-neutral)); backdrop-filter: var(--ctx-backdrop, none);"
        data-testid="support-queue-bulk-bar"
      >
        <span class="text-sm" style="color: var(--ds-text);">{t('supportQueue.bulkBar', { n: selectedIds.size })}</span>
        <Button
          variant="ghost"
          size="small"
          style="border-color: var(--ctx-border, var(--ds-border)); color: var(--ds-text);"
          dataTestid="support-queue-bulk-assign-me"
          disabled={bulkRunning}
          onclick={bulkAssignToMe}
        >
          {t('supportQueue.bulkAssignMe')}
        </Button>
        <select
          class="px-2 py-1 rounded text-sm border transition-colors"
          style="border-color: var(--ctx-border, var(--ds-border)); background-color: var(--ctx-surface-overlay, var(--ds-surface-overlay)); color: var(--ds-text); backdrop-filter: var(--ctx-backdrop, none);"
          data-testid="support-queue-bulk-team-select"
          bind:value={bulkTeamId}
        >
          <option value="">{t('supportQueue.bulkAssignTeam')}</option>
          {#each teams as team (team.id)}
            <option value={String(team.id)}>{team.name}</option>
          {/each}
        </select>
        <Button
          variant="ghost"
          size="small"
          style="border-color: var(--ctx-border, var(--ds-border)); color: var(--ds-text);"
          dataTestid="support-queue-bulk-team-apply"
          disabled={bulkRunning || !bulkTeamId}
          onclick={bulkAssignToTeam}
        >
          {t('supportQueue.bulkApplyTeam')}
        </Button>
        <Button
          variant="ghost"
          size="small"
          style="color: var(--ds-text);"
          dataTestid="support-queue-bulk-clear"
          onclick={clearSelection}
        >
          {t('supportQueue.bulkClear')}
        </Button>
      </div>
    {/if}

    {#if loadingItems}
      <div class="flex items-center gap-2 py-8">
        <Spinner class="w-4 h-4" />
      </div>
    {:else if filteredItems.length === 0}
      <div data-testid="support-queue-empty">
        <EmptyState title={t('supportQueue.empty')} description={t('supportQueue.emptyDescription')} />
      </div>
    {:else}
      <div class="rounded-xl border shadow-sm overflow-x-auto" style="{styles.tableStyle(12)} border-color: var(--ctx-border, var(--ds-border));" data-testid="support-queue-table">
        <TableHeaderBar
          columns={gridTemplateColumns}
          minWidth={gridMinWidth}
          style={styles.tableHeaderStyle}
        >
          <div></div>
          {#each QUEUE_COLUMNS as column (column.field_identifier)}
            <div>{columnLabel(column)}</div>
          {/each}
          <div>{t('common.actions')}</div>
        </TableHeaderBar>

        <div>
          {#each filteredItems as item (item.id)}
            <div
              class="px-4 py-3 list-row transition-colors"
              style="border-top: 1px solid var(--ds-border);"
              data-item-row
              data-item-id={item.id}
              data-testid={`support-queue-row-${item.id}`}
            >
              <LazyRender>
                {#snippet children()}
                  <div
                    class="grid gap-4 items-center"
                    style="grid-template-columns: {gridTemplateColumns}; min-width: {gridMinWidth};"
                  >
                    <input
                      type="checkbox"
                      checked={selectedIds.has(item.id)}
                      aria-label={t('supportQueue.selectAll')}
                      data-testid={`support-queue-item-checkbox-${item.id}`}
                      onchange={() => toggleItem(item.id)}
                    />
                    {#each QUEUE_COLUMNS as column (column.field_identifier)}
                      <div class="min-w-0 overflow-hidden">
                        <ListCellRenderer
                          {item}
                          {column}
                          {workspace}
                          canEdit={canEdit && item.workspace_id === Number(workspaceId)}
                          {statuses}
                          {statusCategories}
                          {priorities}
                          {milestones}
                          {iterations}
                          {users}
                          {projects}
                          {itemTypes}
                          {customFieldDefinitions}
                          onitemUpdated={handleItemUpdated}
                          onupdateError={handleUpdateError}
                        />
                      </div>
                    {/each}

                    <!-- Actions -->
                    <div>
                      <DropdownMenu
                        triggerText=""
                        triggerIcon={MoreHorizontal}
                        triggerClass="p-2 rounded action-btn transition-colors"
                        items={buildItemActions(item)}
                      />
                    </div>
                  </div>
                {/snippet}
              </LazyRender>
            </div>
          {/each}
        </div>
      </div>

      {#if itemsTruncated}
        <p class="mt-2 text-xs" style="color: var(--ctx-text-subtle, var(--ds-text-subtle));">
          {t('supportQueue.showingFirst', { n: items.length })}
        </p>
      {/if}
    {/if}
  {/if}
</StaticViewBackground>

<Modal
  bind:isOpen={editorOpen}
  onclose={closeEditor}
  onSubmit={saveEditor}
  submitDisabled={!editorName.trim() || !editorQl.trim() || editorSaving}
  maxWidth="max-w-2xl"
>
  <div class="p-6" data-testid="support-queue-editor">
    <h2 class="text-lg font-semibold mb-4" style="color: var(--ds-text);">
      {editorQueueId ? t('supportQueue.edit') : t('supportQueue.add')}
    </h2>
    <label class="block text-sm font-medium mb-1" for="queue-editor-name" style="color: var(--ds-text-subtle);">
      {t('supportQueue.nameLabel')}
    </label>
    <Input
      id="queue-editor-name"
      dataTestid="queue-editor-name"
      bind:value={editorName}
      placeholder={t('supportQueue.namePlaceholder')}
      class="mb-4"
    />
    <span class="block text-sm font-medium mb-1" style="color: var(--ds-text-subtle);">
      {t('supportQueue.filterLabel')}
    </span>
    <QlFilterBuilder
      qlQuery={editorQl}
      filterState={editorFilterState}
      testIdPrefix="queue-editor-builder"
      onchange={handleEditorChange}
    />
    <DialogFooter
      onCancel={closeEditor}
      onConfirm={saveEditor}
      confirmLabel={t('common.save')}
      cancelTestid="queue-editor-cancel"
      confirmTestid="queue-editor-save"
      disabled={!editorName.trim() || !editorQl.trim()}
      loading={editorSaving}
      showKeyboardHint={true}
    />
  </div>
</Modal>

<style>
  .list-row:hover {
    background-color: var(--ctx-background-neutral-hovered, var(--ds-background-neutral-hovered));
  }
</style>
