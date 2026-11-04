/**
 * Svelte action: keep a textarea sized to its content so long text wraps
 * instead of scrolling inside a single line. Used by the phone editors and
 * the desktop knowledge-page title.
 *
 * The height is recomputed on input, when the tracked value changes, when the
 * element's width changes (responsive layout, sidebar toggle), and on window
 * resize (rotation, keyboard). Width changes matter because a textarea that
 * mounts before its final width is known can wrap onto a different number of
 * lines than the first measurement sees.
 *
 * @param {HTMLTextAreaElement} node
 * @param {unknown} [_dep] - pass the bound value so programmatic changes resize
 */
export function autoGrow(node, _dep) {
  let frame = 0;
  let lastWidth = 0;

  function measure() {
    node.style.height = 'auto';
    node.style.height = `${node.scrollHeight}px`;
  }

  // Defer to the next frame so a programmatic value change and its layout
  // have settled before measuring.
  function resize() {
    if (typeof requestAnimationFrame !== 'function') {
      measure();
      return;
    }
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(measure);
  }

  const observer =
    typeof ResizeObserver === 'function'
      ? new ResizeObserver((entries) => {
          const width = entries[0]?.contentRect.width ?? 0;
          if (width === lastWidth) return;
          lastWidth = width;
          measure();
        })
      : null;
  observer?.observe(node);

  measure();
  node.addEventListener('input', measure);
  window.addEventListener('resize', resize);
  return {
    update() {
      resize();
    },
    destroy() {
      cancelAnimationFrame(frame);
      observer?.disconnect();
      node.removeEventListener('input', measure);
      window.removeEventListener('resize', resize);
    },
  };
}

/**
 * Enter moves to the next field instead of inserting a newline — a title
 * field may wrap, but it stays a single logical line. Shift+Enter still
 * inserts a newline for anyone who really wants one.
 *
 * @param {HTMLTextAreaElement} node
 * @param {{ next?: HTMLElement | null }} [options]
 */
export function enterMovesFocus(node, options = {}) {
  function onKeydown(event) {
    if (event.key !== 'Enter' || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey)
      return;
    event.preventDefault();
    options.next?.focus();
  }
  node.addEventListener('keydown', onKeydown);
  return {
    update(next = {}) {
      options = next;
    },
    destroy() {
      node.removeEventListener('keydown', onKeydown);
    },
  };
}
