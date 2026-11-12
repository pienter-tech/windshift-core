// Display data for the combined CI status of a pull request link (WCORE-68).
// Only Gitea/Forgejo links carry `ci_state`; others render no status.

const CI_STATUS_DISPLAY = {
  pending: {
    labelKey: 'scm.ciPending',
    bg: 'var(--ds-background-warning)',
    text: 'var(--ds-text-warning)',
  },
  success: {
    labelKey: 'scm.ciSuccess',
    bg: 'var(--ds-background-success)',
    text: 'var(--ds-text-success)',
  },
  failure: {
    labelKey: 'scm.ciFailure',
    bg: 'var(--ds-background-danger)',
    text: 'var(--ds-text-danger)',
  },
};

/**
 * Returns how to show a link's CI status, or null when it has none.
 * @param {{ link_type?: string, ci_state?: string } | null | undefined} link
 * @returns {{ state: string, labelKey: string, bg: string, text: string } | null}
 */
export function ciStatusDisplay(link) {
  if (link?.link_type !== 'pull_request') return null;
  const display = CI_STATUS_DISPLAY[link.ci_state];
  return display ? { state: link.ci_state, ...display } : null;
}
