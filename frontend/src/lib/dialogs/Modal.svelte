<script module>
  // Context key through which floating content (a picker's dropdown) finds
  // the dialog element to render into.
  export const modalElementKey = Symbol('modal-element');
</script>

<script>
  import { onDestroy, setContext } from 'svelte';
  import { on } from 'svelte/events';
  import { getShortcut, matchesShortcut, getDisplayString } from '../utils/keyboardShortcuts.js';
  import ModalBackdrop from '../components/ModalBackdrop.svelte';
  import { overlayOwnsFocus, overlayHasFocusedControl, isFocusEligible } from '../utils/overlayScope.js';

  let {
    isOpen = $bindable(false),
    preventClose = false,
    maxWidth = 'max-w-lg',
    maxHeight = '',
    autoFocus = true,
    onSubmit = null,
    submitDisabled = false,
    zIndexClass = 'z-50',
    noBackdrop = false,
    inline = false,
    closeOnBackdropClick = true,
    onclose = null,
    onKeydown = null,
    dataTestid = undefined,
    ariaLabelledBy = undefined,
    ariaLabel = undefined,
    children
  } = $props();

  let backdropElement = $state(null);
  let modalContentElement = $state(null);

  // aria-modal hides everything outside the dialog element from screen
  // readers, so a dropdown opened from inside the dialog must render inside
  // it too (WCORE-64). Null for an inline modal, which has no dialog element.
  setContext(modalElementKey, {
    get element() {
      return backdropElement;
    }
  });
  let hasTextarea = $state(false);

  const submitShortcut = getShortcut('modal', 'submit');
  const cancelShortcut = getShortcut('modal', 'cancel');

  function close() {
    if (!preventClose) {
      isOpen = false;
      onclose?.();
    }
  }

  // A press that starts inside the dialog and is released on the backdrop
  // (a drag from a field, or from a picker's dropdown, which renders in the
  // backdrop) ends in a click on the backdrop. That is not a click outside.
  let pressStartedInside = false;

  function handleBackdropMouseDown(e) {
    pressStartedInside = e.target !== e.currentTarget;
  }

  function handleBackdropClick(e) {
    const startedInside = pressStartedInside;
    pressStartedInside = false;
    // Clicking outside the modal content can silently dismiss it and lose
    // anything the user has typed. Creation / editing dialogs gate this off
    // (closeOnBackdropClick=false); the modal is closed through its explicit
    // buttons or Escape instead.
    if (closeOnBackdropClick && e.target === e.currentTarget && !startedInside) {
      close();
    }
  }

  function handleSubmit() {
    if (onSubmit && !submitDisabled) {
      onSubmit();
    }
  }

  function handleKeydown(e) {
    onKeydown?.(e);
    e.stopPropagation();
    if (e.isComposing || e.keyCode === 229) return;

    // Rich-text editors may prevent Cmd/Ctrl+Enter while still requesting submit.
    if (onSubmit && matchesShortcut(e, submitShortcut)) {
      e.preventDefault();
      handleSubmit();
      return;
    }

    if (e.defaultPrevented) return;

    if (matchesShortcut(e, cancelShortcut)) {
      close();
      return;
    }

    if (!onSubmit) return;

    // Native buttons, pickers and multiline editors own their Enter activation.
    if (e.key === 'Enter' && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey &&
        e.target instanceof HTMLInputElement &&
        ['text', 'search', 'email', 'url', 'tel', 'password', 'number'].includes(e.target.type) &&
        !e.target.closest('[role="combobox"], [role="listbox"], [contenteditable="true"]')) {
      e.preventDefault();
      handleSubmit();
    }
  }

  // Lazy multiline editors switch the footer hint from Enter to Cmd/Ctrl+Enter.
  function detectTextarea() {
    if (modalContentElement) {
      hasTextarea =
        modalContentElement.querySelector('textarea, [contenteditable="true"]') !== null;
    }
  }

  let submitHint = $derived(hasTextarea ? getDisplayString(submitShortcut) : '↵');

  // Keep focus inside the open dialog. When a field in the dialog is blurred
  // without focus moving anywhere else (seen in the browser when Escape
  // blurs a picker's search field), focus would fall to <body> (WCORE-65).
  // Move it to the dialog itself instead. Focus lost by a mouse press, such
  // as a click in a picker's dropdown, is first left to that interaction: a
  // picker may refocus its field or move focus on after a pick. A press that
  // ends with focus still on <body> (e.g. on the dropdown's empty space,
  // where no picker code runs) gets the field back, or the dialog if the
  // field is gone (WCORE-67). Returning focus to the opener on close is
  // ModalBackdrop's overlay scope's job.
  let mousePressed = false;
  let blurredByPress = null;

  function handleFocusOut(e) {
    if (e.relatedTarget) return;
    if (mousePressed) {
      blurredByPress = e.target;
      return;
    }
    queueMicrotask(() => {
      if (!isOpen || !backdropElement?.isConnected) return;
      const active = document.activeElement;
      if (active && active !== document.body) return;
      backdropElement.focus({ preventScroll: true });
    });
  }

  $effect(() => {
    if (!isOpen || inline) return;
    let timer;
    const press = () => {
      mousePressed = true;
      blurredByPress = null;
    };
    const release = () => {
      mousePressed = false;
      const field = blurredByPress;
      blurredByPress = null;
      if (!field) return;
      // A timer runs after the click and its follow-up microtasks (a pick
      // that focuses another control wins). Unlike an animation frame, it
      // also runs in a background tab.
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (!isOpen || !backdropElement?.isConnected) return;
        const active = document.activeElement;
        if (active && active !== document.body) return;
        const target =
          field.isConnected && backdropElement.contains(field) ? field : backdropElement;
        target.focus({ preventScroll: true });
      });
    };
    document.addEventListener('mousedown', press, true);
    document.addEventListener('mouseup', release, true);
    // ModalBackdrop owns the dialog element's markup, so listen on it here.
    const offFocusOut = backdropElement ? on(backdropElement, 'focusout', handleFocusOut) : null;
    const offMouseDown = backdropElement ? on(backdropElement, 'mousedown', handleBackdropMouseDown) : null;
    return () => {
      document.removeEventListener('mousedown', press, true);
      document.removeEventListener('mouseup', release, true);
      offFocusOut?.();
      offMouseDown?.();
      clearTimeout(timer);
      mousePressed = false;
      blurredByPress = null;
    };
  });

  // Auto-focus runs once per open. The effect below re-runs whenever the modal
  // content is (re)mounted, so a timer scheduled inside it would keep resetting
  // and could fire late — after the user has opened a dropdown — stealing focus
  // back to the first field and scrolling the modal away from the control.
  let autoFocusTimer = null;

  onDestroy(() => {
    if (autoFocusTimer !== null) clearTimeout(autoFocusTimer);
  });

  $effect(() => {
    if (!isOpen) {
      if (autoFocusTimer !== null) {
        clearTimeout(autoFocusTimer);
        autoFocusTimer = null;
      }
      return;
    }
    if (!modalContentElement || !backdropElement) return;

    if (autoFocusTimer === null) {
      autoFocusTimer = setTimeout(() => {
        autoFocusTimer = null;
        if (!isOpen || !modalContentElement || !backdropElement || !overlayOwnsFocus(backdropElement)) return;
        detectTextarea();
        if (overlayHasFocusedControl(backdropElement)) return;
        // Managed focus must not scroll a dialog the user already positioned.
        backdropElement.focus({ preventScroll: true });
        if (autoFocus) {
          const focusable = [...modalContentElement.querySelectorAll(
            'input:not([disabled]):not([type="hidden"]), textarea:not([disabled]), select:not([disabled])'
          )].find(isFocusEligible);
          if (focusable) {
            focusable.focus({ preventScroll: true });
          }
        }
      }, 100);
    }

    // Keep the hint current when an editor mounts after opening.
    const observer = new MutationObserver(detectTextarea);
    observer.observe(modalContentElement, { childList: true, subtree: true });
    return () => observer.disconnect();
  });
