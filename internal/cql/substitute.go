package cql

import (
	"regexp"
	"strconv"
	"strings"
)

// FunctionContext carries the values used to resolve CQL context functions
// (currentUser, currentCustomer, currentOrganisation) before SQL generation.
// A nil pointer means "no value available in this context"; the matching
// function call is left unresolved and will fall through to the generator,
// where it returns a sentinel that won't match real data.
type FunctionContext struct {
	UserID         *int
	CustomerID     *int
	OrganisationID *int
}

// UserContext returns a FunctionContext containing only the authenticated user ID.
// Most non-portal call sites use this.
func UserContext(userID int) FunctionContext {
	return FunctionContext{UserID: &userID}
}

// Function names are matched case-insensitively, with optional whitespace
// inside the parens, mirroring the tokenizer's behavior.
var (
	currentUserRe         = regexp.MustCompile(`(?i)\bcurrentUser\s*\(\s*\)`)
	currentCustomerRe     = regexp.MustCompile(`(?i)\bcurrentCustomer\s*\(\s*\)`)
	currentOrganisationRe = regexp.MustCompile(`(?i)\bcurrentOrganisation\s*\(\s*\)`)
)

// SubstituteFunctions replaces context-dependent CQL function calls with
// their resolved values before tokenization. Each function is only replaced
// when the corresponding context value is set. Quoted string literals and
// backtick-quoted identifiers are copied verbatim so a literal like
// "currentUser()" keeps its literal meaning.
func SubstituteFunctions(query string, ctx FunctionContext) string {
	if strings.TrimSpace(query) == "" {
		return query
	}
	var out strings.Builder
	out.Grow(len(query))
	for i := 0; i < len(query); {
		if quote := query[i]; quote == '"' || quote == '\'' || quote == '`' {
			i = copyQuoted(&out, query, i, quote)
			continue
		}
		next := strings.IndexAny(query[i:], "\"'`")
		if next < 0 {
			out.WriteString(substituteSegment(query[i:], ctx))
			break
		}
		out.WriteString(substituteSegment(query[i:i+next], ctx))
		i += next
	}
	return out.String()
}

// copyQuoted copies a quoted segment (including its delimiters) verbatim and
// returns the index just past the closing delimiter. Backslash escapes the next
// byte, matching the tokenizer's readString.
func copyQuoted(out *strings.Builder, query string, start int, quote byte) int {
	out.WriteByte(quote)
	i := start + 1
	for i < len(query) {
		c := query[i]
		out.WriteByte(c)
		i++
		if c == '\\' && i < len(query) {
			out.WriteByte(query[i])
			i++
			continue
		}
		if c == quote {
			break
		}
	}
	return i
}

func substituteSegment(segment string, ctx FunctionContext) string {
	if ctx.UserID != nil {
		segment = currentUserRe.ReplaceAllString(segment, strconv.Itoa(*ctx.UserID))
	}
	if ctx.CustomerID != nil {
		segment = currentCustomerRe.ReplaceAllString(segment, strconv.Itoa(*ctx.CustomerID))
	}
	if ctx.OrganisationID != nil {
		segment = currentOrganisationRe.ReplaceAllString(segment, strconv.Itoa(*ctx.OrganisationID))
	}
	return segment
}
