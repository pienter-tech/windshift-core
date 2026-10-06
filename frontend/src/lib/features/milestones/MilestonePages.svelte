<script>
	import { onMount, tick } from 'svelte';
	import { FileText, Plus, Trash2 } from '@lucide/svelte';
	import { api } from '../../api.js';
	import AlertBox from '../../components/AlertBox.svelte';
	import LinkComponent from '../../components/Link.svelte';
	import PagePicker from '../../pickers/PagePicker.svelte';
	import { errorToast } from '../../stores/toasts.svelte.js';
	import { t } from '../../stores/i18n.svelte.js';

	// Pages linked to a workspace milestone (WCORE-19), shown as a list in the
	// milestone header card (WCORE-46). Global milestones have no workspace and
	// therefore no Pages section. Anyone who can view the milestone sees the
	// linked pages they may view; users with edit rights on the milestone
	// (canEdit) link pages from its workspace through "+ Add" and unlink them.
	let { milestoneId, workspaceId, canEdit = false } = $props();

	let links = $state([]);
	let loading = $state(true);
	let error = $state('');
	let selectedPageId = $state(null);
	let linking = $state(false);
	let showPicker = $state(false);
	let addButton = $state(null);

	const linkedPageIds = $derived(new Set(links.map((link) => link.page_id)));
	// Editors always see the heading and "+ Add"; viewers only once pages (or a
	// load error) are there to show.
	const visible = $derived(canEdit || (!loading && (links.length > 0 || !!error)));

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
			closePicker();
			return;
		}
		linking = true;
		try {
			const created = await api.milestones.linkPage(milestoneId, page.id);
			links = [...links.filter((link) => link.id !== created.id), created];
			closePicker();
		} catch (err) {
			console.error('Failed to link page to milestone:', err);
			errorToast(err?.message || String(err), t('errors.failedToUpdate'));
		} finally {
			selectedPageId = null;
			linking = false;
		}
	}

	async function togglePicker() {
		if (showPicker) {
			showPicker = false;
			return;
		}
		showPicker = true;
		await tick();
		document.getElementById('milestone-page-picker')?.focus();
	}

	function closePicker() {
		showPicker = false;
		addButton?.focus();
	}

	function handlePickerKeydown(event) {
		if (event.key === 'Escape') closePicker();
	}

	async function unlinkPage(linkId) {
		try {
			await api.milestones.unlinkPage(milestoneId, linkId);
			links = links.filter((link) => link.id !== linkId);
			// The focused remove button is gone; keep keyboard focus in the section.
			addButton?.focus();
		} catch (err) {
			console.error('Failed to unlink page from milestone:', err);
			errorToast(err?.message || String(err), t('errors.failedToUpdate'));
		}
	}
</script>

<!-- Compact list inside the milestone header card (WCORE-46), styled like
     the item detail Pages section. Viewers only see it when pages are linked;
     editors always get the heading and "+ Add". -->
{#if visible}
	<section class="mt-6" data-testid="milestone-pages" aria-labelledby="milestone-pages-heading">
		<div class="flex items-center justify-between mb-3">
			<div class="flex items-center gap-2">
				<FileText class="w-4 h-4" style="color: var(--ds-text-subtle);" />
				<h2
					id="milestone-pages-heading"
					class="text-sm font-semibold uppercase tracking-wider"
					style="color: var(--ds-text-subtle); font-size: 11px;"
				>
					{t('items.linkedPages')}
				</h2>
			</div>
			{#if canEdit}
				<button
					type="button"
					bind:this={addButton}
					data-testid="milestone-page-add"
					class="add-page-btn inline-flex items-center gap-1 px-2 py-1 text-xs font-medium rounded transition-colors cursor-pointer"
					aria-expanded={showPicker}
					aria-controls="milestone-page-picker-panel"
					onclick={togglePicker}
				>
					<Plus class="w-3 h-3" />
					{t('common.add')}
				</button>
			{/if}
		</div>

		{#if error}
			<AlertBox variant="error" message={error} class="mb-3" />
		{/if}

		{#if canEdit && showPicker}
			<!-- Escape closes the picker and returns focus to "+ Add". -->
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<div id="milestone-page-picker-panel" class="mb-3 max-w-md" onkeydown={handlePickerKeydown}>
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

		{#if links.length > 0}
			<ul class="space-y-2">
				{#each links as link (link.id)}
					{@const pageHref = `/workspaces/${link.workspace_id || workspaceId}/pages/${link.page_id}`}
					<li
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
							<!-- Shown on row hover and whenever focus is inside the row. -->
							<button
								type="button"
								data-testid="milestone-page-unlink"
								class="remove-page-btn p-1 rounded cursor-pointer flex-shrink-0 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus:opacity-100 transition-opacity"
								onclick={() => unlinkPage(link.id)}
								title={t('items.removeLink')}
								aria-label={t('items.removeLink')}
							>
								<Trash2 class="w-4 h-4" />
							</button>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</section>
{/if}

<style>
	.add-page-btn {
		color: var(--ds-text-subtle);
	}

	.add-page-btn:hover,
	.add-page-btn[aria-expanded='true'] {
		background-color: var(--ds-background-neutral-hovered);
		color: var(--ds-text);
	}

	.remove-page-btn {
		color: var(--ds-text-subtle);
	}

	.remove-page-btn:hover {
		color: var(--ds-text-danger);
	}
</style>
