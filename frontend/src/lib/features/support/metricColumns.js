// Stable column definitions for support metrics exports (WI-1133). These
// headers are an API contract: charts and CSV must stay in sync with them.
export const supportMetricColumns = [
  { key: 'bucket_start', label: 'Date' },
  { key: 'created', label: 'Created' },
  { key: 'resolved', label: 'Resolved' },
  { key: 'reopened', label: 'Reopened' },
  { key: 'backlog_end', label: 'Backlog at end' },
  { key: 'first_response_p50_ms', label: 'First response p50 (ms)' },
  { key: 'resolution_p50_ms', label: 'Resolution p50 (ms)' },
];

function csvCell(value) {
  if (value === null || value === undefined) return '';
  const text = String(value);
  if (/[",\n]/.test(text)) {
    return `"${text.replace(/"/g, '""')}"`;
  }
  return text;
}

export function supportMetricsToCSV(doc) {
  const rows = doc?.buckets ?? [];
  const lines = [supportMetricColumns.map((column) => csvCell(column.label)).join(',')];
  for (const bucket of rows) {
    lines.push(supportMetricColumns.map((column) => csvCell(bucket[column.key])).join(','));
  }
  return lines.join('\n');
}

export function downloadSupportMetricsCSV(doc, filename = 'support-metrics.csv') {
  const blob = new Blob([supportMetricsToCSV(doc)], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}
