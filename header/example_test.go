package header_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"thde.io/rulefiles/header"
)

const file = `
/*
  X-Robots-Tag: noindex
  Referrer-Policy: strict-origin-when-cross-origin

/assets/*
  Cache-Control: public, max-age=31536000, immutable
  ! X-Robots-Tag

/docs/:slug
  Vary: Accept-Language
  X-Slug: :slug

/docs/*
  Vary: Accept-Encoding
`

// Example demonstrates parsing and resolving headers.
func Example() {
	rules, err := header.Parse(strings.NewReader(file))
	if err != nil {
		log.Fatal(err)
	}

	for _, path := range []string{"/index.html", "/assets/app.css", "/docs/backups"} {
		hdr := http.Header{}
		resolved := header.Resolve(rules, path)
		resolved.ApplyTo(hdr)

		fmt.Println(path)
		for _, name := range resolved.Fields() {
			if _, ok := hdr[name]; !ok {
				fmt.Println("  " + name + " is removed")

				continue
			}
			fmt.Println("  " + name + ": " + hdr.Get(name))
		}
	}

	// Output:
	// /index.html
	//   Referrer-Policy: strict-origin-when-cross-origin
	//   X-Robots-Tag: noindex
	// /assets/app.css
	//   Cache-Control: public, max-age=31536000, immutable
	//   Referrer-Policy: strict-origin-when-cross-origin
	//   X-Robots-Tag is removed
	// /docs/backups
	//   Referrer-Policy: strict-origin-when-cross-origin
	//   Vary: Accept-Language, Accept-Encoding
	//   X-Robots-Tag: noindex
	//   X-Slug: backups
}

// ExampleHandler demonstrates wrapping a handler with header rules.
func ExampleHandler() {
	const file = `
/*
  X-Robots-Tag: noindex

/assets/*
  Cache-Control: public, max-age=31536000, immutable
  Content-Type: text/css
  ! X-Robots-Tag
`

	rules, err := header.Parse(strings.NewReader(file))
	if err != nil {
		slog.Error("reading the header rules", "error", err)

		return
	}

	// site sets fields of its own, as a file server does.
	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Robots-Tag", "index")

		//nolint:gosec // Echo path for example output.
		_, _ = fmt.Fprint(w, "the file at "+r.URL.Path)
	})

	handler := header.Handler(rules, site)

	for _, path := range []string{"/index.html", "/assets/app.css"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))

		fmt.Printf("%s\n  Content-Type: %q\n  Cache-Control: %q\n  X-Robots-Tag: %q\n",
			path, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"), rec.Header().Get("X-Robots-Tag"))
	}

	// Output:
	// /index.html
	//   Content-Type: "text/plain; charset=utf-8"
	//   Cache-Control: ""
	//   X-Robots-Tag: "noindex"
	// /assets/app.css
	//   Content-Type: "text/css"
	//   Cache-Control: "public, max-age=31536000, immutable"
	//   X-Robots-Tag: ""
}

// ExampleNewHandler demonstrates reading rules from a file into a handler.
func ExampleNewHandler() {
	site := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Echo path for example output.
		_, _ = fmt.Fprint(w, "the file at "+r.URL.Path)
	})

	handler, err := header.NewHandler(strings.NewReader("/assets/*\n  Cache-Control: no-store\n"), site)
	if err != nil {
		slog.Error("reading the header rules", "error", err)

		return
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/assets/app.css", http.NoBody))

	fmt.Printf("%d Cache-Control %q\n", rec.Code, rec.Header().Get("Cache-Control"))

	// Output:
	// 200 Cache-Control "no-store"
}

// ExampleResolve demonstrates applying headers to responses.
func ExampleResolve() {
	rules, err := header.Parse(strings.NewReader("/assets/*\n  Cache-Control: no-store\n"))
	if err != nil {
		log.Fatal(err)
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		header.Resolve(rules, r.URL.Path).ApplyTo(w.Header())

		//nolint:gosec // example handler only
		_, _ = fmt.Fprintln(w, "the body of "+r.URL.Path)
	}

	for _, path := range []string{"/assets/app.css", "/index.html"} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))

		fmt.Printf("%s: Cache-Control %q\n", path, rec.Header().Get("Cache-Control"))
	}

	// Output:
	// /assets/app.css: Cache-Control "no-store"
	// /index.html: Cache-Control ""
}

// ExampleResolve_placeholders demonstrates path placeholder expansion.
func ExampleResolve_placeholders() {
	const file = `
/movies/:title
  X-Movie-Name: You are watching ":title"

/downloads/*
  X-Path: :splat
`

	rules, err := header.Parse(strings.NewReader(file))
	if err != nil {
		log.Fatal(err)
	}

	for _, path := range []string{"/movies/serenity", "/downloads/2026/report.pdf"} {
		hdr := http.Header{}
		header.Resolve(rules, path).ApplyTo(hdr)

		fmt.Printf("%s\n  X-Movie-Name: %q\n  X-Path: %q\n", path, hdr.Get("X-Movie-Name"), hdr.Get("X-Path"))
	}

	// Output:
	// /movies/serenity
	//   X-Movie-Name: "You are watching \"serenity\""
	//   X-Path: ""
	// /downloads/2026/report.pdf
	//   X-Movie-Name: ""
	//   X-Path: "2026/report.pdf"
}

// ExampleResolved_ApplyTo demonstrates modifying existing headers.
func ExampleResolved_ApplyTo() {
	rules, err := header.Parse(strings.NewReader("/assets/*\n  ! X-Robots-Tag\n  Vary: Accept-Encoding\n"))
	if err != nil {
		log.Fatal(err)
	}

	hdr := http.Header{"X-Robots-Tag": []string{"index"}, "Vary": []string{"Cookie"}}
	header.Resolve(rules, "/assets/app.css").ApplyTo(hdr)

	_, ok := hdr["X-Robots-Tag"]
	fmt.Println("X-Robots-Tag is set:", ok)
	fmt.Println("Vary:", hdr.Get("Vary"))

	// Output:
	// X-Robots-Tag is set: false
	// Vary: Accept-Encoding
}

// ExampleResolved_Fields demonstrates listing touched fields.
func ExampleResolved_Fields() {
	rules, err := header.Parse(strings.NewReader(file))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(header.Resolve(rules, "/assets/app.css").Fields())

	// Output:
	// [Cache-Control Referrer-Policy X-Robots-Tag]
}

// ExampleWithExactTrailingSlash demonstrates exact slash matching.
func ExampleWithExactTrailingSlash() {
	const file = `
/docs/
  X-Rule: directory

/docs
  X-Rule: file
`

	for _, opts := range [][]header.Option{nil, {header.WithExactTrailingSlash()}} {
		rules, err := header.Parse(strings.NewReader(file), opts...)
		if err != nil {
			log.Fatal(err)
		}

		for _, path := range []string{"/docs", "/docs/"} {
			hdr := http.Header{}
			header.Resolve(rules, path).ApplyTo(hdr)

			fmt.Printf("%s: X-Rule %q\n", path, hdr.Get("X-Rule"))
		}
	}

	// Output:
	// /docs: X-Rule "directory, file"
	// /docs/: X-Rule "directory, file"
	// /docs: X-Rule "file"
	// /docs/: X-Rule "directory"
}
