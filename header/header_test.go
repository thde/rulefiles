package header

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"thde.io/rulefiles/internal/rulefile"
)

const testFile = `
/*
	X-Robots-Tag: noindex
	Referrer-Policy: strict-origin-when-cross-origin

/assets/*
	Cache-Control: public, max-age=31536000, immutable
	! X-Robots-Tag

/docs/:slug
	X-Slug: :slug
	Vary: Accept-Language

/docs/*
	Vary: Accept-Encoding
`

func TestResolveApplyTo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path string
		// existing headers before applying rules.
		existing map[string]string
		// want contains expected headers after applying.
		want map[string]string
	}{
		"single rule": {
			path: "/index.html",
			want: map[string]string{
				"X-Robots-Tag":    "noindex",
				"Referrer-Policy": "strict-origin-when-cross-origin",
			},
		},
		"a removal wins over the rules before it": {
			path: "/assets/main.css",
			want: map[string]string{
				"X-Robots-Tag":  "-",
				"Cache-Control": "public, max-age=31536000, immutable",
			},
		},
		"a removal drops what another handler set": {
			path:     "/assets/main.css",
			existing: map[string]string{"X-Robots-Tag": "index"},
			want:     map[string]string{"X-Robots-Tag": "-"},
		},
		"a rule replaces what another handler set": {
			path:     "/index.html",
			existing: map[string]string{"X-Robots-Tag": "index"},
			want:     map[string]string{"X-Robots-Tag": "noindex"},
		},
		"repeated fields are joined": {
			path: "/docs/backups",
			want: map[string]string{"Vary": "Accept-Language, Accept-Encoding"},
		},
		"placeholders are expanded": {
			path: "/docs/backups",
			want: map[string]string{"X-Slug": "backups"},
		},
		"the splat matches deeper paths": {
			path: "/docs/object-storage/backups",
			want: map[string]string{
				"Vary":   "Accept-Encoding",
				"X-Slug": "-",
			},
		},
	}

	parsed, err := Parse(strings.NewReader(testFile))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hdr := http.Header{}
			for name, value := range test.existing {
				hdr.Set(name, value)
			}

			Resolve(parsed, test.path).ApplyTo(hdr)

			wantFields(t, hdr, test.want)
		})
	}
}

// docFile holds the examples of the two documentations of the format, adapted to
// what this package supports: the absolute URLs of the Cloudflare examples match
// on a host and the "/*.jpg" of its detach example matches inside a segment.
const docFile = `
# This is a comment
/secure/page
	X-Frame-Options: DENY
	X-Content-Type-Options: nosniff
	Referrer-Policy: no-referrer

/static/*
	Access-Control-Allow-Origin: *
	X-Robots-Tag: nosnippet
	Cache-Control: public, max-age=31556952, immutable

/*
	Content-Security-Policy: default-src 'self';

/images/*
	! Content-Security-Policy

/movies/:title
	x-movie-name: You are watching ":title"

/downloads/*
	X-Path: :splat

/cached/*
	cache-control: max-age=0
	cache-control: no-cache
	cache-control: no-store
	cache-control: must-revalidate
`

func TestResolveDocExamples(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path string
		// want contains expected headers after applying.
		want map[string]string
	}{
		// Multiple matching rules apply together.
		"a rule and the rule for every path": {
			path: "/secure/page",
			want: map[string]string{
				"X-Frame-Options":         "DENY",
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         "no-referrer",
				"Content-Security-Policy": "default-src 'self';",
			},
		},
		"fingerprinted assets": {
			path: "/static/main.4f1c.css",
			want: map[string]string{
				"Access-Control-Allow-Origin": "*",
				"X-Robots-Tag":                "nosnippet",
				"Cache-Control":               "public, max-age=31556952, immutable",
			},
		},
		"a removal detaches a field of a broader rule": {
			path: "/images/photo.jpg",
			want: map[string]string{"Content-Security-Policy": "-"},
		},
		"a placeholder is expanded into the value": {
			path: "/movies/serenity",
			want: map[string]string{"X-Movie-Name": `You are watching "serenity"`},
		},
		"a splat is expanded into the value": {
			path: "/downloads/2026/report.pdf",
			want: map[string]string{"X-Path": "2026/report.pdf"},
		},
		"a field declared repeatedly is joined": {
			path: "/cached/index.html",
			want: map[string]string{"Cache-Control": "max-age=0, no-cache, no-store, must-revalidate"},
		},
	}

	parsed, err := Parse(strings.NewReader(docFile))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hdr := http.Header{}
			Resolve(parsed, test.path).ApplyTo(hdr)

			wantFields(t, hdr, test.want)
		})
	}
}

