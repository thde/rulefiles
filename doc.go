// Package rulefiles is the root of a module that parses the _headers and
// _redirects files of a static site build, the formats popularised by Netlify
// and Cloudflare Pages.
//
// The root package holds no code of its own; the two formats are implemented
// by dedicated packages:
//
//   - [thde.io/rulefiles/header] reads a _headers file and resolves the
//     response header fields declared for a request path.
//   - [thde.io/rulefiles/redirect] reads a _redirects file and resolves
//     the destination a request is redirected or rewritten to.
//
// Each package can resolve rules individually for callers that act on the result
// directly, or wrap a [net/http.Handler] for middleware use.
//
// # Middleware Pipeline
//
// When serving a static site with both header and redirect rules, the handlers
// compose with header rules wrapped innermost, directly around the file
// server, and redirect rules outermost:
//
//	site, err := header.NewHandler(headersFile, http.FileServerFS(build))
//	if err != nil {
//		return err
//	}
//	site, err = redirect.NewHandler(redirectsFile, site)
//	if err != nil {
//		return err
//	}
//
// This ensures that when a redirect rule rewrites a request (HTTP status 200),
// the header rules apply to the rewritten destination path rather than the
// originally requested URL.
//
// # Pattern Syntax
//
// Both rule formats share the same path pattern syntax:
//
//   - Patterns match against the decoded request path and are case-sensitive.
//   - Named placeholders (":name") match a single path segment and capture its value.
//   - A trailing splat ("*") matches the remainder of the path and is captured as ":splat".
//   - Comments begin with "#" at the start of a line or following whitespace.
//
// # Error Handling
//
// Parser functions report all errors across the file together using [errors.Join].
// Every error wraps one of two sentinel errors:
//
//   - [thde.io/rulefiles/header.ErrSyntax] / [thde.io/rulefiles/redirect.ErrSyntax]:
//     for malformed lines that violate the file format syntax.
//   - [thde.io/rulefiles/header.ErrUnsupported] / [thde.io/rulefiles/redirect.ErrUnsupported]:
//     for valid platform syntax that this Go module does not implement
//     (such as conditions on country, language, or cookie, or response framing headers).
package rulefiles
