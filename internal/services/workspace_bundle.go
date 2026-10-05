package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"windshift/internal/database"
	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/sanitize"
)

// Workspace bundles carry a workspace's distributable content — pages,
// labels, and typed seed items (master lists such as control catalogs,
// clause libraries, and hazard lists) — as one versioned, name-based
// document. Framework packs are a use of this format, not a parallel path.

const (
	WorkspaceBundleSchemaVersion = 1
	WorkspaceBundleKind          = "windshift.workspace-bundle"
)

type WorkspaceBundle struct {
	SchemaVersion int                   `json:"schema_version"`
	Kind          string                `json:"kind"`
	ExportedAt    time.Time             `json:"exported_at"`
	ExportedBy    *ConfigSetExportBy    `json:"exported_by,omitempty"`
	Source        WorkspaceBundleSource `json:"source"`
	// ConfigurationSet optionally embeds the source workspace's attached
	// configuration-set template. Import applies it through the existing
	// config-set import path and attaches it to the target workspace before
	// any content is created.
	ConfigurationSet *ConfigSetTemplate     `json:"configuration_set_template,omitempty"`
	Payload          WorkspaceBundlePayload `json:"payload"`
	// Warnings records content the export could not represent portably (for
	// example an item whose item type was deleted). The bundle still imports;
	// the warning tells the operator what was left behind.
	Warnings []string `json:"warnings,omitempty"`
}

type WorkspaceBundleSource struct {
	WorkspaceName string `json:"workspace_name,omitempty"`
}

type WorkspaceBundlePayload struct {
	// PageLabels is the workspace's page-label catalog (name + color).
	PageLabels []WorkspaceBundlePageLabel `json:"page_labels,omitempty"`
	Pages      []WorkspaceBundlePage      `json:"pages,omitempty"`
	// Labels is the item-label catalog referenced by bundled items.
	Labels    []WorkspaceBundleLabel    `json:"labels,omitempty"`
	Items     []WorkspaceBundleItem     `json:"items,omitempty"`
	ItemLinks []WorkspaceBundleItemLink `json:"item_links,omitempty"`
}

type WorkspaceBundlePageLabel struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// WorkspaceBundlePage is one content page. ParentRef references another
// page's Ref in the same bundle; empty for root pages. Pages are listed
// parents-first in sibling order so a sequential import rebuilds the tree.
type WorkspaceBundlePage struct {
	Ref       string   `json:"ref"`
	Title     string   `json:"title"`
	ParentRef string   `json:"parent_ref,omitempty"`
	Content   string   `json:"content,omitempty"`
	Labels    []string `json:"labels,omitempty"`
}

type WorkspaceBundleLabel struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// WorkspaceBundleItem is one seed item. ItemTypeName, PriorityName, label
// names, and FieldValues keys (custom field names) are all name-based;
// AssigneeEmail is a user reference resolved at import time.
type WorkspaceBundleItem struct {
	Ref           string         `json:"ref"`
	Title         string         `json:"title"`
	Description   string         `json:"description,omitempty"`
	ItemTypeName  string         `json:"item_type_name"`
	PriorityName  string         `json:"priority_name,omitempty"`
	AssigneeEmail string         `json:"assignee_email,omitempty"`
	ParentRef     string         `json:"parent_ref,omitempty"`
	Labels        []string       `json:"labels,omitempty"`
	FieldValues   map[string]any `json:"field_values,omitempty"`
}

type WorkspaceBundleItemLink struct {
	SourceRef    string `json:"source_ref"`
	TargetRef    string `json:"target_ref"`
	LinkTypeName string `json:"link_type_name"`
}

