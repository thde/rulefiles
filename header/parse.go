package header

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/http/httpguts"
	"thde.io/rulefiles/internal/rulefile"
)

var (
	// ErrSyntax indicates invalid rule syntax.
	ErrSyntax = rulefile.ErrSyntax

	// ErrUnsupported indicates an unsupported feature.
	ErrUnsupported = rulefile.ErrUnsupported
)

// unsupported lists hop-by-hop framing headers. The keys are
// in the canonical form of [http.CanonicalHeaderKey].
var unsupported = map[string]struct{}{
	"Connection":        {},
	"Content-Length":    {},
	"Keep-Alive":        {},
	"Te":                {},
	"Trailer":           {},
	"Transfer-Encoding": {},
	"Upgrade":           {},
}

// Option configures parser behavior.
// Defaults align with the behavioir of Netlify.
type Option func(*options)

type options struct {
	pattern rulefile.PatternOptions
}

// WithExactTrailingSlash matches the path of a request as written, as Cloudflare
// Pages does: a path pattern ending in "/" then only matches a request path
// ending in "/", and one that does not only matches a request path that does
// not. By default a trailing slash is ignored on both sides, as on Netlify.
//
// A pattern ending in "*" is unaffected: its splat covers the rest of the path,
// a trailing slash included, and captures it.
func WithExactTrailingSlash() Option {
	return func(o *options) { o.pattern.ExactTrailingSlash = true }
}

// newOptions builds options from opts.
func newOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// Parse reads the rules of a _headers file. Empty lines and comments starting
// with "#" are ignored. An unindented line declares the path pattern of a rule,
// every indented line that follows sets or removes one of its header fields:
//
//	/static/*
//	  Cache-Control: public, max-age=31536000, immutable
//	  ! X-Robots-Tag
//
// Errors of all lines are reported together.
//
// By default, the rules behave as they do on Netlify.
// Use [Option] arguments to configure their behaviour.
func Parse(r io.Reader, opts ...Option) ([]Rule, error) {
	p := parser{options: newOptions(opts)}

	err := rulefile.Scan(r, func(line int, text string) {
		trimmed := rulefile.TrimSpace(text)
		if isIndented(text) {
			p.parseHeader(line, trimmed)

			return
		}
		p.parsePath(line, trimmed)
	})
	p.flush()
	if err != nil {
		p.errs = append(p.errs, err)
	}
	if err := errors.Join(p.errs...); err != nil {
		return nil, err
	}

	return p.rules, nil
}

// parser builds rules from lines.
type parser struct {
	rules []Rule
	errs  []error
	// options holds parser settings.
	options options

	// pending is the active rule.
	// It is nil while no rule is open, and is appended to rules once the rule is closed.
	pending *Rule
	// declared is the rule line number.
	declared int
	// failed marks the current rule invalid.
	failed bool
}

// parsePath parses a path line and opens the rule it declares.
func (p *parser) parsePath(line int, source string) {
	p.flush()

	pattern, err := rulefile.ParsePattern(source, p.options.pattern)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("line %d: path %q: %w", line, source, err))
		p.pending, p.failed = nil, true

		return
	}

	p.pending, p.declared, p.failed = &Rule{Source: source, pattern: pattern}, line, false
}

// parseHeader parses a header line and adds its operation to the pending rule.
func (p *parser) parseHeader(line int, text string) {
	if p.pending == nil {
		if !p.failed {
			p.errs = append(p.errs, fmt.Errorf("line %d: %w: header %q is not preceded by a path", line, ErrSyntax, text))
		}

		return
	}

	op, err := parseOperation(text, p.pending.pattern)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("line %d: %w", line, err))
		p.failed = true

		return
	}
	p.pending.Operations = append(p.pending.Operations, op)
}

// flush appends the pending rule to rules, or reports one that declares no headers.
func (p *parser) flush() {
	if p.pending == nil {
		return
	}

	switch {
	case len(p.pending.Operations) > 0:
		p.rules = append(p.rules, *p.pending)
	case !p.failed:
		p.errs = append(p.errs, fmt.Errorf("line %d: %w: path %q declares no headers", p.declared, ErrSyntax, p.pending.Source))
	}
	p.pending = nil
}

// parseOperation parses a single header line.
func parseOperation(line string, pattern rulefile.Pattern) (Operation, error) {
	if rest, ok := strings.CutPrefix(line, "!"); ok {
		name := rulefile.TrimSpace(rest)
		if strings.Contains(name, ":") {
			return Operation{}, fmt.Errorf("%w: unexpected value in %q, a removal only takes a field name", ErrSyntax, line)
		}
		if err := validateName(name); err != nil {
			return Operation{}, err
		}

		return Operation{Name: http.CanonicalHeaderKey(name), Remove: true}, nil
	}

	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return Operation{}, fmt.Errorf("%w: expected %q or %q, got %q", ErrSyntax, "<name>: <value>", "! <name>", line)
	}
	name, value = rulefile.TrimSpace(name), rulefile.TrimSpace(value)
	if err := validateName(name); err != nil {
		return Operation{}, err
	}
	if !httpguts.ValidHeaderFieldValue(value) {
		return Operation{}, fmt.Errorf("%w: invalid value %q for %s", ErrSyntax, value, name)
	}
	if unknown := pattern.Uncaptured(value); len(unknown) > 0 {
		return Operation{}, fmt.Errorf("%w: value %q, placeholder %q is not captured by the path", ErrSyntax, value, ":"+unknown[0])
	}

	return Operation{Name: http.CanonicalHeaderKey(name), Value: value}, nil
}

// validateName checks that name is a valid header field name and not one that frames the response.
func validateName(name string) error {
	if !httpguts.ValidHeaderFieldName(name) {
		return fmt.Errorf("%w: invalid header field name %q", ErrSyntax, name)
	}
	if _, ok := unsupported[http.CanonicalHeaderKey(name)]; ok {
		return fmt.Errorf("%w: %s controls how the response is framed", ErrUnsupported, name)
	}

	return nil
}

// isIndented reports whether line starts with whitespace.
func isIndented(line string) bool {
	return rulefile.IsSpace(rune(line[0]))
}
