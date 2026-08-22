package redirect_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"

	"thde.io/rulefiles/redirect"
)

// file contains example redirect rules.
const file = `
/old-path            /new-path
/blog/:year/*        /posts/:year/:splat   302
/gone                /                     410
/app/*               /index.html           200
`

// Example demonstrates parsing and resolving redirect.
func Example() {
	rules, err := redirect.Parse(strings.NewReader(file))
	if err != nil {
		log.Fatal(err)
	}

	for _, request := range []string{"/old-path", "/blog/2026/hello/world?utm=1", "/gone", "/app/deep/link", "/about"} {
		from, err := url.Parse(request)
		if err != nil {
			log.Fatal(err)
		}

		resolved, err := redirect.Resolve(rules, from)
		if err != nil {
			log.Fatal(err)
		}
		if resolved == nil {
			fmt.Printf("%s: no rule\n", request)

			continue
		}
		fmt.Printf("%s: %d %s\n", request, resolved.Rule.Status, resolved.To)
	}

	// Output:
	// /old-path: 301 /new-path
	// /blog/2026/hello/world?utm=1: 302 /posts/2026/hello/world?utm=1
	// /gone: 410 /
	// /app/deep/link: 200 /index.html
	// /about: no rule
}

// ExampleHandler demonstrates wrapping a handler with redirect rules.
func ExampleHandler() {
	rules, err := redirect.Parse(strings.NewReader(file))
	if err != nil {
		slog.Error("reading the redirect rules", "error", err)

		return
	}

	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Echo request path for example output.
		_, _ = fmt.Fprintln(w, "serving "+r.URL.Path)
	})

	handler := redirect.Handler(rules, site)

	for _, request := range []string{"/old-path", "/app/deep/link", "/gone", "/about"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, request, http.NoBody))

		fmt.Printf("%s: %d %s\n", request, rec.Code, strings.TrimSpace(rec.Header().Get("Location")+rec.Body.String()))
	}

	// Output:
	// /old-path: 301 /new-path<a href="/new-path">Moved Permanently</a>.
	// /app/deep/link: 200 serving /index.html
	// /gone: 410 Gone
	// /about: 200 serving /about
}

// ExampleWithExists demonstrates honouring the force flag of a rule.
func ExampleWithExists() {
	const file = `
/about       /about-us      301
/contact     /contact-us    301!
`

	rules, err := redirect.Parse(strings.NewReader(file))
	if err != nil {
		slog.Error("reading the redirect rules", "error", err)

		return
	}

	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Echo request path for example output.
		_, _ = fmt.Fprintln(w, "serving "+r.URL.Path)
	})

	// files holds the paths the site serves itself.
	files := map[string]bool{"/about": true, "/contact": true}
	handler := redirect.Handler(rules, site, redirect.WithExists(func(r *http.Request) bool {
		return files[r.URL.Path]
	}))

	for _, request := range []string{"/about", "/contact"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, request, http.NoBody))

		fmt.Printf("%s: %d %s\n", request, rec.Code, strings.TrimSpace(rec.Header().Get("Location")+rec.Body.String()))
	}

	// Output:
	// /about: 200 serving /about
	// /contact: 301 /contact-us<a href="/contact-us">Moved Permanently</a>.
}

// ExampleWithProxy demonstrates fetching a rewrite from another host.
func ExampleWithProxy() {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Echo request path for example output.
		_, _ = fmt.Fprintln(w, "the backend answered "+r.URL.Path)
	}))
	defer backend.Close()

	rules, err := redirect.Parse(
		strings.NewReader("/api/*  "+backend.URL+"/:splat  200\n"),
		redirect.WithProxying(),
	)
	if err != nil {
		slog.Error("reading the redirect rules", "error", err)

		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) { pr.Out.URL = pr.In.URL },
	}

	rec := httptest.NewRecorder()
	redirect.Handler(rules, http.NotFoundHandler(), redirect.WithProxy(proxy)).
		ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/users", http.NoBody))

	fmt.Printf("%d %s", rec.Code, rec.Body)

	// Output:
	// 200 the backend answered /v1/users
}