func TestResolveNoMatch(t *testing.T) {
	t.Parallel()

	parsed, err := Parse(strings.NewReader("/docs/*\n\tX-A: 1\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	resolved := Resolve(parsed, "/index.html")
	if resolved != nil {
		t.Fatalf("Resolve() = %+v, want nil so that the response is not wrapped", resolved)
	}

	// Nil *Resolved must not panic.
	hdr := http.Header{"X-A": []string{"1"}}
	resolved.ApplyTo(hdr)
	if got := hdr.Get("X-A"); got != "1" {
		t.Errorf("X-A = %q, want a nil *Resolved to leave the header as it is", got)
	}
	if got := resolved.Fields(); got != nil {
		t.Errorf("Fields() = %v, want nil for a nil *Resolved", got)
	}
}

func TestResolveUnparsedRuleMatchesNothing(t *testing.T) {
	t.Parallel()

	// Unparsed rules must match nothing.
	rules := []Rule{{Source: "/docs", Operations: []Operation{{Name: "X-A", Value: "1"}}}}
	for _, path := range []string{"/docs", "/", ""} {
		if resolved := Resolve(rules, path); resolved != nil {
			t.Errorf("Resolve(%q) = %+v, want nil for a rule that was not parsed", path, resolved)
		}
	}
}

func TestFieldsAreSorted(t *testing.T) {
	t.Parallel()

	parsed, err := Parse(strings.NewReader("/*\n\tX-C: 1\n\tX-A: 2\n\t! X-B\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := []string{"X-A", "X-B", "X-C"}
	if got := Resolve(parsed, "/index.html").Fields(); !slices.Equal(got, want) {
		t.Errorf("Fields() = %v, want %v", got, want)
	}
}

func TestResolveDropsInvalidValues(t *testing.T) {
	t.Parallel()

	// Drop invalid header values from placeholders.
	parsed, err := Parse(strings.NewReader("/docs/:slug\n\tX-Slug: :slug\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	hdr := http.Header{}
	Resolve(parsed, "/docs/a\nX-Evil: yes").ApplyTo(hdr)

	if _, ok := hdr["X-Slug"]; ok {
		t.Errorf("X-Slug = %q, want the invalid value to be dropped", hdr.Get("X-Slug"))
	}
}

func TestResolveSetCookieIsNotJoined(t *testing.T) {
	t.Parallel()

	parsed, err := Parse(strings.NewReader("/*\n\tSet-Cookie: a=1\n\tSet-Cookie: b=2\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	hdr := http.Header{}
	Resolve(parsed, "/index.html").ApplyTo(hdr)

	if got, want := strings.Join(hdr["Set-Cookie"], "|"), "a=1|b=2"; got != want {
		t.Errorf("Set-Cookie = %q, want %q", got, want)
	}
}

// wantFields asserts expected header values.
func wantFields(t *testing.T, hdr http.Header, want map[string]string) {
	t.Helper()

	for name, want := range want {
		got := hdr.Get(name)
		if want == "-" {
			if _, ok := hdr[name]; ok {
				t.Errorf("%s = %q, want it to be absent", name, got)
			}

			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// testPattern compiles a pattern for tests.
func testPattern(t *testing.T, source string) rulefile.Pattern {
	t.Helper()

	pattern, err := rulefile.ParsePattern(source, rulefile.PatternOptions{})
	if err != nil {
		t.Fatalf("ParsePattern(%q) error = %v", source, err)
	}

	return pattern
}
