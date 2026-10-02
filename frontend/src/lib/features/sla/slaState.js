import { api } from '../../api.js';

// Item SLA reads are pure and cheap on the server, but a list/board page shows
// many rows. Cache each item's state briefly and dedupe concurrent reads so a
// page issues at most one request per item, and none at all on a re-render.
const TTL_MS = 30_000;
const stateCache = new Map();
const inFlight = new Map();
const thresholdsCache = new Map();
let cacheGeneration = 0;

// Batch coalescing: badges mounted in the same tick share one workspace
// batch read instead of issuing one request per row (WI-1591). Items whose
// workspace is unknown (or batches that fail) fall back to per-item reads.
const pendingByWorkspace = new Map();

export async function getItemSLA(itemId, workspaceId = null) {
  if (!itemId) return [];
  const cached = stateCache.get(itemId);
  const now = Date.now();
  if (cached && now - cached.at < TTL_MS) return cached.value;
  if (inFlight.has(itemId)) return inFlight.get(itemId);

  if (workspaceId && api.sla?.getItemSLABatch) {
    return enqueueBatchItemSLA(itemId, workspaceId);
  }
  if (!api.sla?.getItemSLA) return [];
  return fetchItemSLA(itemId);
}

// enqueueBatchItemSLA collects item ids per workspace and flushes them on the
// next microtask, so all badges rendered by one page share a single request.
function enqueueBatchItemSLA(itemId, workspaceId) {
  let queue = pendingByWorkspace.get(workspaceId);
  if (!queue) {
    queue = { ids: new Set(), waiters: new Map(), scheduled: false };
    pendingByWorkspace.set(workspaceId, queue);
  }
  if (queue.waiters.has(itemId)) return queue.waiters.get(itemId);
  queue.ids.add(itemId);
  const promise = new Promise((resolve) => queue.waiters.set(itemId, resolve));
  if (!queue.scheduled) {
    queue.scheduled = true;
    queueMicrotask(() => flushBatchItemSLA(workspaceId, queue));
  }
  return promise;
}

async function flushBatchItemSLA(workspaceId, queue) {
  pendingByWorkspace.delete(workspaceId);
  const ids = [...queue.ids];
  const generation = cacheGeneration;
  let results = null;
  try {
    results = (await api.sla.getItemSLABatch(workspaceId, ids)) ?? {};
  } catch {
    results = null;
  }
  for (const itemId of ids) {
    const resolve = queue.waiters.get(itemId);
    if (!resolve) continue;
    const value = results ? (results[itemId] ?? results[String(itemId)] ?? []) : null;
    if (value) {
      if (generation === cacheGeneration) {
        stateCache.set(itemId, { value, at: Date.now() });
      }
      resolve(value);
    } else {
      // Missing id or failed batch: fall back to the single-item read.
      resolve(fetchItemSLA(itemId));
    }
  }
}

// fetchItemSLA issues the per-item read and caches the result.
function fetchItemSLA(itemId) {
  const generation = cacheGeneration;
  let request;
  request = api.sla
    .getItemSLA(itemId)
    .then((value) => {
      if (generation === cacheGeneration) {
        stateCache.set(itemId, { value: value ?? [], at: Date.now() });
      }
      return value ?? [];
    })
    .catch(() => [])
    .finally(() => {
      if (inFlight.get(itemId) === request) inFlight.delete(itemId);
    });

  inFlight.set(itemId, request);
  return request;
}

export async function getSLAThresholds(workspaceId) {
  if (!workspaceId || !api.sla?.getWarningThresholds) return [];
  const cached = thresholdsCache.get(workspaceId);
  if (cached && Date.now() - cached.at < TTL_MS) return cached.value;
  const generation = cacheGeneration;
  const value = (await api.sla.getWarningThresholds(workspaceId).catch(() => [])) ?? [];
  if (generation === cacheGeneration) thresholdsCache.set(workspaceId, { value, at: Date.now() });
  return value;
}

// Drop cached state when an item changes so the next render re-derives it.
export function invalidateSLAState() {
  cacheGeneration++;
  inFlight.clear();
  stateCache.clear();
  thresholdsCache.clear();
  // Waiters of an in-flight batch still resolve with their fallback reads.
  pendingByWorkspace.clear();
}

if (typeof window !== 'undefined') {
  window.addEventListener('refresh-work-items', invalidateSLAState);
}
