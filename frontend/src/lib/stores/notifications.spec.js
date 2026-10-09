/** @vitest-environment jsdom */

import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getAll: vi.fn(),
  addToast: vi.fn(),
  canSync: { value: true },
  onAvailable: null,
}));

vi.mock('../api.js', () => ({ api: { notifications: { getAll: mocks.getAll } } }));
vi.mock('../router.js', () => ({ navigate: vi.fn() }));
vi.mock('../utils/dateFormatter.js', () => ({ formatDateSimple: (d) => String(d) }));
vi.mock('../utils/isTauri.js', () => ({ isTauri: () => false }));
vi.mock('./activityStore.svelte.js', () => ({ activityStore: { isIdle: false } }));
vi.mock('./toasts.svelte.js', () => ({ addToast: mocks.addToast }));
vi.mock('../utils/backgroundSync.js', () => ({
  canRunBackgroundSync: () => mocks.canSync.value,
  isExpectedBackgroundSyncError: () => false,
  onBackgroundSyncAvailable: (callback) => {
    mocks.onAvailable = callback;
    return () => {
      mocks.onAvailable = null;
    };
  },
}));

import { notifications, startNotificationPoller, stopNotificationPoller } from './notifications.js';

class FakeEventSource {
  static instances = [];
  constructor(url) {
    this.url = url;
    this.listeners = {};
    FakeEventSource.instances.push(this);
  }
  addEventListener(type, fn) {
    this.listeners[type] = fn;
  }
  emit(type) {
    this.listeners[type]?.({ type });
  }
  close() {}
}

function notification(id, type, read = false) {
  return {
    id,
    type,
    read,
    title: `Title ${id}`,
    message: `Message ${id}`,
    timestamp: '2026-10-05T10:00:00Z',
    action_url: `/workspaces/1/items/${id}`,
  };
}

// Unread assignment + mention, and a read assignment, all from before the page load.
const existing = [
  notification(1, 'assignment'),
  notification(2, 'mention'),
  notification(3, 'assignment', true),
];

function stream() {
  return FakeEventSource.instances.at(-1);
}

// Fire a stream event and let the debounced reconcile load finish.
async function streamEvent(type) {
  stream().emit(type);
  await vi.advanceTimersByTimeAsync(300);
}

function toastedTitles() {
  return mocks.addToast.mock.calls.map(([options]) => options.title);
}

function expectExistingListedUnchanged() {
  const listed = get(notifications);
  expect(listed.map((n) => [n.id, n.read])).toEqual([
    [1, false],
    [2, false],
    [3, true],
  ]);
}

describe('notification toasts after a page load', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
    mocks.canSync.value = true;
    mocks.getAll.mockReset();
    mocks.addToast.mockReset();
    vi.spyOn(console, 'error').mockImplementation(() => {});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    stopNotificationPoller();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('does not toast existing notifications when a hidden tab reconciles before the inbox load', async () => {
    mocks.canSync.value = false;
    mocks.getAll.mockResolvedValue(existing);

    startNotificationPoller();
    // Hidden tab: the poller skips its initial load, the stream still connects.
    await vi.advanceTimersByTimeAsync(0);
    expect(mocks.getAll).not.toHaveBeenCalled();

    await streamEvent('connected');
    expect(mocks.getAll).toHaveBeenCalled();
    expect(mocks.addToast).not.toHaveBeenCalled();
    expectExistingListedUnchanged();

    // The tab becomes visible: the resumed load still toasts nothing old.
    mocks.canSync.value = true;
    mocks.onAvailable();
    await vi.advanceTimersByTimeAsync(0);
    expect(mocks.addToast).not.toHaveBeenCalled();
    expectExistingListedUnchanged();

    // A genuinely new assignment toasts exactly once.
    mocks.getAll.mockResolvedValue([notification(4, 'assignment'), ...existing]);
    await streamEvent('notifications');
    await streamEvent('notifications');
    expect(toastedTitles()).toEqual(['Title 4']);
  });

  it('does not toast existing notifications after a failed first load and a stream reconcile', async () => {
    mocks.getAll.mockRejectedValueOnce(new Error('boom')).mockResolvedValue(existing);

    startNotificationPoller();
    await vi.advanceTimersByTimeAsync(0);
    expect(mocks.getAll).toHaveBeenCalledTimes(1);
    expect(get(notifications)).toEqual([]);

    await streamEvent('connected');
    expect(mocks.getAll).toHaveBeenCalledTimes(2);
    expect(mocks.addToast).not.toHaveBeenCalled();
    expectExistingListedUnchanged();

    mocks.getAll.mockResolvedValue([notification(5, 'mention'), ...existing]);
    await streamEvent('notifications');
    expect(toastedTitles()).toEqual(['Title 5']);
    expect(get(notifications).find((n) => n.id === 5)?.read).toBe(false);
  });

  it('does not toast existing notifications after a failed first load and a poll', async () => {
    vi.stubGlobal('EventSource', undefined);
    mocks.getAll.mockRejectedValueOnce(new Error('boom')).mockResolvedValue(existing);

    startNotificationPoller();
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(mocks.getAll).toHaveBeenCalledTimes(2);
    expect(mocks.addToast).not.toHaveBeenCalled();
    expectExistingListedUnchanged();

    mocks.getAll.mockResolvedValue([notification(6, 'assignment'), ...existing]);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(toastedTitles()).toEqual(['Title 6']);
  });

  it('toasts a new assignment once after a normal visible start', async () => {
    mocks.getAll.mockResolvedValue(existing);

    startNotificationPoller();
    await vi.advanceTimersByTimeAsync(0);
    await streamEvent('connected');
    expect(mocks.addToast).not.toHaveBeenCalled();
    expectExistingListedUnchanged();

    mocks.getAll.mockResolvedValue([notification(7, 'assignment'), ...existing]);
    await streamEvent('notifications');
    await streamEvent('reload');
    expect(toastedTitles()).toEqual(['Title 7']);
  });
});
