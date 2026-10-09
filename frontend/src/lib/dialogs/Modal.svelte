<script module>
  // Context key through which a ModalHeader names its dialog.
  export const modalTitleKey = Symbol('modal-title');
  // Context key through which floating content (a picker's dropdown) finds
  // the dialog element to render into.
  export const modalElementKey = Symbol('modal-element');
</script>

<script>
  import { setContext, untrack } from 'svelte';
  import { getShortcut, matchesShortcut, getDisplayString } from '../utils/keyboardShortcuts.js';
  import { portal } from '../actions/portal.js';

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
    children
  } = $props();

  // A ModalHeader inside this dialog registers through context, takes this
  // id for its heading, and so gives the dialog its accessible name
  // (aria-labelledby). The id is per instance, so stacked dialogs don't clash.
  const uid = $props.id();
  const titleId = `${uid}-title`;
  let titleCount = $state(0);
  setContext(modalTitleKey, {
    id: titleId,
    // Called from the header's effect; untracked so that effect doesn't
    // depend on the count it changes.
    register() {
      untrack(() => (titleCount += 1));
      return () => untrack(() => (titleCount -= 1));
    }
  });

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

  // Get shortcut configurations
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

    // The submit gesture (Ctrl/Cmd+Enter) must fire even when inner content
    // already handled the key: a rich-text editor (ProseMirror) calls
    // preventDefault() on Cmd+Enter, which would otherwise bail at the
    // defaultPrevented guard below and swallow the submit. Checking it first
    // mirrors CreateModal's window-level handler.
    if (onSubmit && !submitDisabled && matchesShortcut(e, submitShortcut)) {
      e.preventDefault();
      handleSubmit();
      return;
    }

    if (e.defaultPrevented) return;

    // Check for cancel shortcut (Escape)
    if (matchesShortcut(e, cancelShortcut)) {
      close();
      return;
    }

    // Only handle submission if onSubmit is provided
    if (!onSubmit || submitDisabled) return;

    // Enter without modifier
    if (e.key === 'Enter' && !e.ctrlKey && !e.metaKey) {
      // A <textarea> or a rich-text contenteditable (the Markdown editor's
      // ProseMirror surface) owns bare Enter — it inserts a line rather than
      // submitting the modal.
      if (e.target.tagName === 'TEXTAREA' || e.target.isContentEditable) {
        return;
      }
      // In input field or outside input: submit
      e.preventDefault();
      handleSubmit();
    }
  }

  // Detect if the modal contains a multiline editor (a <textarea> or a
  // contenteditable rich-text surface). This drives the footer submit hint:
  // Ctrl/Cmd+Enter when present, plain Enter otherwise. Re-run on focusin too,
  // since a lazily-mounted editor may not exist yet at initial detection.
  function detectTextarea() {
    if (modalContentElement) {
      hasTextarea =
        modalContentElement.querySelector('textarea, [contenteditable="true"]') !== null;
    }
  }

  let submitHint = $derived(hasTextarea ? getDisplayString(submitShortcut) : '↵');

  // Keep focus inside the open dialog. Escape is handled on the backdrop, so
  // it only reaches this dialog while focus is inside it. When a field in
  // the dialog is blurred without focus moving anywhere else (seen in the
  // browser when Escape blurs a picker's search field), focus would fall to
  // <body> and the next Escape would not close the dialog (WCORE-65). Move
  // it to the dialog itself instead. Focus lost by a mouse press, such as a
  // click in a picker's dropdown portalled outside the dialog, is first left
  // to that interaction: a picker may refocus its field or move focus on
  // after a pick. A press that ends with focus still on <body> (e.g. on the
  // dropdown's empty space, where no picker code runs) gets the field back,
  // or the dialog if the field is gone (WCORE-67).
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

  // Return focus to the control that opened the dialog once it closes
  // (WCORE-63). The opener is whatever had focus when the dialog opened; this
  // pre-effect runs before the dialog moves focus into itself. The restore
  // waits a microtask so the dialog is gone, and only runs when focus fell
  // to <body> with it: a caller that already moved focus elsewhere keeps
  // it. It also skips an opener that left the document and a close caused
  // by navigating to another page.
  $effect.pre(() => {
    if (!isOpen || inline) return;
    const opener = document.activeElement;
    const path = location.pathname;
    return () => {
      queueMicrotask(() => {
        if (!opener || opener === document.body || !opener.isConnected) return;
        if (location.pathname !== path) return;
        const active = document.activeElement;
        if (active && active !== document.body) return;
        opener.focus();
      });
    };
  });

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
    return () => {
      document.removeEventListener('mousedown', press, true);
      document.removeEventListener('mouseup', release, true);
      clearTimeout(timer);
      mousePressed = false;
      blurredByPress = null;
    };
  });

  $effect(() => {
    if (isOpen && modalContentElement && backdropElement) {
      const timer = setTimeout(() => {
        detectTextarea();
        if (modalContentElement.contains(document.activeElement)) return;
        backdropElement.focus();
        if (autoFocus) {
          const focusable = modalContentElement.querySelector(
            'input:not([disabled]):not([type="hidden"]), textarea:not([disabled]), select:not([disabled])'
          );
          if (focusable) {
            focusable.focus();
          }
        }
      }, 100);
      // A lazily-mounted editor may appear after the initial detect — keep the
      // submit hint in sync as the modal's subtree changes.
      const observer = new MutationObserver(detectTextarea);
      observer.observe(modalContentElement, { childList: true, subtree: true });
      return () => {
        clearTimeout(timer);
        observer.disconnect();
      };
    }
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
  <div
    use:portal
    bind:this={backdropElement}
    class={`fixed inset-0 flex items-start justify-center pt-8 overflow-y-auto ${zIndexClass}`}
    style={noBackdrop ? '' : 'background-color: rgba(0, 0, 0, 0.4); backdrop-filter: blur(4px);'}
    tabindex="-1"
    onmousedown={handleBackdropMouseDown}
    onclick={handleBackdropClick}
    onkeydown={handleKeydown}
    onfocusin={detectTextarea}
    onfocusout={handleFocusOut}
    role="dialog"
    aria-modal="true"
    aria-labelledby={titleCount > 0 ? titleId : undefined}
    data-testid={dataTestid}
  >
    <div
      bind:this={modalContentElement}
      class="relative rounded-lg overflow-hidden {maxWidth} w-full mx-4 mb-8 {maxHeight ? 'flex flex-col' : ''}"
      style="background-color: var(--ds-surface-raised, var(--ds-surface, white)); box-shadow: var(--shadow-float, 0 20px 50px rgba(0, 0, 0, 0.18));{maxHeight ? ` max-height: ${maxHeight};` : ''}"
    >
      {@render children?.(submitHint)}
    </div>
  </div>
{/if}
