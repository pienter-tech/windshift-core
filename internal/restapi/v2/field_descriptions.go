package v2

import "reflect"

// fieldDescriptionKey names one JSON field of one request or response type.
type fieldDescriptionKey struct {
	owner reflect.Type
	field string
}

// fieldDescriptions overrides the generated OpenAPI description of single
// fields. Keying by the owning type leaves the same JSON name on other types
// at the generator's generic description.
var fieldDescriptions = map[fieldDescriptionKey]string{
	{reflect.TypeFor[milestoneListEntry](), "last_updated_at"}: "When the milestone or anything in it last changed: the latest of the milestone's own updated_at, its description, status, and target date changes, its comments (including edits), and its page links and unlinks, and, counting only items in workspaces the caller can access, member item updates, comments on member items (including edits), and items added to or removed from the milestone.",
}

// FieldDescription returns the OpenAPI description override for the JSON
// field of struct type owner, if one is declared.
func FieldDescription(owner reflect.Type, field string) (string, bool) {
	description, ok := fieldDescriptions[fieldDescriptionKey{owner: owner, field: field}]
	return description, ok
}
