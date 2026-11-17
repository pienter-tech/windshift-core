package smtp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"strings"
	"testing"
)

// rfc2045Tspecials are the characters that force a parameter value to be a
// quoted-string (RFC 2045 section 5.1).
const rfc2045Tspecials = `()<>@,;:\"/[]?=`

func isRFC2045Token(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r >= 0x7f || strings.ContainsRune(rfc2045Tspecials, r) {
			return false
		}
	}
	return true
}

// boundaryParam returns the raw boundary parameter text as written in the
// Content-Type header, before any unquoting.
func boundaryParam(t *testing.T, contentType string) string {
	t.Helper()
	idx := strings.Index(strings.ToLower(contentType), "boundary=")
	if idx < 0 {
		t.Fatalf("Content-Type %q has no boundary parameter", contentType)
	}
	raw := contentType[idx+len("boundary="):]
	if strings.HasPrefix(raw, `"`) {
		end := strings.Index(raw[1:], `"`)
		if end < 0 {
			t.Fatalf("unterminated quoted boundary in %q", contentType)
		}
		return raw[:end+2]
	}
	if end := strings.IndexAny(raw, "; \t"); end >= 0 {
		return raw[:end]
	}
	return raw
}

// assertRFC2045Boundary checks the raw parameter is a token, or a
// quoted-string matching the parsed value.
func assertRFC2045Boundary(t *testing.T, raw, parsed string) {
	t.Helper()
	if strings.HasPrefix(raw, `"`) {
		if want := `"` + parsed + `"`; raw != want {
			t.Fatalf("quoted boundary %q does not match parsed value %q", raw, parsed)
		}
		return
	}
	if !isRFC2045Token(raw) {
		t.Fatalf("unquoted boundary %q is not an RFC 2045 token", raw)
	}
	if raw != parsed {
		t.Fatalf("unquoted boundary %q does not match parsed value %q", raw, parsed)
	}
}

func TestBuildMimeBoundaryIsValidAndMatchesBody(t *testing.T) {
	opts := mimeOptions{
		FromEmail:    "noreply@example.com",
		FromName:     "Windshift",
		ToEmail:      "user@example.com",
		ToName:       "Zoë User",
		ReplyToEmail: "support@example.com",
		Subject:      "Test – notification",
		TextBody:     "Plain body with = and ünïcode",
		HTMLBody:     "<p>HTML body with = and ünïcode</p>",
		MessageID:    "abc@example.com",
		InReplyTo:    "parent@example.com",
		References:   []string{"root@example.com", "parent@example.com"},
	}

	// The boundary is random; build several messages so one lucky value does
	// not hide a bad one.
	for i := 0; i < 20; i++ {
		msg, err := buildMime(opts)
		if err != nil {
			t.Fatalf("buildMime: %v", err)
		}

		tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(msg)))
		header, err := tp.ReadMIMEHeader()
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}

		contentType := header.Get("Content-Type")
		mediaType, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			t.Fatalf("parse Content-Type %q: %v", contentType, err)
		}
		if mediaType != "multipart/alternative" {
			t.Fatalf("media type = %q, want multipart/alternative", mediaType)
		}
		boundary := params["boundary"]
		if boundary == "" {
			t.Fatalf("Content-Type %q has an empty boundary", contentType)
		}
		assertRFC2045Boundary(t, boundaryParam(t, contentType), boundary)

		// The reader only finds parts when the body delimiters use the same
		// boundary the header announced.
		mr := multipart.NewReader(tp.R, boundary)
		want := []struct{ mediaType, body string }{
			{"text/plain", opts.TextBody},
			{"text/html", opts.HTMLBody},
		}
		for _, w := range want {
			part, err := mr.NextRawPart()
			if err != nil {
				t.Fatalf("read %s part: %v", w.mediaType, err)
			}
			partType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse part Content-Type: %v", err)
			}
			if partType != w.mediaType {
				t.Fatalf("part type = %q, want %q", partType, w.mediaType)
			}
			if enc := part.Header.Get("Content-Transfer-Encoding"); enc != "quoted-printable" {
				t.Fatalf("part encoding = %q, want quoted-printable", enc)
			}
			decoded, err := io.ReadAll(quotedprintable.NewReader(part))
			if err != nil {
				t.Fatalf("decode %s part: %v", w.mediaType, err)
			}
			if got := strings.TrimRight(string(decoded), "\r\n"); got != w.body {
				t.Fatalf("%s body = %q, want %q", w.mediaType, got, w.body)
			}
		}
		if _, err := mr.NextRawPart(); !errors.Is(err, io.EOF) {
			t.Fatalf("expected end of multipart after two parts, got %v", err)
		}
	}
}

// TestOldUnquotedBoundaryFailsCheck pins the check against the header shape
// Amazon SES rejected: an unquoted boundary holding "=".
func TestOldUnquotedBoundaryFailsCheck(t *testing.T) {
	old := "multipart/alternative; boundary=----=_NextPart_1792575761215414920"
	if _, _, err := mime.ParseMediaType(old); err == nil {
		t.Fatalf("ParseMediaType accepted unquoted boundary with '=': %q", old)
	}
	if isRFC2045Token(boundaryParam(t, old)) {
		t.Fatalf("token check accepted unquoted boundary with '='")
	}

	// The same value passes once quoted, which mime.FormatMediaType does for
	// values that need it.
	quoted := mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": "----=_NextPart_1792575761215414920"})
	_, params, err := mime.ParseMediaType(quoted)
	if err != nil {
		t.Fatalf("parse quoted boundary %q: %v", quoted, err)
	}
	assertRFC2045Boundary(t, boundaryParam(t, quoted), params["boundary"])
}
