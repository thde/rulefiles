// Package header parses and applies _headers files.
//
// The _headers file format, popularised by [Netlify] and [Cloudflare Pages],
// defines HTTP response header fields that should be attached to responses
// matching specific request path patterns.
//
// # File Format
//
// A _headers file consists of an unindented path pattern line followed by one
// or more indented lines that set or remove header fields:
//
//	/*
//	  X-Robots-Tag: noindex
//	/static/*
//	  Cache-Control: public, max-age=31536000, immutable
//	  ! X-Robots-Tag
//
// Field names are case-insensitive and converted to canonical form with
// [net/http.CanonicalHeaderKey].
//
// # Multi-Value and Framing Headers
//
// When multiple rules match a request path, their header operations are applied
// in the order declared. If a header is set multiple times, values are joined
// with ", ", matching Cloudflare Pages behavior. The "Set-Cookie" header is an
// exception: each value is retained as an individual header line.
//
// Hop-by-hop and response-framing headers (such as "Content-Length",
// "Transfer-Encoding", "Connection", "Upgrade", "Trailer", and "Keep-Alive")
// are rejected during parsing with [ErrUnsupported].
//
// # Usage
//
// The package supports two workflows:
//
//   - HTTP middleware: [Handler] and [NewHandler] wrap an existing [net/http.Handler].
//     Headers are applied dynamically when the wrapped handler writes its status,
//     ensuring configured headers override downstream values and "!" removals drop them.
//   - Manual resolution: [Parse] reads rules into a [][Rule] slice, and [Resolve] returns a
//     [*Resolved] result that can be applied to an [net/http.Header] via [Resolved.ApplyTo].
//
// [Netlify]: https://docs.netlify.com/manage/routing/headers/
// [Cloudflare Pages]: https://developers.cloudflare.com/pages/configuration/headers/
package header

import (
	"net/http"
	"slices"
	"strings"

	"golang.org/x/net/http/httpguts"
	"thde.io/rulefiles/internal/rulefile"
)

// Rule defines headers for a path pattern.
type Rule struct {
	// Source is the raw path pattern.
	Source string
	// Operations are the header operations.
	Operations []Operation

	// pattern is the compiled path pattern.
	pattern rulefile.Pattern
}

// Operation is a single header operation.
type Operation struct {
	// Name is the canonical header name.
	Name string
	// Value is the field value, possibly containing ":name" placeholders. It is
	// empty for a removal.
	Value string
	// Remove reports whether to delete the header.
	Remove bool
}

// Resolved holds headers resolved for a request.
type Resolved struct {
	// set holds added header values.
	set http.Header
	// removed holds deleted header names.
	removed map[string]struct{}
}

// Resolve finds matching headers for path.
func Resolve(rules []Rule, path string) *Resolved {
	var resolved *Resolved

	for _, ru := range rules {
		captures, ok := ru.pattern.Match(path)
		if !ok {
			continue
		}
		if resolved == nil {
			resolved = &Resolved{set: http.Header{}, removed: map[string]struct{}{}}
		}

		for _, op := range ru.Operations {
			if op.Remove {
				delete(resolved.set, op.Name)
				resolved.removed[op.Name] = struct{}{}
				continue
			}

			value := rulefile.Expand(op.Value, captures)
			// Drop invalid header values.
			if !httpguts.ValidHeaderFieldValue(value) {
				continue
			}
			resolved.set[op.Name] = append(resolved.set[op.Name], value)
			delete(resolved.removed, op.Name)
		}
	}

	return resolved
}

// ApplyTo updates hdr with the resolved headers.
func (r *Resolved) ApplyTo(hdr http.Header) {
	if r == nil {
		return
	}

	for name, values := range r.set {
		hdr[name] = joinValues(name, values)
	}
	for name := range r.removed {
		delete(hdr, name)
	}
}

// Fields returns sorted touched header names.
func (r *Resolved) Fields() []string {
	if r == nil {
		return nil
	}

	names := make([]string, 0, len(r.set)+len(r.removed))
	for name := range r.set {
		names = append(names, name)
	}
	for name := range r.removed {
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

// joinValues joins values with commas, except cookies.
func joinValues(name string, values []string) []string {
	if len(values) == 1 || name == "Set-Cookie" {
		return values
	}

	return []string{strings.Join(values, ", ")}
}
