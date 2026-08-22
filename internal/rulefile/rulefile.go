// Package rulefile implements the syntax shared by the _redirects and _headers
// files, the formats popularised by Netlify and Cloudflare Pages:
// comments, path patterns and the ":name" placeholders a pattern captures.
package rulefile

import (
	"fmt"
	"net/url"
	"strings"
)

// SplatName is the name a trailing "*" of a pattern captures under.
const SplatName = "splat"

// PatternOptions configures how a pattern is compiled and matched.
// It defaults to the behaviour of Netlify.
type PatternOptions struct {
	// ExactTrailingSlash matches the trailing slash of a path as written, as
	// Cloudflare Pages does: a pattern ending in "/" then only matches a path
	// ending in "/", and one that does not only matches a path that does not.
	// By default a trailing slash is ignored on both sides, as on Netlify.
	//
	// A pattern ending in "*" is unaffected, as its splat covers the rest of the
	// path including a trailing slash and captures it.
	ExactTrailingSlash bool
}

// Pattern is a compiled path pattern such as "/blog/:year/*". Patterns are
// matched against a decoded URL path and are case sensitive.
type Pattern struct {
	// compiled reports whether ParsePattern built the pattern.
	compiled bool
	// segments holds path segments.
	segments []segment
	// names contains placeholder capture names.
	names map[string]struct{}
	// options holds compile options.
	options PatternOptions
	// slash reports a trailing slash.
	slash bool
}

// segment is a path segment.
type segment struct {
	// literal is matched verbatim if placeholder is empty.
	literal string
	// placeholder is the capture name of a ":name" or "*" segment.
	placeholder string
	// splat reports whether the segment captures all remaining segments.
	splat bool
}

// ParsePattern compiles a path pattern.
// The pattern only parses the path of a request. Scheme, host and query strings are ignored.
// The percent escapes of a literal segment are decoded,
// as a pattern is matched against the decoded path of a request.
func ParsePattern(source string, opts PatternOptions) (Pattern, error) {
	switch {
	case !strings.HasPrefix(source, "/"):
		return Pattern{}, fmt.Errorf(`%w: matching on a scheme or host, a pattern must start with "/"`, ErrUnsupported)
	case strings.ContainsAny(source, "?#"):
		return Pattern{}, fmt.Errorf("%w: matching on a query string or fragment", ErrUnsupported)
	}

	parts, slash := pathSegments(source)
	pattern := Pattern{
		compiled: true,
		segments: make([]segment, 0, len(parts)),
		names:    make(map[string]struct{}, len(parts)),
		options:  opts,
		slash:    slash,
	}

	for i, part := range parts {
		var seg segment
		switch {
		case part == "*":
			if i != len(parts)-1 {
				return Pattern{}, fmt.Errorf(`%w: a "*" other than the last segment`, ErrUnsupported)
			}
			seg = segment{placeholder: SplatName, splat: true}
		case strings.HasPrefix(part, ":"):
			if !isPlaceholderName(part[1:]) {
				return Pattern{}, fmt.Errorf("%w: invalid placeholder %q", ErrSyntax, part)
			}
			seg = segment{placeholder: part[1:]}
		case strings.Contains(part, "*"):
			return Pattern{}, fmt.Errorf(`%w: a "*" that is not a whole segment`, ErrUnsupported)
		default:
			literal, err := unescapeSegment(part)
			if err != nil {
				return Pattern{}, err
			}
			seg = segment{literal: literal}
		}

		if seg.placeholder != "" {
			if _, ok := pattern.names[seg.placeholder]; ok {
				return Pattern{}, fmt.Errorf("%w: duplicate placeholder %q", ErrSyntax, ":"+seg.placeholder)
			}
			pattern.names[seg.placeholder] = struct{}{}
		}
		pattern.segments = append(pattern.segments, seg)
	}

	return pattern, nil
}

// Match reports whether path matches the pattern and returns the captured placeholder values.
// The captures are nil if the pattern captures nothing.
func (p Pattern) Match(path string) (map[string]string, bool) {
	if !p.compiled {
		return nil, false
	}

	parts, slash := pathSegments(path)
	if !p.matches(parts, slash) {
		return nil, false
	}
	if len(p.names) == 0 {
		return nil, true
	}

	captures := make(map[string]string, len(p.names))
	for i, seg := range p.segments {
		switch {
		case seg.splat:
			captures[SplatName] = p.splat(parts[i:], slash)
		case seg.placeholder != "":
			captures[seg.placeholder] = parts[i]
		}
	}

	return captures, true
}

