package header

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const handlerFile = `
/*
	X-Robots-Tag: noindex

/assets/*
	Cache-Control: public, max-age=31536000
	Content-Type: text/css
	! X-Robots-Tag
`

func TestHandler(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path string
		// next is the handler the rules are applied to.
		next http.HandlerFunc
		// wantStatus is the status of the response.
		wantStatus int
		// want holds the expected fields, "-" for one that is not set.
		want map[string]string
	}{
		"a handler that writes nothing": {
			path:       "/index.html",
			next:       func(http.ResponseWriter, *http.Request) {},
			wantStatus: http.StatusOK,
			want:       map[string]string{"X-Robots-Tag": "noindex"},
		},
		"a rule wins over the field of the handler": {
			path: "/assets/app.css",
			next: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
			},
			wantStatus: http.StatusOK,
			want:       map[string]string{"Content-Type": "text/css"},
		},
		"a removal drops the field of the handler": {
			path: "/assets/app.css",
			next: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Robots-Tag", "index")
				_, _ = io.WriteString(w, "body")
			},
			wantStatus: http.StatusOK,
			want:       map[string]string{"X-Robots-Tag": "-", "Cache-Control": "public, max-age=31536000"},
		},
		"the status of the handler is kept": {
			path: "/index.html",
			next: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "gone", http.StatusGone)
			},
			wantStatus: http.StatusGone,
			want:       map[string]string{"X-Robots-Tag": "noindex"},
		},
		"a field no rule declares is kept": {
			path: "/index.html",
			next: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
			},
			wantStatus: http.StatusOK,
			want:       map[string]string{"Cache-Control": "no-store", "X-Robots-Tag": "noindex"},
		},
	}

	rules, err := Parse(strings.NewReader(handlerFile))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			Handler(rules, tt.next).ServeHTTP(rec, request(t, tt.path))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			wantFields(t, rec.Header(), tt.want)
		})
	}
}

// TestHandlerUnmatched checks that a request no rule matches keeps the response
// writer it was served with, so that the interfaces it implements survive.
func TestHandlerUnmatched(t *testing.T) {
	t.Parallel()

	rules, err := Parse(strings.NewReader("/assets/*\n  Cache-Control: no-store\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	tests := map[string]struct {
		path string
		// wantWrapped reports whether next is expected to be given the wrapper.
		wantWrapped bool
	}{
		"a path a rule matches":  {path: "/assets/app.css", wantWrapped: true},
		"a path no rule matches": {path: "/index.html", wantWrapped: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var wrapped bool
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, wrapped = w.(*responseWriter)
			})

			Handler(rules, next).ServeHTTP(httptest.NewRecorder(), request(t, tt.path))

			if wrapped != tt.wantWrapped {
				t.Errorf("response writer wrapped = %t, want %t", wrapped, tt.wantWrapped)
			}
		})
	}
}

// TestHandlerReadFrom checks that the fields are applied when the body is
// copied through the io.ReaderFrom of the wrapped writer, as net/http does when
// it serves a file.
func TestHandlerReadFrom(t *testing.T) {
	t.Parallel()

	rules, err := Parse(strings.NewReader("/*\n  X-Robots-Tag: noindex\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	var read bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rf, ok := w.(io.ReaderFrom)
		if !ok {
			t.Error("response writer does not implement io.ReaderFrom")

			return
		}
		if _, err := rf.ReadFrom(strings.NewReader("body")); err != nil {
			t.Errorf("ReadFrom() error = %v", err)
		}
		read = true
	})

	rec := httptest.NewRecorder()
	Handler(rules, next).ServeHTTP(rec, request(t, "/index.html"))

	if !read {
		t.Fatal("the body was not copied")
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q, want %q", got, "noindex")
	}
	if got := rec.Body.String(); got != "body" {
		t.Errorf("body = %q, want %q", got, "body")
	}
}

// TestHandlerFlush checks that the fields are applied before a handler that
// streams its response flushes the header.
func TestHandlerFlush(t *testing.T) {
	t.Parallel()

	rules, err := Parse(strings.NewReader("/*\n  X-Robots-Tag: noindex\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer does not implement http.Flusher")

			return
		}
		flusher.Flush()

		// A field set after the flush is too late to be sent.
		w.Header().Set("X-Robots-Tag", "index")
	})

	rec := httptest.NewRecorder()
	Handler(rules, next).ServeHTTP(rec, request(t, "/index.html"))

	if !rec.Flushed {
		t.Error("the response was not flushed")
	}
	if got := rec.Result().Header.Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q, want %q", got, "noindex")
	}
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		handler, err := NewHandler(strings.NewReader("/*\n  X-Robots-Tag: noindex\n"), http.NotFoundHandler())
		if err != nil {
			t.Fatalf("NewHandler() error = %v", err)
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request(t, "/index.html"))

		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("X-Robots-Tag = %q, want %q", got, "noindex")
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()

		if _, err := NewHandler(strings.NewReader("/*\n  no colon\n"), http.NotFoundHandler()); err == nil {
			t.Error("NewHandler() error = nil, want an error")
		}
	})
}

// request builds a GET request for path.
func request(t *testing.T, path string) *http.Request {
	t.Helper()

	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
}
