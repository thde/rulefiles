# rulefiles

Go parsers for the `_headers` and `_redirects` files of a static site build, the
formats popularised by [Netlify][netlify-headers] and
[Cloudflare Pages][cloudflare-headers].

[netlify-headers]: https://docs.netlify.com/manage/routing/headers/
[cloudflare-headers]: https://developers.cloudflare.com/pages/configuration/headers/

| Package    | File         | Purpose                             | Documentation                                         |
| ---------- | ------------ | ----------------------------------- | ----------------------------------------------------- |
| `header`   | `_headers`   | Response header fields for a path   | [pkg.go.dev/thde.io/rulefiles/header][doc-header]     |
| `redirect` | `_redirects` | URL redirects, rewrites, and errors | [pkg.go.dev/thde.io/rulefiles/redirect][doc-redirect] |

[doc-header]: https://pkg.go.dev/thde.io/rulefiles/header
[doc-redirect]: https://pkg.go.dev/thde.io/rulefiles/redirect

```bash
go get thde.io/rulefiles@latest
```

## Quick Start

`NewHandler` reads a rules file and wraps an `http.Handler`. Handlers compose
with `header` innermost (around the static file server) and `redirect` outermost,
so that header rules apply to the rewritten destination of a redirect:

```go
site, err := header.NewHandler(headersFile, http.FileServerFS(build))
if err != nil {
	return err
}

site, err = redirect.NewHandler(redirectsFile, site)
if err != nil {
	return err
}
```

For callers that need to inspect or act on rules directly without middleware,
both packages also export `Parse` and `Resolve` functions. See the
[package documentation](#documentation) for details.

## File Formats

### `_redirects`

`_redirects` holds one `<source> <target> [<status>]` rule per line, as
documented by
[Netlify](https://docs.netlify.com/manage/routing/redirects/overview/) and
[Cloudflare Pages](https://developers.cloudflare.com/pages/configuration/redirects/).
The status defaults to `301`. A status of `200` rewrites the request internally
instead of redirecting. Query parameters of the request are preserved unless
the target defines them.

```
/old/path        /new/path
/docs/:id/*      /articles/:id/:splat  302
/gone            /                     410
```

### `_headers`

`_headers` holds an unindented path pattern followed by indented
`<name>: <value>` or `! <name>` lines, as documented by
[Netlify][netlify-headers] and [Cloudflare Pages][cloudflare-headers]. All
matching rules apply in the order declared; a field set by more than one rule is
joined with a comma. Field names are case insensitive.

```
/*
  X-Robots-Tag: noindex
/static/*
  Cache-Control: public, max-age=31536000, immutable
  ! X-Robots-Tag
```

### Path Patterns & Placeholders

Both formats share pattern syntax:

- Patterns match against the decoded request path and are case sensitive.
- Named placeholders (`:name`) capture individual path segments.
- A trailing `*` captures the remainder of the path as `:splat`.
- Placeholders expand into the target path, query, and fragment separately with
  proper escaping. Placeholders cannot introduce directory traversals (`..`).
- Comments start with `#` at the start of a line or after whitespace.

### Differences from Netlify and Cloudflare Pages

- Matching on a scheme, host, query string, country, language, role, or cookie
  is not supported. Proxying to another host is supported via `redirect.WithProxying`
  and `redirect.WithProxy`.
- A trailing `!` on a redirect status (e.g. `301!`) forces a rule to apply even
  if a file of the same name exists, reported as `Rule.Force` (enabled with `redirect.WithExists`).
- A rule that omits the status redirects with 301 (Netlify default). Cloudflare
  defaults to 302; use `redirect.WithDefaultStatus` to configure.
- By default a trailing slash is ignored on both sides (Netlify default). Use
  `WithExactTrailingSlash()` to match trailing slashes exactly (Cloudflare default).
- `*` is only supported as the last segment of a pattern.
- Special characters in source paths may be URL-encoded or written as literals.
- Header fields that frame the response (`Content-Length`, `Transfer-Encoding`,
  `Connection`, `Upgrade`, etc.) are rejected with `ErrUnsupported`.
- A field declared more than once is joined with `", "` (Cloudflare behavior).
  `Set-Cookie` values are retained as separate header lines.
- Lines longer than 1 MiB are rejected to bound allocation.

## Documentation

Full API documentation, options, and examples are available via `go doc` or pkg.go.dev:

- [`thde.io/rulefiles`][doc-root]: Root module overview, architecture, and pipeline composition.
- [`thde.io/rulefiles/header`][doc-header]: Header rule parsing, resolution, and response writer middleware.
- [`thde.io/rulefiles/redirect`][doc-redirect]: Redirect rule parsing, resolution, proxying, and middleware.

[doc-root]: https://pkg.go.dev/thde.io/rulefiles

## Tests

```bash
go test ./...
```

Both parsers include fuzz tests verifying round-trip encoding and safe resolution:

```bash
go test ./redirect -run '^$' -fuzz FuzzResolve
go test ./header -run '^$' -fuzz FuzzResolve
```
