package rulefiles_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"thde.io/rulefiles/header"
	"thde.io/rulefiles/redirect"
)

// Example combines redirect and header rules.
func Example() {
	const (
		redirectsFile = "/old/*  /new/:splat\n/app/*  /index.html  200\n/gone   /            410\n"
		headersFile   = "/*\n  X-Robots-Tag: noindex\n/new/*\n  Cache-Control: no-store\n"
	)

	// files simulates a static file server.
	files := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Echo path for example output.
		_, _ = fmt.Fprint(w, "the file at "+r.URL.Path)
	})

	// The header rules apply to the path a request is rewritten to, so they wrap
	// the file server rather than the site.
	served, err := header.NewHandler(strings.NewReader(headersFile), files)
	if err != nil {
		slog.Error("reading the header rules", "error", err)

		return
	}
	site, err := redirect.NewHandler(strings.NewReader(redirectsFile), served)
	if err != nil {
		slog.Error("reading the redirect rules", "error", err)

		return
	}

	for _, request := range []string{"/index.html", "/old/page.html", "/new/page.html", "/app/deep/link", "/gone"} {
		rec := httptest.NewRecorder()
		site.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, request, http.NoBody))

		fmt.Printf("%s: %d location=%q cache-control=%q robots=%q\n",
			request, rec.Code, rec.Header().Get("Location"),
			rec.Header().Get("Cache-Control"), rec.Header().Get("X-Robots-Tag"))
	}

	// Output:
	// /index.html: 200 location="" cache-control="" robots="noindex"
	// /old/page.html: 301 location="/new/page.html" cache-control="" robots=""
	// /new/page.html: 200 location="" cache-control="no-store" robots="noindex"
	// /app/deep/link: 200 location="" cache-control="" robots="noindex"
	// /gone: 410 location="" cache-control="" robots=""
}
