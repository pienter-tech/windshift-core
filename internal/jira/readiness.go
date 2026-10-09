package jira

import "strings"

// readiness.go classifies how faithfully a Jira instance migrates into
// Windshift. It is a pure layer on top of field_mapper.go — no I/O — so the
// handler can feed it sampled issue data and the test suite can exercise the
// rules directly. The same clean/lossy/blocked taxonomy backs the
// "Migrating Jira to Windshift" whitepaper, so the two never drift apart.

// Severity classifies how faithfully a Jira concept survives import.
type Severity string

const (
	// SeverityClean: imported 1:1 with full fidelity.
	SeverityClean Severity = "clean"
	// SeverityLossy: imported but degraded — partially dropped, flattened, or
	// deferred to a later import phase.
	SeverityLossy Severity = "lossy"
	// SeverityBlocked: no mapping exists; the data is skipped entirely.
	SeverityBlocked Severity = "blocked"
)

// Finding is a single migration-fidelity observation about one Jira concept.
// UsageCount is how many sampled issues actually exercise it; 0 means the
// finding is schema-only (the concept is defined but unused in the sample).
type Finding struct {
	Entity     string   `json:"entity"`
	Category   string   `json:"category"`
	Severity   Severity `json:"severity"`
	JiraType   string   `json:"jira_type,omitempty"`
	Reason     string   `json:"reason"`
	UsageCount int      `json:"usage_count"`
}

// fieldTypeSeverity maps a resolved Windshift field type to its migration
// fidelity. Native scalar, user, asset, and option mappings land cleanly.
// Opaque app-owned structures are deliberately preserved as JSON text and are
// classified separately by ClassifyField.
func fieldTypeSeverity(t WindshiftFieldType) (severity Severity, reason string) {
	switch t {
	case FieldTypeUnmapped:
		return SeverityBlocked, "No Windshift equivalent for this field type; it is skipped."
	case FieldTypeUser, FieldTypeMultiUser:
		return SeverityClean, "User-valued field; resolved to Windshift users by account/email."
	case FieldTypeAsset:
		return SeverityClean, "Backed by Jira Assets/Insight; the referenced objects import into Windshift asset sets and the field resolves to them."
	default:
		// text, textarea, number, select, multiselect, date, milestone, iteration
		return SeverityClean, "Field type maps to a Windshift custom field and its value is written during import."
	}
}

// ClassifyField turns a field-mapping suggestion (from SuggestFieldMappings)
// plus its observed usage into a Finding.
func ClassifyField(s FieldMappingSuggestion, usageCount int) Finding {
	sev, reason := fieldTypeSeverity(s.WindshiftFieldType)
	switch s.JiraFieldType {
	case "com.pyxis.greenhopper.jira:gh-lexo-rank":
		sev = SeverityClean
		reason = "Jira Rank controls import order; Windshift generates increasing fractional indexes in that order."
	case "com.atlassian.servicedesk:vp-origin":
		sev = SeverityClean
		reason = "Jira Request Type maps to the item's first-class portal request type."
	}
	if s.PreserveRaw {
		sev = SeverityLossy
		reason = "No proven native Windshift shape; the complete Jira value is preserved as JSON text."
	}
	if s.WindshiftFieldType == FieldTypeDate &&
		(strings.Contains(strings.ToLower(s.JiraFieldType), "datetime") ||
			strings.Contains(s.Notes, "time-of-day editing is lossy")) {
		sev = SeverityLossy
		reason = "Jira datetime values are stored as calendar dates with the original timestamp retained in item metadata, but Windshift exposes calendar-date editing and rendering."
	}
	if s.Notes != "" {
		reason += " " + s.Notes
	}
	return Finding{
		Entity:     "Custom field: " + s.JiraFieldName,
		Category:   "custom_field",
		Severity:   sev,
		JiraType:   s.JiraFieldType,
		Reason:     reason,
		UsageCount: usageCount,
	}
}

// supportedADFNodes are rendered faithfully by the ADF-to-Markdown converter;
// other nodes are flattened and flagged as lossy. Include container children
// walked positionally so supported structures are not falsely flagged.
var supportedADFNodes = map[string]bool{
	// Document + block structure.
	"doc":          true,
	"paragraph":    true,
	"heading":      true,
	"blockquote":   true,
	"codeBlock":    true,
	"rule":         true,
	"panel":        true,
	"expand":       true,
	"nestedExpand": true,
	// Lists (+ their items).
	"bulletList":  true,
	"orderedList": true,
	"listItem":    true,
	"taskList":    true,
	"taskItem":    true,
	// Tables (+ their rows/cells).
	"table":       true,
	"tableRow":    true,
	"tableCell":   true,
	"tableHeader": true,
	// Media (rendered as a placeholder; the file itself imports as an attachment).
	"mediaSingle": true,
	"mediaGroup":  true,
	"media":       true,
	// Inline content.
	"text":       true,
	"hardBreak":  true,
	"mention":    true,
	"status":     true,
	"emoji":      true,
	"date":       true,
	"inlineCard": true,
	"blockCard":  true,
}

// ScanADF walks an ADF document (the shape Jira returns for description and
// comment bodies) and returns a histogram of node `type` → occurrence count.
// A nil or plain-string body yields an empty map.
func ScanADF(adf any) map[string]int {
	counts := make(map[string]int)
	var walk func(node any)
	walk = func(node any) {
		m, ok := node.(map[string]any)
		if !ok {
			return
		}
		if t, _ := m["type"].(string); t != "" {
			counts[t]++
		}
		if content, ok := m["content"].([]any); ok {
			for _, c := range content {
				walk(c)
			}
		}
	}
	walk(adf)
	return counts
}

// UnsupportedADFNodes returns the subset of a ScanADF histogram whose node
// types the importer cannot render with full fidelity, with their counts.
func UnsupportedADFNodes(counts map[string]int) map[string]int {
	out := make(map[string]int)
	for t, n := range counts {
		if !supportedADFNodes[t] {
			out[t] = n
		}
	}
	return out
}

// severityWeight is the fraction of fidelity each severity retains.
func severityWeight(s Severity) float64 {
	switch s {
	case SeverityClean:
		return 1.0
	case SeverityLossy:
		return 0.5
	default: // blocked
		return 0.0
	}
}

// ScoreFindings produces a 0–100 readiness score and a per-severity tally of
// distinct findings. Each finding is weighted by its usage count (floored at 1
// so schema-only findings still register) so a lossy field touched by one
// issue barely dents the score while one touched by thousands dominates. An
// empty finding set scores 100 — nothing to lose.
func ScoreFindings(findings []Finding) (score int, bySeverity map[Severity]int) {
	bySeverity = map[Severity]int{SeverityClean: 0, SeverityLossy: 0, SeverityBlocked: 0}
	var weighted, total float64
	for _, f := range findings {
		bySeverity[f.Severity]++
		w := float64(f.UsageCount)
		if w < 1 {
			w = 1
		}
		total += w
		weighted += w * severityWeight(f.Severity)
	}
	if total == 0 {
		return 100, bySeverity
	}
	return int((weighted/total)*100 + 0.5), bySeverity
}
