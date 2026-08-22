package redirect

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"thde.io/rulefiles/internal/rulefile"
)

var (
	// ErrSyntax reports malformed rule syntax.
	ErrSyntax = rulefile.ErrSyntax

	// ErrUnsupported reports a line that Netlify or Cloudflare Pages accepts but that this package does not implement,
	// such as matching on a country or a query parameter.
	ErrUnsupported = rulefile.ErrUnsupported
)

// Option configures the parser.
type Option func(*options)

// options holds parser settings.
type options struct {
	// defaultStatus is the fallback status code.
	defaultStatus int
	// pattern holds pattern compiler options.
	pattern rulefile.PatternOptions
	// proxy allows rewrites to other hosts.
	proxy bool
}

// WithDefaultStatus sets the default redirect status.
// Defaults to 301, as on Netlify. Cloudflare Pages uses 302.
func WithDefaultStatus(status int) Option {
	return func(o *options) { o.defaultStatus = status }
}

// WithExactTrailingSlash matches trailing slashes exactly.
// By default a trailing slash is ignored on both sides, as on Netlify.
func WithExactTrailingSlash() Option {
	return func(o *options) { o.pattern.ExactTrailingSlash = true }
}

// WithProxying allows rewrites to other hosts. Fetching the destination is left to the caller.
func WithProxying() Option {
	return func(o *options) { o.proxy = true }
}

// newOptions returns default options with opts applied.
func newOptions(opts []Option) options {
	o := options{defaultStatus: statusDefault}
	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// Parse reads the rules of a _redirects file. Empty lines and comments starting
// with "#" are ignored.
// Errors of all lines are reported together.
//
// By default, the rules behave as they do on Netlify.
// Use [Option] arguments to configure their behaviour.
func Parse(r io.Reader, opts ...Option) ([]Rule, error) {
	var (
		rules []Rule
		errs  []error
	)

	o := newOptions(opts)
	// Validate default status before scanning lines.
	if err := validateStatus(o.defaultStatus); err != nil {
		return nil, fmt.Errorf("default status: %w", err)
	}

	err := rulefile.Scan(r, func(line int, text string) {
		ru, err := parseRule(text, o)
		if err != nil {
			errs = append(errs, fmt.Errorf("line %d: %w", line, err))

			return
		}
		rules = append(rules, ru)
	})
	if err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	return rules, nil
}

// parseRule parses a single rule line.
func parseRule(line string, o options) (Rule, error) {
	// Split line by whitespace.
	fields := strings.FieldsFunc(line, rulefile.IsSpace)
	if len(fields) < 2 {
		return Rule{}, fmt.Errorf("%w: expected %q, got %q", ErrSyntax, "<source> <target> [<status>]", line)
	}
	if len(fields) > 3 {
		return Rule{}, fmt.Errorf("%w: unexpected %q, matching on conditions such as query parameters, countries or roles", ErrUnsupported, fields[3])
	}

	status, force := o.defaultStatus, false
	if len(fields) == 3 {
		field := fields[2]
		// Check for trailing force flag.
		if trimmed, ok := strings.CutSuffix(field, "!"); ok {
			field, force = trimmed, true
		}

		var err error
		if status, err = parseStatus(field); err != nil {
			return Rule{}, err
		}
	}

	ru := Rule{Source: fields[0], Target: fields[1], Status: status, Force: force}

	pattern, err := rulefile.ParsePattern(ru.Source, o.pattern)
	if err != nil {
		return Rule{}, fmt.Errorf("source %q: %w", ru.Source, err)
	}
	ru.pattern = pattern

	if err := validateTarget(ru.Target, ru.Status, pattern, o.proxy); err != nil {
		return Rule{}, fmt.Errorf("target %q: %w", ru.Target, err)
	}

	return ru, nil
}

// parseStatus parses a status code.
func parseStatus(field string) (int, error) {
	status, err := strconv.Atoi(field)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid status code %q", ErrSyntax, field)
	}
	if err := validateStatus(status); err != nil {
		return 0, err
	}

	return status, nil
}

// validateStatus checks that status is 200 or in the 3xx to 5xx range.
func validateStatus(status int) error {
	if status != http.StatusOK && (status < 300 || status > 599) {
		return fmt.Errorf("%w: status code %d, expected 200, 3xx, 4xx or 5xx", ErrUnsupported, status)
	}

	return nil
}

// validateTarget checks target syntax and placeholders.
func validateTarget(target string, status int, pattern rulefile.Pattern, proxy bool) error {
	// Parse target URL components.
	to, err := url.Parse(target)
	if err != nil {
		// Unwrap url.Error for cleaner messages.
		if parseErr, ok := errors.AsType[*url.Error](err); ok {
			err = parseErr.Err
		}

		return fmt.Errorf("%w: %w", ErrSyntax, err)
	}

	// Ensure rewrite targets a valid path.
	if status == http.StatusOK && !isSitePath(to) {
		switch {
		case !proxy:
			return fmt.Errorf("%w: proxying to another host, a rewrite must target a path", ErrUnsupported)
		case to.Scheme == "" || to.Host == "":
			return fmt.Errorf("%w: a rewrite that proxies must target a path or an absolute URL naming a scheme and a host", ErrSyntax)
		}
	}

	if unknown := pattern.Uncaptured(target); len(unknown) > 0 {
		return fmt.Errorf("%w: placeholder %q is not captured by the source path", ErrSyntax, ":"+unknown[0])
	}

	return nil
}

// isSitePath reports whether to is a site-relative path, naming no scheme, host or user.
func isSitePath(to *url.URL) bool {
	return to.Scheme == "" && to.Opaque == "" && to.Host == "" && to.User == nil &&
		strings.HasPrefix(to.Path, "/")
}