// SanitizeWorkspaceBundle scrubs an uploaded bundle with the same field
// policies as the config-set template importer. It returns an error when the
// bundle is structurally invalid (missing refs, missing item type names).
func SanitizeWorkspaceBundle(bundle *WorkspaceBundle) error {
	if bundle == nil {
		return errors.New("import: empty bundle")
	}
	if bundle.Kind != WorkspaceBundleKind {
		return fmt.Errorf("import: unsupported kind %q (want %q)", bundle.Kind, WorkspaceBundleKind)
	}
	if bundle.SchemaVersion != WorkspaceBundleSchemaVersion {
		return fmt.Errorf("import: unsupported schema_version %d (want %d)", bundle.SchemaVersion, WorkspaceBundleSchemaVersion)
	}
	if bundle.ExportedBy != nil {
		SanitizeConfigSetExportBy(bundle.ExportedBy)
	}
	sanitize.Apply(&bundle.Source.WorkspaceName, sanitize.PlainTextField)
	p := &bundle.Payload

	pageLabels := make(map[string]bool, len(p.PageLabels))
	for i := range p.PageLabels {
		pl := &p.PageLabels[i]
		sanitize.ApplyAll(
			sanitize.Pair{Target: &pl.Name, Policy: sanitize.ShortIdentifier},
			sanitize.Pair{Target: &pl.Color, Policy: sanitize.ShortIdentifier},
		)
		if pl.Name == "" {
			return fmt.Errorf("page_labels[%d]: name is required", i)
		}
		if pageLabels[pl.Name] {
			return fmt.Errorf("page_labels[%d]: duplicate name %q", i, pl.Name)
		}
		pageLabels[pl.Name] = true
	}

	refs := make(map[string]bool, len(p.Pages))
	for i := range p.Pages {
		page := &p.Pages[i]
		sanitize.ApplyAll(
			sanitize.Pair{Target: &page.Ref, Policy: sanitize.ShortIdentifier},
			sanitize.Pair{Target: &page.Title, Policy: sanitize.PlainTextField},
			sanitize.Pair{Target: &page.Content, Policy: sanitize.LongDocument},
		)
		if page.Ref == "" {
			return fmt.Errorf("pages[%d]: ref is required", i)
		}
		if refs[page.Ref] {
			return fmt.Errorf("pages[%d]: duplicate ref %q", i, page.Ref)
		}
		refs[page.Ref] = true
		for j := range page.Labels {
			sanitize.Apply(&page.Labels[j], sanitize.ShortIdentifier)
			if page.Labels[j] == "" {
				return fmt.Errorf("pages[%d]: empty label name", i)
			}
		}
	}

	itemLabels := make(map[string]bool, len(p.Labels))
	for i := range p.Labels {
		l := &p.Labels[i]
		sanitize.ApplyAll(
			sanitize.Pair{Target: &l.Name, Policy: sanitize.ShortIdentifier},
			sanitize.Pair{Target: &l.Color, Policy: sanitize.ShortIdentifier},
		)
		if l.Name == "" {
			return fmt.Errorf("labels[%d]: name is required", i)
		}
		if itemLabels[l.Name] {
			return fmt.Errorf("labels[%d]: duplicate name %q", i, l.Name)
		}
		itemLabels[l.Name] = true
	}

	itemRefs := make(map[string]bool, len(p.Items))
	for i := range p.Items {
		item := &p.Items[i]
		sanitize.ApplyAll(
			sanitize.Pair{Target: &item.Ref, Policy: sanitize.ShortIdentifier},
			sanitize.Pair{Target: &item.Title, Policy: sanitize.PlainTextField},
			sanitize.Pair{Target: &item.Description, Policy: sanitize.RichText},
			sanitize.Pair{Target: &item.ItemTypeName, Policy: sanitize.PlainTextField},
			sanitize.Pair{Target: &item.PriorityName, Policy: sanitize.PlainTextField},
			sanitize.Pair{Target: &item.AssigneeEmail, Policy: sanitize.ShortIdentifier},
			sanitize.Pair{Target: &item.ParentRef, Policy: sanitize.ShortIdentifier},
		)
		if item.Ref == "" || item.ItemTypeName == "" {
			return fmt.Errorf("items[%d]: ref and item_type_name are required", i)
		}
		if itemRefs[item.Ref] {
			return fmt.Errorf("items[%d]: duplicate ref %q", i, item.Ref)
		}
		itemRefs[item.Ref] = true
		for j := range item.Labels {
			sanitize.Apply(&item.Labels[j], sanitize.ShortIdentifier)
			if item.Labels[j] == "" {
				return fmt.Errorf("items[%d]: empty label name", i)
			}
		}
		if err := sanitize.ValidateJSONPayload(
			fmt.Sprintf("items[%d].field_values", i), mustMarshalJSON(item.FieldValues),
		); err != nil {
			return err
		}
	}

	for i := range p.ItemLinks {
		link := &p.ItemLinks[i]
		sanitize.ApplyAll(
			sanitize.Pair{Target: &link.SourceRef, Policy: sanitize.ShortIdentifier},
			sanitize.Pair{Target: &link.TargetRef, Policy: sanitize.ShortIdentifier},
			sanitize.Pair{Target: &link.LinkTypeName, Policy: sanitize.PlainTextField},
		)
		if link.SourceRef == "" || link.TargetRef == "" || link.LinkTypeName == "" {
			return fmt.Errorf("item_links[%d]: source_ref, target_ref, and link_type_name are required", i)
		}
		if !itemRefs[link.SourceRef] || !itemRefs[link.TargetRef] {
			return fmt.Errorf("item_links[%d]: source_ref and target_ref must reference bundled items", i)
		}
	}
	return nil
}