</script>

{#if isOpen && inline}
  <!-- Inline mode is used for long creation flows that need page-sized space
       without dialog semantics, a backdrop, or scroll trapping. -->
  <div
    class="relative rounded-lg overflow-hidden w-full border"
    style="background-color: var(--ds-surface-raised, var(--ds-surface, white)); border-color: var(--ds-border);"
  >
    {@render children?.(getDisplayString(submitShortcut))}
  </div>
{:else if isOpen}
  <!-- Backdrop -->
  <ModalBackdrop
    bind:show={isOpen}
    portal={true}
    zIndex={null}
    class={zIndexClass}
    {dataTestid}
    {ariaLabelledBy}
    {ariaLabel}
    align="top" paddingTop="pt-8" scrollable
    opacity={noBackdrop ? 0 : 0.4}
    blur={noBackdrop ? 0 : 4}
    closeOnClick={false} closeOnEscape={false}
    onKeydown={handleKeydown}
    onBackdropClick={handleBackdropClick}
    bind:backdropElement
    focusOnOpen={false}
    onclose={close}
  >
    <div
      bind:this={modalContentElement}
      onfocusin={detectTextarea}
      class="relative rounded-lg overflow-hidden {maxWidth} w-full mx-4 mb-8 {maxHeight ? 'flex flex-col' : ''}"
      style="background-color: var(--ds-surface-raised, var(--ds-surface, white)); box-shadow: var(--shadow-float, 0 20px 50px rgba(0, 0, 0, 0.18));{maxHeight ? ` max-height: ${maxHeight};` : ''}"
    >
      {@render children?.(submitHint)}
    </div>
  </ModalBackdrop>
{/if}
