<script>
  let {
    for: forId = null,
    id = null,
    required = false,
    size = 'sm',           // 'xs' | 'sm' | 'md'
    color = 'subtle',      // 'default' | 'subtle'
    class: className = '',
    children
  } = $props();

  const sizeClass = $derived({
    xs: 'text-xs',
    sm: 'text-sm',
    md: 'text-base'
  }[size] || 'text-sm');

  const colorToken = $derived(color === 'subtle' ? '--ds-text-subtle' : '--ds-text');
</script>

<label
  {id}
  for={forId}
  class="block font-medium {sizeClass} {className}"
  style="color: var({colorToken});"
>
  {@render children?.()}
  {#if required}
    <span class="text-ds-text-danger ml-0.5">*</span>
  {/if}
</label>
