/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, render, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  attachments: {
    uploadToItem: vi.fn(async () => ({ id: 7 })),
    upload: vi.fn(async () => ({ success: true, attachment: { id: 8 } })),
    getContentUrl: (id) => `/api/v2/attachments/${id}/content`,
  },
}));

vi.mock('../api/misc.js', () => ({ attachments: apiMock.attachments }));
vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../router.js', () => ({ navigate: vi.fn() }));

import { attachmentStatus } from '../stores/attachmentStatus.svelte.js';
import MilkdownEditor from './MilkdownEditor.svelte';

// ProseMirror adds its own separator images; match only inserted ones.
const IMG = 'img:not(.ProseMirror-separator)';

// jsdom has no ClipboardEvent or DragEvent; the upload plugin checks instanceof.
beforeAll(() => {
  if (typeof globalThis.DragEvent === 'undefined') {
    globalThis.DragEvent = class DragEvent extends Event {};
  }
  if (typeof globalThis.ClipboardEvent === 'undefined') {
    globalThis.ClipboardEvent = class ClipboardEvent extends Event {
      constructor(type, init = {}) {
        super(type, init);
        this.clipboardData = init.clipboardData ?? null;
      }
    };
  }
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function pngFile() {
  return new File([new Uint8Array([137, 80, 78, 71])], 'pasted.png', { type: 'image/png' });
}

function pasteFile(root, file) {
  const files = { length: 1, item: (i) => (i === 0 ? file : null), 0: file };
  const clipboardData = { files, types: ['Files'], getData: () => '' };
  const event = new ClipboardEvent('paste', { bubbles: true, cancelable: true, clipboardData });
  root.dispatchEvent(event);
  return event;
}

async function renderEditor(props) {
  let content = '';
  const { container } = render(MilkdownEditor, {
    props: { testId: 'ed', content: '', onContentChange: (md) => (content = md), ...props },
  });
  const wrapper = container.querySelector('[data-testid="ed"]');
  await waitFor(() => expect(wrapper.getAttribute('data-ready')).toBe('true'));
  return { root: wrapper.querySelector('.ProseMirror'), getContent: () => content };
}

describe('MilkdownEditor pasted image files', () => {
  it('inserts nothing when attachments are disabled and there is no custom uploader', async () => {
    attachmentStatus.hydrate({ enabled: false, writable: true });
    const { root, getContent } = await renderEditor({ entityType: 'item', entityId: 1 });

    const event = pasteFile(root, pngFile());
    expect(event.defaultPrevented).toBe(true);
    await new Promise((r) => setTimeout(r, 50));

    expect(root.querySelector(IMG)).toBeNull();
    expect(getContent()).not.toContain('data:');
    expect(apiMock.attachments.uploadToItem).not.toHaveBeenCalled();
    expect(apiMock.attachments.upload).not.toHaveBeenCalled();
  });

  it('uploads to the item when attachments are enabled', async () => {
    attachmentStatus.hydrate({ enabled: true, writable: true });
    const { root } = await renderEditor({ entityType: 'item', entityId: 1 });

    pasteFile(root, pngFile());

    await waitFor(() =>
      expect(root.querySelector(IMG)?.getAttribute('src')).toBe('/api/v2/attachments/7/content')
    );
    expect(apiMock.attachments.uploadToItem).toHaveBeenCalledTimes(1);
  });

  it('uses a custom uploader even when attachments are disabled', async () => {
    attachmentStatus.hydrate({ enabled: false, writable: true });
    const customUploadFn = vi.fn(async () => ({ success: true, attachment: { id: 9 } }));
    const { root } = await renderEditor({ customUploadFn, downloadUrlBase: '/custom' });

    pasteFile(root, pngFile());

    await waitFor(() =>
      expect(root.querySelector(IMG)?.getAttribute('src')).toBe('/custom/9/download')
    );
    expect(customUploadFn).toHaveBeenCalledTimes(1);
  });

  it('keeps external image URLs from Markdown when attachments are disabled', async () => {
    attachmentStatus.hydrate({ enabled: false, writable: true });
    const { root } = await renderEditor({ content: '![logo](https://example.com/logo.png)' });

    await waitFor(() =>
      expect(root.querySelector(IMG)?.getAttribute('src')).toBe('https://example.com/logo.png')
    );
  });
});