// matches reports whether the segments of a path match the pattern.
func (p Pattern) matches(parts []string, slash bool) bool {
	for i, seg := range p.segments {
		// Splat matches all remaining segments.
		if seg.splat {
			return true
		}
		if i >= len(parts) {
			return false
		}
		if seg.placeholder == "" && seg.literal != parts[i] {
			return false
		}
	}
	if len(parts) != len(p.segments) {
		return false
	}

	return !p.options.ExactTrailingSlash || slash == p.slash
}

// splat returns the value a trailing "*" captures for the segments of the path it covers.
func (p Pattern) splat(rest []string, slash bool) string {
	value := strings.Join(rest, "/")
	if p.options.ExactTrailingSlash && slash && len(rest) > 0 {
		value += "/"
	}

	return value
}

// Uncaptured returns missing placeholder names from s.
func (p Pattern) Uncaptured(s string) []string {
	var unknown []string

	placeholders(s, func(_, _ int, name string) {
		if _, ok := p.names[name]; !ok {
			unknown = append(unknown, name)
		}
	})

	return unknown
}

// Expand replaces every ":name" in s by the value captured for it. Placeholders
// without a capture are left as they are.
func Expand(s string, captures map[string]string) string {
	return ExpandFunc(s, captures, nil)
}

// ExpandFunc replaces placeholders using an escape function.
func ExpandFunc(s string, captures map[string]string, escape func(value string) string) string {
	var (
		expanded strings.Builder
		// last is the end of the placeholder expanded last, and so the start of
		// the text still to copy. It is zero while nothing was expanded.
		last int
	)

	placeholders(s, func(start, end int, name string) {
		value, ok := captures[name]
		if !ok {
			return
		}
		if escape != nil {
			value = escape(value)
		}
		expanded.WriteString(s[last:start])
		expanded.WriteString(value)
		last = end
	})
	if last == 0 {
		return s
	}
	expanded.WriteString(s[last:])

	return expanded.String()
}

// IsSpace reports whether r is whitespace.
func IsSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// TrimSpace removes leading and trailing whitespace.
func TrimSpace(s string) string {
	return strings.Trim(s, " \t")
}

// StripComment removes a comment from a line.
// A "#" only starts a comment at the beginning of a line or after whitespace,
// so that URL fragments and header values survive.
func StripComment(line string) string {
	for i := range len(line) {
		if line[i] != '#' {
			continue
		}
		if i == 0 || IsSpace(rune(line[i-1])) {
			return line[:i]
		}
	}

	return line
}

// pathSegments splits a path into segments.
func pathSegments(path string) (parts []string, slash bool) {
	slash = strings.HasSuffix(path, "/")

	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil, slash
	}

	return strings.Split(trimmed, "/"), slash
}

// placeholders finds valid placeholder names in s.
func placeholders(s string, fn func(start, end int, name string)) {
	for i := 0; i < len(s); i++ {
		if s[i] != ':' {
			continue
		}

		end := i + 1
		for end < len(s) && isNameByte(s[end]) {
			end++
		}
		if name := s[i+1 : end]; isPlaceholderName(name) {
			fn(i, end, name)
			i = end - 1
		}
	}
}

// unescapeSegment decodes URL-escaped path characters.
func unescapeSegment(part string) (string, error) {
	literal, err := url.PathUnescape(part)
	if err != nil {
		return "", fmt.Errorf("%w: segment %q: %w", ErrSyntax, part, err)
	}
	if strings.Contains(literal, "/") {
		return "", fmt.Errorf(`%w: segment %q, an escaped "/" would not add a segment`, ErrUnsupported, part)
	}

	return literal, nil
}

// isPlaceholderName reports whether name is a valid placeholder name. This keeps
// ":" in URLs such as "https://example.com:8080/" from being expanded.
func isPlaceholderName(name string) bool {
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}
	for i := range len(name) {
		if !isNameByte(name[i]) {
			return false
		}
	}

	return true
}

// isNameByte reports valid placeholder characters.
func isNameByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}
