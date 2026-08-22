package redirect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const handlerFile = `
/old-path       /new-path
/blog/:year/*   /posts/:year/:splat   302
/gone           /                     410
/app/*          /index.html           200
`

func TestHandler(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		request string
		// wantStatus is the status of the response.
		wantStatus int
		// wantLocation is the Location field of a redirect.
		wantLocation string
		// wantServed is the URL next was called with, empty if it was not called.
		wantServed string
	}{
		"a redirect": {request: "/old-path", wantStatus: http.StatusMovedPermanently, wantLocation: "/new-path"},
		"a redirect with a status": {
			request:      "/blog/2026/hello?utm=1",
			wantStatus:   http.StatusFound,
			wantLocation: "/posts/2026/hello?utm=1",
		},
		"an error":  {request: "/gone", wantStatus: http.StatusGone},
		"a rewrite": {request: "/app/deep/link", wantStatus: http.StatusOK, wantServed: "/index.html"},
		"a rewrite keeps the query": {
			request:    "/app/deep/link?page=2",
			wantStatus: http.StatusOK,
			wantServed: "/index.html?page=2",
		},
		"no rule": {request: "/about", wantStatus: http.StatusOK, wantServed: "/about"},
	}

	rules, err := Parse(strings.NewReader(handlerFile))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec, served := serve(t, rules, tt.request)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
			if served != tt.wantServed {
				t.Errorf("next served %q, want %q", served, tt.wantServed)
			}
		})
	}
}

// TestHandlerRewrite checks that a rewrite leaves the request of the caller
// alone, as it is shared with the server that reads it.
func TestHandlerRewrite(t *testing.T) {
	t.Parallel()

	rules, err := Parse(strings.NewReader("/app/* /index.html 200\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	req := request(t, "/app/deep/link")

	Handler(rules, next).ServeHTTP(httptest.NewRecorder(), req)

	if got := req.URL.Path; got != "/app/deep/link" {
		t.Errorf("the URL of the request is %q, want it unchanged", got)
	}
}

func TestHandlerWithExists(t *testing.T) {
	t.Parallel()

	const file = `
/exists       /forced        200!
/exists       /unforced      200
/shadowed     /unforced      200
/shadowed     /fallback      404
`

	tests := map[string]struct {
		request string
		// exists reports whether next serves the path itself.
		exists bool
		// wantStatus is the status of the response.
		wantStatus int
		// wantServed is the URL next was called with, empty if it was not called.
		wantServed string
	}{
		"a forced rule shadows a file":       {request: "/exists", exists: true, wantStatus: http.StatusOK, wantServed: "/forced"},
		"a forced rule without a file":       {request: "/exists", exists: false, wantStatus: http.StatusOK, wantServed: "/forced"},
		"an unforced rule yields to a file":  {request: "/shadowed", exists: true, wantStatus: http.StatusOK, wantServed: "/shadowed"},
		"an unforced rule without a file":    {request: "/shadowed", exists: false, wantStatus: http.StatusOK, wantServed: "/unforced"},
		"a skipped rule does not stop later": {request: "/missing", exists: false, wantStatus: http.StatusOK, wantServed: "/missing"},
	}

	rules, err := Parse(strings.NewReader(file))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec, served := serve(t, rules, tt.request, WithExists(func(*http.Request) bool { return tt.exists }))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if served != tt.wantServed {
				t.Errorf("next served %q, want %q", served, tt.wantServed)
			}
		})
	}
}

// TestHandlerWithExistsLookup checks that the path of a request is looked up at
// most once, and not at all while no rule that is unforced matches it.
func TestHandlerWithExistsLookup(t *testing.T) {
	t.Parallel()

	rules, err := Parse(strings.NewReader("/a /b\n/a /c\n/forced /d 301!\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	tests := map[string]struct {
		request string
		// want is the number of lookups the request is expected to cause.
		want int
	}{
		"an unforced rule looks the path up once": {request: "/a", want: 1},
		"a forced rule does not look it up":       {request: "/forced", want: 0},
		"an unmatched path is not looked up":      {request: "/none", want: 0},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var lookups int
			exists := func(*http.Request) bool {
				lookups++

				return true
			}

			handler := Handler(rules, http.NotFoundHandler(), WithExists(exists))
			handler.ServeHTTP(httptest.NewRecorder(), request(t, tt.request))

			if lookups != tt.want {
				t.Errorf("looked the path up %d times, want %d", lookups, tt.want)
			}
		})
	}
}

func TestHandlerProxy(t *testing.T) {
	t.Parallel()

	rules, err := Parse(strings.NewReader("/api/* https://backend.example.com/:splat 200\n"), WithProxying())
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	t.Run("with a proxy", func(t *testing.T) {
		t.Parallel()

		var fetched string
		proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fetched = r.URL.String()
			w.WriteHeader(http.StatusOK)
		})

		rec := httptest.NewRecorder()
		Handler(rules, http.NotFoundHandler(), WithProxy(proxy)).ServeHTTP(rec, request(t, "/api/v1/users"))

		if want := "https://backend.example.com/v1/users"; fetched != want {
			t.Errorf("the proxy fetched %q, want %q", fetched, want)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("without a proxy", func(t *testing.T) {
		t.Parallel()

		var got error
		errorHandler := func(_ http.ResponseWriter, _ *http.Request, err error) { got = err }

		rec := httptest.NewRecorder()
		Handler(rules, http.NotFoundHandler(), WithErrorHandler(errorHandler)).ServeHTTP(rec, request(t, "/api/v1/users"))

		if !errors.Is(got, ErrProxy) {
			t.Errorf("error = %v, want it to wrap %v", got, ErrProxy)
		}
	})

	t.Run("without an error handler", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		Handler(rules, http.NotFoundHandler()).ServeHTTP(rec, request(t, "/api/v1/users"))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		handler, err := NewHandler(strings.NewReader("/old /new\n"), http.NotFoundHandler(), WithDefaultStatus(http.StatusFound))
		if err != nil {
			t.Fatalf("NewHandler() error = %v", err)
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request(t, "/old"))

		if rec.Code != http.StatusFound {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusFound)
		}
		if got := rec.Header().Get("Location"); got != "/new" {
			t.Errorf("Location = %q, want %q", got, "/new")
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()

		if _, err := NewHandler(strings.NewReader("/only-a-source\n"), http.NotFoundHandler()); err == nil {
			t.Error("NewHandler() error = nil, want an error")
		}
	})
}

// serve serves target through a redirect handler for rules and returns the
// response and the URL the next handler was called with, empty if it was not
// called.
func serve(t *testing.T, rules []Rule, target string, opts ...HandlerOption) (rec *httptest.ResponseRecorder, served string) {
	t.Helper()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = r.URL.String()
		w.WriteHeader(http.StatusOK)
	})

	rec = httptest.NewRecorder()
	Handler(rules, next, opts...).ServeHTTP(rec, request(t, target))

	return rec, served
}

// request builds a GET request for target.
func request(t *testing.T, target string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
}
