// Package redirect parses and resolves _redirects files.
//
// The _redirects file format, popularised by [Netlify] and [Cloudflare Pages],
// defines rules for URL redirects, rewrites, and error responses based on
// incoming request paths.
//
// # File Format
//
// A _redirects file contains one rule per line in the format:
//
//	<source> <target> [<status>]
//
// For example:
//
//	/old-path        /new-path
//	/blog/:year/*    /posts/:year/:splat  302
//	/app/*           /index.html          200
//	/gone            /                    410
//
// The status code determines how the match is handled:
//
//   - 3xx (or omitted): redirects the client to the target URL (default 301, or 302 with [WithDefaultStatus]).
//   - 200: rewrites the request, serving the target path internally without redirecting the client.
//   - 4xx / 5xx: answers the request immediately with that HTTP status code.
//
// Query parameters from the original request are preserved and appended to the target URL
// unless the target explicitly defines query parameters.
//
// # Placeholders and Security
//
// Placeholders (such as ":name" and trailing "*"/":splat") captured from the source path
// are expanded into the target path, query string, and fragment separately, with component-appropriate
// escaping. Placeholder values containing ".." cannot escape the target directory path.
//
// # Forced Rules and Shadowing
//
// A trailing "!" on the status code (e.g. "301!") marks the rule as forced ([Rule.Force]).
// When [Handler] is configured with [WithExists], unforced rules are skipped if the wrapped
// handler already serves a static file at the requested path. Without [WithExists], rules
// always apply regardless of existing files.
//
// # Proxying
//
// Rewrites (status 200) to absolute URLs (e.g. "https://api.example.com/:splat") are
// permitted when parsed with [WithProxying]. In [Handler], proxied requests are forwarded
// to the handler supplied via [WithProxy]. If no proxy handler is configured, [Handler]
// returns an error wrapping [ErrProxy].
//
// # Usage
//
// The package supports two workflows:
//
//   - HTTP middleware: [Handler] and [NewHandler] wrap an existing [net/http.Handler] and
//     automatically handle redirects, rewrites, errors, proxying, and shadowing.
//   - Manual resolution: [Parse] reads rules into a [][Rule] slice, and [Resolve] returns a
//     [*Resolved] result for callers that want to handle redirection and rewriting directly.
//
// [Netlify]: https://docs.netlify.com/manage/routing/redirects/overview/
// [Cloudflare Pages]: https://developers.cloudflare.com/pages/configuration/redirects/
package redirect

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"thde.io/rulefiles/internal/rulefile"
)

// statusDefault is used for rules that omit the status code.
// Replicates Netlify's default behaviour.
const statusDefault = http.StatusMovedPermanently

// Rule is a single redirect rule.
type Rule struct {
	// Source is the original path pattern.
	Source string
	// Target is the destination path or URL.
	Target string
	// Status is the HTTP status code.
	Status int
	// Force overrides existing static files.
	Force bool

	// pattern is the compiled Source.
	pattern rulefile.Pattern
}

// Resolved holds the matched redirect result.
type Resolved struct {
	// Rule is the first rule that matched the request.
	Rule Rule
	// To is the target URL.
	To *url.URL
}

// Proxy reports whether the destination is to be fetched from another host
// rather than served from the site. It is only ever true for rules parsed with
// [WithProxying], which allows a rewrite to target an absolute URL, and is false
// for a nil *Resolved.
func (r *Resolved) Proxy() bool {
	return r != nil && r.Rule.Status == http.StatusOK && r.To.Host != ""
}

// Resolve returns the redirect of the first rule matching from, in the order the
// rules are declared. It returns nil if no rule matches.
//
// The status of the matched rule says what to do with the destination:
// - a 200 rewrites the request to it
// - a 400 and above answers the request with that error
// - anything else redirects to it
//
// The caller needs to act on the result.
// A rewrite [Resolved.Proxy] reports is fetched from another host rather than served from the site.
func Resolve(rules []Rule, from *url.URL) (*Resolved, error) {
	return resolve(rules, from, nil)
}

// resolve returns the redirect of the first rule matching from that skip does
// not reject. A nil skip resolves every rule that matches.
func resolve(rules []Rule, from *url.URL, skip func(ru Rule) bool) (*Resolved, error) {
	for _, ru := range rules {
		captures, ok := ru.Match(from.Path)
		if !ok || (skip != nil && skip(ru)) {
			continue
		}

		to, err := ru.Destination(captures, from)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", ru.Source, err)
		}

		return &Resolved{Rule: ru, To: to}, nil
	}

	return nil, nil
}

// Match reports whether reqPath matches the rule and returns the captured placeholder values.
func (ru Rule) Match(reqPath string) (map[string]string, bool) {
	return ru.pattern.Match(reqPath)
}

// Destination builds the URL a request to from is sent to.
// Query parameters of the request are kept unless the target defines them itself.
func (ru Rule) Destination(captures map[string]string, from *url.URL) (*url.URL, error) {
	to, err := url.Parse(ru.Target)
	if err != nil {
		return nil, fmt.Errorf("parsing target %q: %w", ru.Target, err)
	}

	// Clear raw parts after expanding placeholders.
	if expanded := cleanPath(rulefile.ExpandFunc(to.Path, captures, containPath)); expanded != to.Path {
		to.Path, to.RawPath = expanded, ""
	}
	if expanded := rulefile.Expand(to.Fragment, captures); expanded != to.Fragment {
		to.Fragment, to.RawFragment = expanded, ""
	}
	to.RawQuery = rulefile.ExpandFunc(to.RawQuery, captures, url.QueryEscape)

	if from.RawQuery == "" {
		return to, nil
	}
	query := to.Query()
	for name, values := range from.Query() {
		if _, ok := query[name]; !ok {
			query[name] = values
		}
	}
	to.RawQuery = query.Encode()

	return to, nil
}

// containPath cleans relative segments within a capture.
func containPath(value string) string {
	return strings.TrimPrefix(cleanPath("/"+value), "/")
}

// cleanPath cleans p while preserving trailing slashes.
func cleanPath(p string) string {
	if p == "" {
		return p
	}

	cleaned := path.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}

	return cleaned
}