func mustMarshalJSON(v any) string {
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	if len(raw) > workspaceBundleFieldValuesMaxBytes {
		return ""
	}
	return string(raw)
}

// workspaceBundleFieldValuesMaxBytes bounds one item's serialized field
// values, mirroring the config-set importer's condition-config cap.
const workspaceBundleFieldValuesMaxBytes = 64 * 1024

// ---- export -----------------------------------------------------------------

// WorkspaceBundleExportService produces a portable bundle from a workspace's
// live content. Read-only.
type WorkspaceBundleExportService struct {
	db               database.Database
	configSetExports *ConfigSetExportService
}

func NewWorkspaceBundleExportService(db database.Database, configSetExports *ConfigSetExportService) *WorkspaceBundleExportService {
	return &WorkspaceBundleExportService{db: db, configSetExports: configSetExports}
}

// Export walks the workspace's non-archived pages (parents first, sibling
// order preserved), its labels, its items, and its item links. The
// workspace's attached configuration set is embedded as a template when one
// is attached and exportable; the default configuration set is never
// embedded.
func (s *WorkspaceBundleExportService) Export(ctx context.Context, workspaceID int, exportedBy *ConfigSetExportBy) (*WorkspaceBundle, error) {
	bundle := &WorkspaceBundle{
		SchemaVersion: WorkspaceBundleSchemaVersion,
		Kind:          WorkspaceBundleKind,
		ExportedAt:    time.Now().UTC(),
		ExportedBy:    exportedBy,
	}
	var workspaceName string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id = ?`, workspaceID).Scan(&workspaceName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorkspaceBundleWorkspaceNotFound
		}
		return nil, err
	}
	bundle.Source = WorkspaceBundleSource{WorkspaceName: workspaceName}

	pages, err := s.exportPages(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("export pages: %w", err)
	}
	bundle.Payload.Pages = pages
	bundle.Payload.PageLabels, err = s.exportPageLabels(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("export page labels: %w", err)
	}

	items, usedItemLabels, idToRef, warnings, err := s.exportItems(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("export items: %w", err)
	}
	bundle.Payload.Items = items
	bundle.Warnings = warnings
	if len(usedItemLabels) > 0 {
		bundle.Payload.Labels, err = s.exportItemLabelCatalog(ctx, usedItemLabels)
		if err != nil {
			return nil, fmt.Errorf("export labels: %w", err)
		}
	}
	bundle.Payload.ItemLinks, err = s.exportItemLinks(ctx, workspaceID, idToRef)
	if err != nil {
		return nil, fmt.Errorf("export item links: %w", err)
	}

	bundle.ConfigurationSet = s.exportEmbeddedConfigurationSet(ctx, workspaceID, exportedBy)
	return bundle, nil
}

// AuditExport records a workspace bundle export so bulk content extraction
// stays visible in the security log.
func (s *WorkspaceBundleExportService) AuditExport(actor AuditActor, workspaceID int) {
	emitServiceAudit(s.db, actor, logger.ActionWorkspaceBundleExport, logger.ResourceWorkspace, &workspaceID, "", nil)
}

var ErrWorkspaceBundleWorkspaceNotFound = errors.New("workspace bundle: workspace not found")

// exportPageRows walks the page tree depth-first with siblings in display
// order, so the bundle lists parents before children.
func (s *WorkspaceBundleExportService) exportPages(ctx context.Context, workspaceID int) ([]WorkspaceBundlePage, error) {
	type pageRow struct {
		id       int
		parentID *int
		title    string
		content  string
		frac     string
		isHome   bool
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, parent_id, title, content, COALESCE(frac_index, ''), is_home
		FROM pages
		WHERE workspace_id = ? AND archived_at IS NULL
		ORDER BY id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	byParent := map[int][]pageRow{}
	root := []pageRow{}
	for rows.Next() {
		var r pageRow
		var parentID sql.NullInt64
		if err := rows.Scan(&r.id, &parentID, &r.title, &r.content, &r.frac, &r.isHome); err != nil {
			return nil, err
		}
		if parentID.Valid {
			r.parentID = new(int)
			*r.parentID = int(parentID.Int64)
		}
		if r.isHome {
			// The home page is workspace scaffolding, not distributable
			// content; a target workspace always has its own.
			continue
		}
		if r.parentID == nil {
			root = append(root, r)
		} else {
			byParent[*r.parentID] = append(byParent[*r.parentID], r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, siblings := range byParent {
		sort.SliceStable(siblings, func(i, j int) bool {
			if siblings[i].frac != siblings[j].frac {
				return siblings[i].frac < siblings[j].frac
			}
			return siblings[i].id < siblings[j].id
		})
	}
	sort.SliceStable(root, func(i, j int) bool {
		if root[i].frac != root[j].frac {
			return root[i].frac < root[j].frac
		}
		return root[i].id < root[j].id
	})

	labelNames, err := s.pageLabelNames(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	var out []WorkspaceBundlePage
	counter := 0
	var walk func(r pageRow, parentRef string) error
	walk = func(r pageRow, parentRef string) error {
		counter++
		ref := fmt.Sprintf("page-%d", counter)
		entry := WorkspaceBundlePage{Ref: ref, Title: r.title, ParentRef: parentRef, Content: r.content}
		entry.Labels = labelNames[r.id]
		out = append(out, entry)
		for _, child := range byParent[r.id] {
			if err := walk(child, ref); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range root {
		if err := walk(r, ""); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *WorkspaceBundleExportService) pageLabelNames(ctx context.Context, workspaceID int) (map[int][]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pla.page_id, pl.name
		FROM page_label_assignments pla
		JOIN page_labels pl ON pl.id = pla.page_label_id
		JOIN pages p ON p.id = pla.page_id
		WHERE p.workspace_id = ? AND p.archived_at IS NULL
		ORDER BY pl.name
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int][]string{}
	for rows.Next() {
		var pageID int
		var name string
		if err := rows.Scan(&pageID, &name); err != nil {
			return nil, err
		}
		out[pageID] = append(out[pageID], name)
	}
	return out, rows.Err()
}

func (s *WorkspaceBundleExportService) exportPageLabels(ctx context.Context, workspaceID int) ([]WorkspaceBundlePageLabel, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, COALESCE(color, '') FROM page_labels WHERE workspace_id = ? ORDER BY name
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []WorkspaceBundlePageLabel
	for rows.Next() {
		var l WorkspaceBundlePageLabel
		if err := rows.Scan(&l.Name, &l.Color); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// bundleExportItem is one item during export, before refs are assigned.
type bundleExportItem struct {
	id          int
	parentID    sql.NullInt64
	item        WorkspaceBundleItem
	fieldValues map[string]any
}

// exportItems dumps the workspace's items with name-based references. Items
// are emitted parents-first regardless of id order (re-parenting can put a
// child before its parent), and custom-field values are encoded by name or
// option label so the bundle round-trips across instances. The returned set is
// the item label names actually used; the id → ref table maps links onto refs.
// Items whose item type was deleted are skipped and reported in warnings
// instead of being dropped silently by an inner join.
func (s *WorkspaceBundleExportService) exportItems(ctx context.Context, workspaceID int) (items []WorkspaceBundleItem, usedLabels map[string]bool, idToRef map[int]string, warnings []string, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.title, COALESCE(i.description, ''), COALESCE(it.name, '') AS item_type_name,
		       COALESCE(p.name, '') AS priority_name, COALESCE(u.email, '') AS assignee_email,
		       i.custom_field_values AS custom_field_values, i.parent_id
		FROM items i
		LEFT JOIN item_types it ON it.id = i.item_type_id
		LEFT JOIN priorities p ON p.id = i.priority_id
		LEFT JOIN users u ON u.id = i.assignee_id
		WHERE i.workspace_id = ?
		ORDER BY i.id
	`, workspaceID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var ordered []*bundleExportItem
	byID := map[int]*bundleExportItem{}
	for rows.Next() {
		var (
			r     bundleExportItem
			cfRaw sql.NullString
		)
		if err := rows.Scan(&r.id, &r.item.Title, &r.item.Description, &r.item.ItemTypeName,
			&r.item.PriorityName, &r.item.AssigneeEmail, &cfRaw, &r.parentID); err != nil {
			return nil, nil, nil, nil, err
		}
		if r.item.ItemTypeName == "" {
			warnings = append(warnings, fmt.Sprintf("item %d (%s) has no item type and was skipped", r.id, r.item.Title))
			continue
		}
		if cfRaw.Valid && cfRaw.String != "" {
			values := map[string]any{}
			if err := json.Unmarshal([]byte(cfRaw.String), &values); err == nil && len(values) > 0 {
				r.fieldValues = values
			}
		}
		byID[r.id] = &r
		ordered = append(ordered, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, nil, err
	}

	// Depth-first emission keeps parents before their children while preserving
	// id order among siblings. A cycle cannot be emitted twice; leftovers are
	// appended in id order so a malformed tree still exports.
	var out []*bundleExportItem
	emitted := map[int]bool{}
	var emit func(r *bundleExportItem)
	emit = func(r *bundleExportItem) {
		if emitted[r.id] {
			return
		}
		emitted[r.id] = true
		if r.parentID.Valid {
			if parent, ok := byID[int(r.parentID.Int64)]; ok {
				emit(parent)
			}
		}
		out = append(out, r)
	}
	for _, r := range ordered {
		emit(r)
	}

	idToRef = map[int]string{}
	for i, r := range out {
		r.item.Ref = fmt.Sprintf("item-%d", i+1)
		idToRef[r.id] = r.item.Ref
	}
	for _, r := range out {
		if r.parentID.Valid {
			if ref, ok := idToRef[int(r.parentID.Int64)]; ok {
				r.item.ParentRef = ref
			}
		}
	}

	fieldDefs, err := s.loadCustomFieldDefs(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	labelNames, err := s.itemLabelNames(ctx, workspaceID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	usedLabels = map[string]bool{}
	for _, r := range out {
		if len(r.fieldValues) > 0 {
			rewritten := make(map[string]any, len(r.fieldValues))
			for key, value := range r.fieldValues {
				def, ok := fieldDefs[key]
				if !ok {
					continue // orphaned value for a deleted field
				}
				encoded, err := encodeChoiceFieldValue(def, value)
				if err != nil {
					return nil, nil, nil, nil, err
				}
				rewritten[def.Name] = encoded
			}
			r.item.FieldValues = rewritten
		}
		if names := labelNames[r.id]; len(names) > 0 {
			r.item.Labels = names
			for _, n := range names {
				usedLabels[n] = true
			}
		}
		items = append(items, r.item)
	}
	return items, usedLabels, idToRef, warnings, nil
}

// customFieldDef is the subset of a custom-field definition needed to encode
// and decode choice values.
type customFieldDef struct {
	ID        int
	Name      string
	FieldType string
	Options   string
}

func (s *WorkspaceBundleExportService) loadCustomFieldDefs(ctx context.Context) (map[string]customFieldDef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, field_type, COALESCE(options, '') FROM custom_field_definitions
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]customFieldDef{}
	for rows.Next() {
		var def customFieldDef
		if err := rows.Scan(&def.ID, &def.Name, &def.FieldType, &def.Options); err != nil {
			return nil, err
		}
		out[strconv.Itoa(def.ID)] = def
	}
	return out, rows.Err()
}

// encodeChoiceFieldValue turns a select/multiselect option id (or list of ids)
// into its option label(s) so the bundle is portable across instances whose
// option ids differ. Non-choice values pass through unchanged.
func encodeChoiceFieldValue(def customFieldDef, value any) (any, error) {
	switch def.FieldType {
	case "select":
		id, ok := coerceFieldOptionID(value)
		if !ok {
			return value, nil
		}
		label, ok := selectOptionLabel(def.Options, id)
		if !ok {
			return nil, fmt.Errorf("custom field %q: option id %d has no label", def.Name, id)
		}
		return label, nil
	case "multiselect":
		items, ok := value.([]any)
		if !ok {
			return value, nil
		}
		labels := make([]any, 0, len(items))
		for _, item := range items {
			id, ok := coerceFieldOptionID(item)
			if !ok {
				return value, nil
			}
			label, ok := selectOptionLabel(def.Options, id)
			if !ok {
				return nil, fmt.Errorf("custom field %q: option id %d has no label", def.Name, id)
			}
			labels = append(labels, label)
		}
		return labels, nil
	default:
		return value, nil
	}
}

// coerceFieldOptionID accepts JSON numbers and legacy numeric strings.
func coerceFieldOptionID(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case string:
		n, err := strconv.Atoi(typed)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func selectOptionLabel(optionsJSON string, id int) (string, bool) {
	opts, err := models.ParseSelectOptions(optionsJSON)
	if err != nil {
		return "", false
	}
	for _, item := range opts.Items {
		if item.ID == id {
			return item.Label, true
		}
	}
	return "", false
}

func (s *WorkspaceBundleExportService) itemLabelNames(ctx context.Context, workspaceID int) (map[int][]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT il.item_id, l.name
		FROM item_labels il
		JOIN labels l ON l.id = il.label_id
		JOIN items i ON i.id = il.item_id
		WHERE i.workspace_id = ?
		ORDER BY l.name
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byItemID := map[int][]string{}
	for rows.Next() {
		var itemID int
		var name string
		if err := rows.Scan(&itemID, &name); err != nil {
			return nil, err
		}
		byItemID[itemID] = append(byItemID[itemID], name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return byItemID, nil
}

func (s *WorkspaceBundleExportService) exportItemLabelCatalog(ctx context.Context, usedNames map[string]bool) ([]WorkspaceBundleLabel, error) {
	names := make([]string, 0, len(usedNames))
	for name := range usedNames {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]WorkspaceBundleLabel, 0, len(names))
	for _, name := range names {
		var color string
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(color, '') FROM labels WHERE name = ?`, name).Scan(&color); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		out = append(out, WorkspaceBundleLabel{Name: name, Color: color})
	}
	return out, nil
}

// exportItemLinks maps the workspace's item↔item links onto in-bundle refs.
// Links whose endpoints were not exported are skipped.
func (s *WorkspaceBundleExportService) exportItemLinks(ctx context.Context, workspaceID int, idToRef map[int]string) ([]WorkspaceBundleItemLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT il.source_id, il.target_id, lt.name
		FROM item_links il
		JOIN link_types lt ON lt.id = il.link_type_id
		JOIN items si ON si.id = il.source_id AND il.source_type = 'item'
		JOIN items ti ON ti.id = il.target_id AND il.target_type = 'item'
		WHERE si.workspace_id = ? AND ti.workspace_id = ?
		ORDER BY il.id
	`, workspaceID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []WorkspaceBundleItemLink
	for rows.Next() {
		var (
			sourceID, targetID int
			l                  WorkspaceBundleItemLink
		)
		if err := rows.Scan(&sourceID, &targetID, &l.LinkTypeName); err != nil {
			return nil, err
		}
		sourceRef, ok := idToRef[sourceID]
		if !ok {
			continue
		}
		targetRef, ok := idToRef[targetID]
		if !ok {
			continue
		}
		l.SourceRef = sourceRef
		l.TargetRef = targetRef
		out = append(out, l)
	}
	return out, rows.Err()
}

// exportEmbeddedConfigurationSet embeds the workspace's attached
// configuration-set template when one is attached and exportable. A missing
// or default (non-portable) configuration set simply leaves the bundle
// content-only.
func (s *WorkspaceBundleExportService) exportEmbeddedConfigurationSet(ctx context.Context, workspaceID int, exportedBy *ConfigSetExportBy) *ConfigSetTemplate {
	var configSetID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT configuration_set_id FROM workspace_configuration_sets WHERE workspace_id = ?
	`, workspaceID).Scan(&configSetID); err != nil || !configSetID.Valid {
		return nil
	}
	tpl, err := s.configSetExports.Export(ctx, int(configSetID.Int64), exportedBy)
	if err != nil {
		// Default configuration sets are intentionally not portable.
		return nil
	}
	return tpl
}
