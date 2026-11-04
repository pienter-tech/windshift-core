// Data handling for the issue sync label and milestone mapping editors.
//
// label_mappings is an array of { github_label, windshift_label_id } (the
// forge label name, whatever the provider). milestone_mappings is an object
// keyed by the forge milestone number as a string; for Gitea/Forgejo that
// number is the milestone ID.

// Forges report label colors as hex without '#'; CSS needs the '#'.
function forgeColor(color) {
  const hex = String(color ?? '').replace(/^#/, '');
  return /^[0-9a-f]{3}([0-9a-f]{3})?$/i.test(hex) ? `#${hex}` : '';
}

function positiveId(value) {
  const id = Number(value);
  return Number.isInteger(id) && id > 0 ? id : null;
}

/**
 * One row per repository label, with its mapped Windshift label ID or null.
 * Mapped names the repository no longer has follow as `missing` rows so
 * they stay visible and can be cleared.
 */
export function labelMappingRows(repoLabels, labelMappings) {
  const mappings = Array.isArray(labelMappings) ? labelMappings : [];
  const mappedId = new Map();
  for (const mapping of mappings) {
    const id = positiveId(mapping?.windshift_label_id);
    if (mapping?.github_label && id) mappedId.set(mapping.github_label, id);
  }

  const rows = [];
  const seen = new Set();
  for (const label of Array.isArray(repoLabels) ? repoLabels : []) {
    if (!label?.name || seen.has(label.name)) continue;
    seen.add(label.name);
    rows.push({
      name: label.name,
      color: forgeColor(label.color),
      windshiftLabelId: mappedId.get(label.name) ?? null,
      missing: false,
    });
  }
  for (const [name, id] of mappedId) {
    if (seen.has(name)) continue;
    rows.push({ name, color: '', windshiftLabelId: id, missing: true });
  }
  return rows;
}

/**
 * Map a forge label to a Windshift label, or remove its mapping when
 * windshiftLabelId is empty. Returns a new array with one entry per label.
 */
export function setLabelMapping(labelMappings, forgeLabel, windshiftLabelId) {
  const mappings = (Array.isArray(labelMappings) ? labelMappings : []).filter(
    (mapping) => mapping?.github_label && mapping.github_label !== forgeLabel
  );
  const id = positiveId(windshiftLabelId);
  if (!forgeLabel || !id) return mappings;
  return [...mappings, { github_label: forgeLabel, windshift_label_id: id }];
}

/**
 * One row per repository milestone, with its mapped Windshift milestone ID
 * or null. Mapped keys the repository no longer has follow as `missing` rows.
 */
export function milestoneMappingRows(repoMilestones, milestoneMappings) {
  const mappings =
    milestoneMappings && typeof milestoneMappings === 'object' && !Array.isArray(milestoneMappings)
      ? milestoneMappings
      : {};

  const rows = [];
  const seen = new Set();
  for (const milestone of Array.isArray(repoMilestones) ? repoMilestones : []) {
    if (milestone?.number == null) continue;
    const key = String(milestone.number);
    if (seen.has(key)) continue;
    seen.add(key);
    rows.push({
      key,
      title: milestone.title || `#${key}`,
      closed: milestone.state === 'closed',
      windshiftMilestoneId: positiveId(mappings[key]),
      missing: false,
    });
  }
  for (const [key, value] of Object.entries(mappings)) {
    const id = positiveId(value);
    if (seen.has(key) || !id) continue;
    rows.push({ key, title: `#${key}`, closed: false, windshiftMilestoneId: id, missing: true });
  }
  return rows;
}

/**
 * Map a forge milestone number to a Windshift milestone, or remove its
 * mapping when windshiftMilestoneId is empty. Returns a new object.
 */
export function setMilestoneMapping(milestoneMappings, forgeMilestoneKey, windshiftMilestoneId) {
  const base =
    milestoneMappings && typeof milestoneMappings === 'object' && !Array.isArray(milestoneMappings)
      ? milestoneMappings
      : {};
  const key = String(forgeMilestoneKey ?? '');
  const { [key]: _previous, ...rest } = base;
  const id = positiveId(windshiftMilestoneId);
  if (!key || !id) return rest;
  return { ...rest, [key]: id };
}