// ExampleResolve demonstrates handling matched redirect rules.
func ExampleResolve() {
	rules, err := redirect.Parse(strings.NewReader(file))
	if err != nil {
		log.Fatal(err)
	}

	serve := func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Echo request path for example output.
		_, _ = fmt.Fprintln(w, "serving "+r.URL.Path)
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		resolved, err := redirect.Resolve(rules, r.URL)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

			return
		}
		if resolved == nil {
			serve(w, r)

			return
		}

		switch status := resolved.Rule.Status; {
		case status == http.StatusOK:
			// Rewrite request URL.
			r.URL = resolved.To

			serve(w, r)
		case status >= 400:
			http.Error(w, http.StatusText(status), status)
		default:
			//nolint:gosec // Destination is from static rules.
			http.Redirect(w, r, resolved.To.String(), status)
		}
	}

	for _, request := range []string{"/old-path", "/app/deep/link", "/gone", "/about"} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, request, http.NoBody))

		fmt.Printf("%s: %d %s%s", request, rec.Code, rec.Header().Get("Location"), rec.Body)
	}

	// Output:
	// /old-path: 301 /new-path<a href="/new-path">Moved Permanently</a>.
	//
	// /app/deep/link: 200 serving /index.html
	// /gone: 410 Gone
	// /about: 200 serving /about
}

// ExampleRule_Destination demonstrates expanding path placeholders.
func ExampleRule_Destination() {
	rules, err := redirect.Parse(strings.NewReader("/blog/:slug /posts/:slug?from=:slug\n"))
	if err != nil {
		log.Fatal(err)
	}
	rule := rules[0]

	for _, request := range []string{"/blog/hello", "/blog/a%20b%26c", "/about"} {
		from, err := url.Parse(request)
		if err != nil {
			log.Fatal(err)
		}

		captures, ok := rule.Match(from.Path)
		if !ok {
			fmt.Printf("%s: %s does not match\n", request, rule.Source)

			continue
		}

		to, err := rule.Destination(captures, from)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: slug %q -> %s\n", request, captures["slug"], to)
	}

	// Output:
	// /blog/hello: slug "hello" -> /posts/hello?from=hello
	// /blog/a%20b%26c: slug "a b&c" -> /posts/a%20b&c?from=a+b%26c
	// /about: /blog/:slug does not match
}

// ExampleWithExactTrailingSlash demonstrates exact slash matching.
func ExampleWithExactTrailingSlash() {
	const file = "/docs /docs/\n"

	for _, opts := range [][]redirect.Option{nil, {redirect.WithExactTrailingSlash(), redirect.WithDefaultStatus(302)}} {
		rules, err := redirect.Parse(strings.NewReader(file), opts...)
		if err != nil {
			log.Fatal(err)
		}

		for _, request := range []string{"/docs", "/docs/"} {
			from, err := url.Parse(request)
			if err != nil {
				log.Fatal(err)
			}

			resolved, err := redirect.Resolve(rules, from)
			if err != nil {
				log.Fatal(err)
			}
			if resolved == nil {
				fmt.Printf("%s: no rule\n", request)

				continue
			}
			fmt.Printf("%s: %d %s\n", request, resolved.Rule.Status, resolved.To)
		}
	}

	// Output:
	// /docs: 301 /docs/
	// /docs/: 301 /docs/
	// /docs: 302 /docs/
	// /docs/: no rule
}

// ExampleWithProxying demonstrates proxying rewrites to URLs.
func ExampleWithProxying() {
	const file = `
/api/*    https://backend.example.com/:splat   200
/app/*    /index.html                          200
`

	rules, err := redirect.Parse(strings.NewReader(file), redirect.WithProxying())
	if err != nil {
		log.Fatal(err)
	}

	for _, request := range []string{"/api/v1/users", "/app/deep/link"} {
		from, err := url.Parse(request)
		if err != nil {
			log.Fatal(err)
		}

		resolved, err := redirect.Resolve(rules, from)
		if err != nil {
			log.Fatal(err)
		}

		if resolved.Proxy() {
			fmt.Printf("%s: fetch %s\n", request, resolved.To)

			continue
		}
		fmt.Printf("%s: serve %s\n", request, resolved.To)
	}

	// Output:
	// /api/v1/users: fetch https://backend.example.com/v1/users
	// /app/deep/link: serve /index.html
}
