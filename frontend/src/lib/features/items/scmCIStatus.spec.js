import { describe, expect, it } from 'vitest';
import { ciStatusDisplay } from './scmCIStatus.js';

describe('ciStatusDisplay', () => {
  it.each([
    ['pending', 'scm.ciPending', 'var(--ds-text-warning)'],
    ['success', 'scm.ciSuccess', 'var(--ds-text-success)'],
    ['failure', 'scm.ciFailure', 'var(--ds-text-danger)'],
  ])('shows a %s pull request status', (state, labelKey, text) => {
    expect(ciStatusDisplay({ link_type: 'pull_request', ci_state: state })).toMatchObject({
      state,
      labelKey,
      text,
    });
  });

  it('shows nothing for pull requests without CI status, as GitHub and GitLab links are', () => {
    expect(ciStatusDisplay({ link_type: 'pull_request', provider_type: 'github' })).toBeNull();
    expect(ciStatusDisplay({ link_type: 'pull_request', ci_state: 'unknown' })).toBeNull();
  });

  it('shows nothing for branches and commits', () => {
    expect(ciStatusDisplay({ link_type: 'branch', ci_state: 'success' })).toBeNull();
    expect(ciStatusDisplay(null)).toBeNull();
  });
});
