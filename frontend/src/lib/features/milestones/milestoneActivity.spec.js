import { describe, expect, it } from 'vitest';
import { describeMilestoneActivity } from './milestoneActivity.js';

// Echo the key and params so assertions show which message was chosen.
const t = (key, params) => (params ? `${key} ${JSON.stringify(params)}` : key);
const formatDate = (value) => `date(${value})`;
const describe_ = (entry) => describeMilestoneActivity(entry, { t, formatDate });

const item = { id: 12, key: 'HUB-2', title: 'Ship it', workspace_id: 3 };

describe('describeMilestoneActivity', () => {
  it('describes milestone status and target-date changes with old and new values', () => {
    expect(
      describe_({
        type: 'milestone_status_changed',
        actor_kind: 'user',
        actor_name: 'Ada Lovelace',
        old_value: 'planning',
        new_value: 'in-progress',
      })
    ).toEqual({
      actor: 'Ada Lovelace',
      lead: 'milestones.activity.changedStatus {"from":"milestones.status.planning","to":"milestones.status.inProgress"}',
      target: null,
      trail: '',
    });
    expect(
      describe_({
        type: 'milestone_target_date_changed',
        actor_kind: 'system',
        new_value: '2026-12-01',
      }).lead
    ).toBe(
      'milestones.activity.changedTargetDate {"from":"milestones.activity.none","to":"date(2026-12-01)"}'
    );
  });

  it('links item entries to the item and page entries to the page', () => {
    const status = describe_({
      type: 'item_status_changed',
      actor_kind: 'user',
      actor_name: 'Bob',
      item,
      old_value: 'Open',
      new_value: 'Done',
    });
    expect(status.target).toEqual({ label: 'HUB-2 Ship it', href: '/workspaces/3/items/12' });
    expect(status.trail).toBe('milestones.activity.fromTo {"from":"Open","to":"Done"}');

    const removed = describe_({
      type: 'item_removed',
      actor_kind: 'user',
      actor_name: 'Bob',
      item,
    });
    expect(removed.lead).toBe('milestones.activity.removed');
    expect(removed.trail).toBe('milestones.activity.fromMilestone');

    const page = describe_({
      type: 'milestone_page_linked',
      actor_kind: 'user',
      actor_name: 'Ada',
      page: { id: 42, title: 'Spec', workspace_id: 3 },
    });
    expect(page.target).toEqual({ label: 'Spec', href: '/workspaces/3/pages/42' });
  });

  it('names system and unknown actors', () => {
    expect(describe_({ type: 'item_added', actor_kind: 'system', item }).actor).toBe(
      'milestones.activity.system'
    );
    expect(describe_({ type: 'item_comment_added', actor_kind: 'user', item }).actor).toBe(
      'milestones.activity.someone'
    );
  });
});
