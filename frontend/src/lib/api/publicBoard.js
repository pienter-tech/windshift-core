/**
 * Public board API - uses plain fetch (no auth headers needed)
 */

/**
 * Fetch a public-board endpoint, throwing an error carrying the HTTP status
 * on non-2xx responses (shared by all public board calls).
 * @param {string} url
 * @param {{ signal?: AbortSignal }} [options]
 * @returns {Promise<any>}
 */
async function publicBoardFetch(url, { signal } = {}) {
  const res = await fetch(url, { signal });
  if (!res.ok) {
    const err = new Error(`${res.status}`);
    /** @type {any} */ (err).status = res.status;
    throw err;
  }
  return res.json();
}

export const publicBoard = {
  get(slug, options) {
    return publicBoardFetch(`/api/public/board/${encodeURIComponent(slug)}`, options);
  },

  getItem(slug, key, options) {
    return publicBoardFetch(
      `/api/public/board/${encodeURIComponent(slug)}/items/${encodeURIComponent(key)}`,
      options
    );
  },
};
