package restapi

import (
	"encoding/json"
	"net/http"
	"reflect"
)

// NormalizeEmptySlice replaces a nil top-level slice with an empty slice so a
// list endpoint encodes [] instead of null. A JSON null crashes clients that
// iterate the response, and repositories build results with `var items []T`
// and return nil when nothing matches. Nested nil slices keep their existing
// encoding so the normalization stays scoped to list payloads.
func NormalizeEmptySlice(data any) any {
	if data == nil {
		return nil
	}
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Slice && v.IsNil() {
		return reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	return data
}

// RespondJSON writes a JSON response with the given status code
func RespondJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	data = NormalizeEmptySlice(data)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

// RespondOK writes a 200 OK JSON response
func RespondOK(w http.ResponseWriter, data any) {
	RespondJSON(w, http.StatusOK, data)
}

// RespondCreated writes a 201 Created JSON response
func RespondCreated(w http.ResponseWriter, data any) {
	RespondJSON(w, http.StatusCreated, data)
}

// RespondNoContent writes a 204 No Content response
func RespondNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// RespondPaginated writes a paginated JSON response. The list is normalized
// before it is wrapped so an empty page encodes "data": [] rather than null.
func RespondPaginated(w http.ResponseWriter, data any, pagination PaginationMeta) {
	RespondOK(w, NewPaginatedResponse(NormalizeEmptySlice(data), pagination))
}
