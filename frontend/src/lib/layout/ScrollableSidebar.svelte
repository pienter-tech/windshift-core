<script>
  import { t } from '../stores/i18n.svelte.js';

  let {
    as = 'aside',
    class: className = '',
    header = undefined,
    children,
    footer = undefined,
    scrollClass = '',
    scrollTestid = undefined,
    reserveScrollbarSpace = true,
    scrollContent = true,
    scrollFade = true,
    ...restProps
  } = $props();

  let contentEl = $state(null);
  let showTopFade = $state(false);
  let showBottomFade = $state(false);

  const fadeTestId = $derived(scrollTestid ? `${scrollTestid}-fade` : 'scrollable-sidebar-fade');

  function updateFades() {
    if (!contentEl) return;
    const { scrollTop, scrollHeight, clientHeight } = contentEl;
    showTopFade = scrollTop > 1;
    showBottomFade = scrollTop + clientHeight < scrollHeight - 1;
  }

  $effect(() => {
    if (!contentEl || !scrollContent || !scrollFade) {
      showTopFade = false;
      showBottomFade = false;
      return;
    }

    updateFades();
    contentEl.addEventListener('scroll', updateFades, { passive: true });

    // Content can grow or shrink after mount (search filtering, async loads),
    // so re-measure on size and DOM changes instead of only on scroll.
    const resizeObserver =
      typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(updateFades);
    resizeObserver?.observe(contentEl);
    const contentObserver =
      typeof MutationObserver === 'undefined' ? null : new MutationObserver(updateFades);
    contentObserver?.observe(contentEl, { childList: true, subtree: true });
    void document.fonts?.ready.then(updateFades);

    return () => {
      contentEl.removeEventListener('scroll', updateFades);
      resizeObserver?.disconnect();
      contentObserver?.disconnect();
    };
  });
</script>

<svelte:element this={as} {...restProps} class={['scrollable-sidebar', className]}>
  {#if header}
    <div class="scrollable-sidebar-header">
      {@render header()}
    </div>
  {/if}

  <div class="scrollable-sidebar-body">
    <div
      bind:this={contentEl}
      class={[
        'scrollable-sidebar-content',
        { 'scrollable-sidebar-content-static': !scrollContent },
        { 'scrollable-sidebar-content-stable-gutter': reserveScrollbarSpace },
        scrollClass,
      ]}
      data-testid={scrollTestid}
    >
      {@render children?.()}
    </div>

    {#if scrollContent && scrollFade}
      {#if showTopFade}
        <div
          class="scrollable-sidebar-fade scrollable-sidebar-fade-top"
          data-testid="{fadeTestId}-top"
          aria-hidden="true"
        >
          <span class="scrollable-sidebar-fade-hint">{t('nav.scrollUpForMore')}</span>
        </div>
      {/if}
      {#if showBottomFade}
        <div
          class="scrollable-sidebar-fade scrollable-sidebar-fade-bottom"
          data-testid="{fadeTestId}-bottom"
          aria-hidden="true"
        >
          <span class="scrollable-sidebar-fade-hint">{t('nav.scrollDownForMore')}</span>
        </div>
      {/if}
    {/if}
  </div>

  {#if footer}
    <div class="scrollable-sidebar-footer">
      {@render footer()}
    </div>
  {/if}
</svelte:element>

<style>
  .scrollable-sidebar {
    display: flex;
    height: 100%;
    min-height: 0;
    max-height: 100%;
    flex-direction: column;
    overflow: hidden;
  }

  .scrollable-sidebar-header,
  .scrollable-sidebar-footer {
    flex: 0 0 auto;
  }

  .scrollable-sidebar-body {
    position: relative;
    display: flex;
    min-height: 0;
    flex: 1 1 auto;
    align-self: stretch;
    flex-direction: column;
    container-type: inline-size;
  }

  .scrollable-sidebar-content {
    min-height: 0;
    flex: 1 1 auto;
    overflow-x: hidden;
    overflow-y: auto;
    overscroll-behavior-y: contain;
    scrollbar-color: var(--ds-border) transparent;
    scrollbar-width: thin;
  }

  .scrollable-sidebar-content-stable-gutter {
    scrollbar-gutter: stable;
  }

  .scrollable-sidebar-content-static {
    overflow: hidden;
    scrollbar-gutter: auto;
  }

  /* Overlay fades signal that the list continues past the edge. The colour
     defaults to the raised surface and can be overridden by sidebars that use
     a different background via --scrollable-sidebar-fade-color. */
  .scrollable-sidebar-fade {
    position: absolute;
    z-index: 1;
    right: 0;
    left: 0;
    display: flex;
    height: 2rem;
    justify-content: center;
    pointer-events: none;
    animation: scrollable-sidebar-fade-in 120ms ease-out;
  }

  .scrollable-sidebar-fade-top {
    top: 0;
    align-items: flex-start;
    background: linear-gradient(
      to bottom,
      var(--scrollable-sidebar-fade-color, var(--ds-surface-raised)),
      transparent
    );
  }

  .scrollable-sidebar-fade-bottom {
    bottom: 0;
    align-items: flex-end;
    background: linear-gradient(
      to top,
      var(--scrollable-sidebar-fade-color, var(--ds-surface-raised)),
      transparent
    );
  }

  /* The hint is hidden in narrow, icon-only sidebars where it would not fit. */
  .scrollable-sidebar-fade-hint {
    display: none;
    padding: 0.25rem 0.5rem;
    color: var(--ds-text-subtle);
    font-size: 0.6875rem;
    font-weight: 500;
    line-height: 1rem;
    white-space: nowrap;
  }

  @container (min-width: 11rem) {
    .scrollable-sidebar-fade-hint {
      display: block;
    }
  }

  @keyframes scrollable-sidebar-fade-in {
    from {
      opacity: 0;
    }

    to {
      opacity: 1;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .scrollable-sidebar-fade {
      animation: none;
    }
  }

  .scrollable-sidebar-content::-webkit-scrollbar {
    width: 8px;
  }

  .scrollable-sidebar-content::-webkit-scrollbar-track {
    background: transparent;
  }

  .scrollable-sidebar-content::-webkit-scrollbar-thumb {
    border: 2px solid transparent;
    border-radius: 999px;
    background: var(--ds-border);
    background-clip: content-box;
  }

  .scrollable-sidebar-content::-webkit-scrollbar-thumb:hover {
    background-color: var(--ds-text-subtlest);
  }
</style>
