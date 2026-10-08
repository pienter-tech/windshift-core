import { describe, expect, it } from 'vitest';
import {
  labelMappingRows,
  milestoneMappingRows,
  setLabelMapping,
  setMilestoneMapping,
} from './issueSyncMappings.js';

describe('labelMappingRows', () => {
  it('lists every repository label with its mapped Windshift label', () => {
    const rows = labelMappingRows(
      [
        { id: 1, name: 'bug', color: 'ee0701' },
        { id: 2, name: 'feature', color: '' },
        { id: 3, name: 'docs', color: '#0075ca' },
        { id: 4, name: 'odd', color: 'not-a-color' },
      ],
      [{ github_label: 'bug', windshift_label_id: 7 }]
    );
    expect(rows).toEqual([
      { name: 'bug', color: '#ee0701', windshiftLabelId: 7, missing: false },
      { name: 'feature', color: '', windshiftLabelId: null, missing: false },
      { name: 'docs', color: '#0075ca', windshiftLabelId: null, missing: false },
      { name: 'odd', color: '', windshiftLabelId: null, missing: false },
    ]);
  });

  it('keeps mappings for labels the repository no longer has as missing rows', () => {
    const rows = labelMappingRows(
      [{ id: 1, name: 'bug' }],
      [
        { github_label: 'gone', windshift_label_id: 3 },
        { github_label: 'broken', windshift_label_id: 0 },
      ]
    );
    expect(rows.map((row) => [row.name, row.windshiftLabelId, row.missing])).toEqual([
      ['bug', null, false],
      ['gone', 3, true],
    ]);
  });

  it('tolerates missing data', () => {
    expect(labelMappingRows(null, null)).toEqual([]);
  });
});

describe('setLabelMapping', () => {
  it('adds, replaces, and removes a single mapping per label', () => {
    let mappings = setLabelMapping([], 'bug', 7);
    expect(mappings).toEqual([{ github_label: 'bug', windshift_label_id: 7 }]);

    mappings = setLabelMapping(mappings, 'bug', '9');
    expect(mappings).toEqual([{ github_label: 'bug', windshift_label_id: 9 }]);

    mappings = setLabelMapping(mappings, 'feature', 4);
    mappings = setLabelMapping(mappings, 'bug', null);
    expect(mappings).toEqual([{ github_label: 'feature', windshift_label_id: 4 }]);
  });

  it('does not mutate the input', () => {
    const original = [{ github_label: 'bug', windshift_label_id: 7 }];
    setLabelMapping(original, 'bug', null);
    expect(original).toEqual([{ github_label: 'bug', windshift_label_id: 7 }]);
  });
});

describe('milestoneMappingRows', () => {
  it('keys repository milestones by number and reads their mapping', () => {
    const rows = milestoneMappingRows(
      [
        { id: 12, number: 12, title: 'v1', state: 'open' },
        { id: 13, number: 13, title: 'v0', state: 'closed' },
      ],
      { 12: 5 }
    );
    expect(rows).toEqual([
      { key: '12', title: 'v1', closed: false, windshiftMilestoneId: 5, missing: false },
      { key: '13', title: 'v0', closed: true, windshiftMilestoneId: null, missing: false },
    ]);
  });

  it('keeps mappings for milestones the repository no longer has as missing rows', () => {
    const rows = milestoneMappingRows([], { 4: 2 });
    expect(rows).toEqual([
      { key: '4', title: '#4', closed: false, windshiftMilestoneId: 2, missing: true },
    ]);
  });

  it('tolerates missing data', () => {
    expect(milestoneMappingRows(undefined, [])).toEqual([]);
  });
});

describe('setMilestoneMapping', () => {
  it('sets and clears a mapping by milestone number', () => {
    let mappings = setMilestoneMapping({}, 12, 5);
    expect(mappings).toEqual({ 12: 5 });

    mappings = setMilestoneMapping(mappings, '13', 6);
    mappings = setMilestoneMapping(mappings, 12, null);
    expect(mappings).toEqual({ 13: 6 });
  });

  it('does not mutate the input', () => {
    const original = { 12: 5 };
    setMilestoneMapping(original, 12, null);
    expect(original).toEqual({ 12: 5 });
  });
});
