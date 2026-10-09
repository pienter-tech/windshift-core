<script>
	import { onMount } from 'svelte';
	import { FileText, Trash2 } from '@lucide/svelte';
	import { api } from '../../api.js';
	import AlertBox from '../../components/AlertBox.svelte';
	import StateDisplay from '../../components/StateDisplay.svelte';
	import LinkComponent from '../../components/Link.svelte';
	import PagePicker from '../../pickers/PagePicker.svelte';
	import { errorToast } from '../../stores/toasts.svelte.js';
	import { t } from '../../stores/i18n.svelte.js';

	// Pages linked to a workspace milestone. Global milestones have
	// no workspace and therefore no Pages section. Anyone who can view the
	// milestone sees the linked pages they may view; users with edit rights on
	// the milestone (canEdit) link pages from its workspace and unlink them.
	let { milestoneId, workspaceId, canEdit = false } = $props();

	let links = $state([]);
	let loading = $state(true);
	let error = $state('');
	let selectedPageId = $state(null);
	let linking = $state(false);

	const linkedPageIds = $derived(new Set(links.map((link) => link.page_id)));

	onMount(() => {
		loadLinks();
	});

	async function loadLinks() {
		loading = true;
		error = '';
		try {
			links = (await api.milestones.getPageLinks(milestoneId)) || [];
		} catch (err) {
			console.error('Failed to load milestone page links:', err);
			error = t('dialogs.alerts.failedToLoad', { error: t('items.linkedPages') });
			links = [];
		} finally {
			loading = false;
		}
	}

	async function linkPage(page) {
		if (!page || linking) return;
		if (linkedPageIds.has(page.id)) {
			selectedPageId = null;
			return;
		}
		linking = true;
		try {
			const created = await api.milestones.linkPage(milestoneId, page.id);
			links = [...links.filter((link) => link.id !== created.id), created];
		} catch (err) {
			console.error('Failed to link page to milestone:', err);
			errorToast(err?.message || String(err), t('errors.failedToUpdate'));
		} finally {
			selectedPageId = null;
			linking = false;
		}
	}

	async function unlinkPage(linkId) {
		try {
			await api.milestones.unlinkPage(milestoneId, linkId);
			links = links.filter((link) => link.id !== linkId);
		} catch (err) {
			console.error('Failed to unlink page from milestone:', err);
			errorToast(err?.message || String(err), t('errors.failedToUpdate'));
		}
	}
</script>

<section
	class="rounded-xl border p-6 mt-6"
	style="background-color: var(--ds-surface-raised); border-color: var(--ds-border);"
	data-testid="milestone-pages"
>
	<h2 class="text-lg font-semibold mb-4 flex items-center gap-2" style="color: var(--ds-text);">
		<FileText class="w-4 h-4" style="color: var(--ds-text-subtle);" />
		{t('items.linkedPages')}
		{#if links.length > 0}
			<span class="text-sm font-normal" style="color: var(--ds-text-subtle);">({links.length})</span>
		{/if}
	</h2>

	{#if error}
		<AlertBox variant="error" message={error} class="mb-4" />
	{/if}

	{#if loading}
		<StateDisplay type="loading" />
	{:else}
		{#if links.length === 0 && !error}
			<p class="text-sm" style="color: var(--ds-text-subtle);">{t('milestones.noPagesLinked')}</p>
		{/if}

		{#if links.length > 0}
			<div class="space-y-2">
				{#each links as link (link.id)}
					{@const pageHref = `/workspaces/${link.workspace_id || workspaceId}/pages/${link.page_id}`}
					<div
						class="group flex items-center justify-between px-4 py-3 rounded-lg border"
						style="background-color: var(--ds-surface); border-color: var(--ds-border);"
						data-testid="milestone-page-row"
						data-link-id={link.id}
						data-page-id={link.page_id}
					>
						<div class="flex items-center gap-3 flex-1 min-w-0">
							<div
								class="w-6 h-6 rounded-full flex items-center justify-center flex-shrink-0"
								style="background-color: var(--ds-background-neutral); color: var(--ds-text-subtle);"
							>
								<FileText class="w-3.5 h-3.5" />
							</div>
							<LinkComponent
								href={pageHref}
								class="text-sm hover:text-ds-text-link cursor-pointer truncate"
								style="color: var(--ds-text);"
							>
								{link.page_title || t('pages.untitled')}
							</LinkComponent>
						</div>
						{#if canEdit}
							<button
								type="button"
								data-testid="milestone-page-unlink"
								class="p-1 rounded cursor-pointer opacity-0 group-hover:opacity-100 focus:opacity-100 transition-opacity hover:text-[var(--ds-text-danger)]"
								style="color: var(--ds-text-subtle);"
								onclick={() => unlinkPage(link.id)}
								title={t('items.removeLink')}
								aria-label={t('items.removeLink')}
							>
								<Trash2 class="w-4 h-4" />
							</button>
						{/if}
					</div>
				{/each}
			</div>
		{/if}

		{#if canEdit}
			<div class="mt-4 max-w-md">
				<PagePicker
					id="milestone-page-picker"
					{workspaceId}
					bind:value={selectedPageId}
					placeholder={t('items.pagePickerPlaceholder')}
					disabled={linking}
					inputTestid="milestone-page-picker"
					onSelect={linkPage}
				/>
			</div>
		{/if}
	{/if}
</section>
