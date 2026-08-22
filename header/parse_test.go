package header

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		file string
		// want is the canonical text of the parsed rules.
		want string
	}{
		"comments and blank lines": {
			file: "# a comment\n" +
				"/*\n" +
				"\tX-Robots-Tag: noindex\t# a trailing comment\n" +
				"\n" +
				"/static/*\n" +
				"\tCache-Control: public, max-age=31536000, immutable\n" +
				"\t! X-Robots-Tag\n",
			want: "/*\n" +
				"\tX-Robots-Tag:noindex\n" +
				"/static/*\n" +
				"\tCache-Control:public, max-age=31536000, immutable\n" +
				"\t!X-Robots-Tag\n",
		},
		// The documented example indents with spaces and comments every path.
		"comments between the headers": {
			file: "# a path:\n" +
				"/templates/index.html\n" +
				"  # headers for that path:\n" +
				"  X-Frame-Options: DENY\n" +
				"# another path:\n" +
				"/templates/index2.html\n" +
				"  # headers for that path:\n" +
				"  X-Frame-Options: SAMEORIGIN\n",
			want: "/templates/index.html\n" +
				"\tX-Frame-Options:DENY\n" +
				"/templates/index2.html\n" +
				"\tX-Frame-Options:SAMEORIGIN\n",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parsed, err := Parse(strings.NewReader(test.file))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got := writeRules(parsed); got != test.want {
				t.Errorf("Parse() rules =\n%s\nwant\n%s", got, test.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		file string
		// lines are the lines expected in the error output, which may report no others.
		lines []string
		// want is the error kind the file is rejected with.
		want error
	}{
		"a header without a path": {
			file:  "\tX-Robots-Tag: noindex\n",
			lines: []string{"line 1"},
			want:  ErrSyntax,
		},
		"a path without headers": {
			file:  "/*\n\tX-A: 1\n/static/*\n/other\n\tX-B: 2\n",
			lines: []string{"line 3"},
			want:  ErrSyntax,
		},
		"a trailing path without headers": {
			file:  "/*\n\tX-A: 1\n/static/*\n",
			lines: []string{"line 3"},
			want:  ErrSyntax,
		},
		// The headers of a path that is rejected are not reported again.
		"an invalid path skips its headers": {
			file:  "relative/path\n\tX-A: 1\n\tX-B: 2\n",
			lines: []string{"line 1"},
			want:  ErrUnsupported,
		},
		"a duplicate placeholder": {
			file:  "/:a/:a\n\tX-A: 1\n",
			lines: []string{"line 1"},
			want:  ErrSyntax,
		},
		"an escaped slash in a path": {
			file:  "/a%2Fb\n\tX-A: 1\n",
			lines: []string{"line 1"},
			want:  ErrUnsupported,
		},
		"a query in the path": {
			file:  "/old?page=2\n\tX-A: 1\n",
			lines: []string{"line 1"},
			want:  ErrUnsupported,
		},
		"a splat in the middle of the path": {
			file:  "/a/*/b\n\tX-A: 1\n",
			lines: []string{"line 1"},
			want:  ErrUnsupported,
		},
		// Unsupported features from documentation examples.
		"docs: an absolute path": {
			file:  "https://myproject.pages.dev/*\n\tX-Robots-Tag: noindex\n",
			lines: []string{"line 1"},
			want:  ErrUnsupported,
		},
		"docs: a wildcard inside a segment": {
			file:  "/*.jpg\n\t! Content-Security-Policy\n",
			lines: []string{"line 1"},
			want:  ErrUnsupported,
		},
		"a header without a colon": {
			file:  "/*\n\tX-Robots-Tag\n\tX-A: 1\n",
			lines: []string{"line 2"},
			want:  ErrSyntax,
		},
		"a header without a name": {
			file:  "/*\n\t: noindex\n\tX-A: 1\n",
			lines: []string{"line 2"},
			want:  ErrSyntax,
		},
		"an invalid field name": {
			file:  "/*\n\tX Spaces: 1\n\tX-A: 1\n",
			lines: []string{"line 2"},
			want:  ErrSyntax,
		},
		"an invalid field value": {
			file:  "/*\n\tX-A: a\x00b\n\tX-B: 1\n",
			lines: []string{"line 2"},
			want:  ErrSyntax,
		},
		"a removal with a value": {
			file:  "/*\n\t! X-A: 1\n\tX-A: 1\n",
			lines: []string{"line 2"},
			want:  ErrSyntax,
		},
		"an uncaptured placeholder": {
			file:  "/blog/:slug\n\tX-Name: :title\n\tX-A: 1\n",
			lines: []string{"line 2"},
			want:  ErrSyntax,
		},
		"a framing field": {
			file:  "/*\n\tContent-Length: 0\n\tX-A: 1\n",
			lines: []string{"line 2"},
			want:  ErrUnsupported,
		},
		"the removal of a framing field": {
			file:  "/*\n\t! Content-Length\n\tX-A: 1\n",
			lines: []string{"line 2"},
			want:  ErrUnsupported,
		},
		"a transfer encoding": {
			file:  "/*\n\tTransfer-Encoding: chunked\n\tX-A: 1\n",
			lines: []string{"line 2"},
			want:  ErrUnsupported,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse(strings.NewReader(test.file))
			if err == nil {
				t.Fatalf("Parse() error = nil, want errors for %v", test.lines)
			}
			for _, line := range test.lines {
				if !strings.Contains(err.Error(), line) {
					t.Errorf("Parse() error = %v, want it to report %s", err, line)
				}
			}
			if got, want := strings.Count(err.Error(), "line "), len(test.lines); got != want {
				t.Errorf("Parse() reported %d lines, want %d: %v", got, want, err)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Parse() error = %v, want it to wrap %v", err, test.want)
			}
			// The two kinds are exclusive.
			other := ErrUnsupported
			if errors.Is(test.want, ErrUnsupported) {
				other = ErrSyntax
			}
			if errors.Is(err, other) {
				t.Errorf("Parse() error = %v, want it not to wrap %v as well", err, other)
			}
		})
	}
}

func TestParseOperation(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		line    string
		want    Operation
		wantErr bool
	}{
		"set":                 {line: "X-Robots-Tag: noindex", want: Operation{Name: "X-Robots-Tag", Value: "noindex"}},
		"canonical name":      {line: "x-robots-tag: noindex", want: Operation{Name: "X-Robots-Tag", Value: "noindex"}},
		"value with a colon":  {line: "Content-Security-Policy: default-src 'self'; img-src https://a:8443", want: Operation{Name: "Content-Security-Policy", Value: "default-src 'self'; img-src https://a:8443"}},
		"empty value":         {line: "X-A:", want: Operation{Name: "X-A"}},
		"extra whitespace":    {line: "X-A :  1 ", want: Operation{Name: "X-A", Value: "1"}},
		"remove":              {line: "! X-Robots-Tag", want: Operation{Name: "X-Robots-Tag", Remove: true}},
		"remove without gap":  {line: "!X-Robots-Tag", want: Operation{Name: "X-Robots-Tag", Remove: true}},
		"missing colon":       {line: "X-Robots-Tag noindex", wantErr: true},
		"missing name":        {line: ": noindex", wantErr: true},
		"remove without name": {line: "!", wantErr: true},
		"remove with a value": {line: "! X-A: 1", wantErr: true},
		"framing name":        {line: "Content-Length: 0", wantErr: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Check values against empty pattern.
			pattern := testPattern(t, "/*")

			got, err := parseOperation(test.line, pattern)
			if gotErr := err != nil; gotErr != test.wantErr {
				t.Fatalf("parseOperation(%q) error = %v, want error = %v", test.line, err, test.wantErr)
			}
			if test.wantErr {
				// Verify error category.
				if !errors.Is(err, ErrSyntax) && !errors.Is(err, ErrUnsupported) {
					t.Errorf("parseOperation(%q) error = %v, want it to wrap ErrSyntax or ErrUnsupported", test.line, err)
				}

				return
			}
			if got != test.want {
				t.Errorf("parseOperation(%q) = %+v, want %+v", test.line, got, test.want)
			}
		})
	}
}

// TestParseWithExactTrailingSlash tests exact slash matching.
func TestParseWithExactTrailingSlash(t *testing.T) {
	t.Parallel()

	file := "/docs/\n  X-Rule: directory\n\n/docs\n  X-Rule: file\n\n/assets/*\n  X-Path: :splat\n"

	tests := map[string]struct {
		opts []Option
		want map[string]string // expected headers per path
	}{
		// Trailing slashes match loosely by default.
		"netlify": {want: map[string]string{
			"/docs":              "directory, file",
			"/docs/":             "directory, file",
			"/assets/app.css":    "app.css",
			"/assets/js/app.js/": "js/app.js",
		}},
		"cloudflare": {opts: []Option{WithExactTrailingSlash()}, want: map[string]string{
			"/docs":              "file",
			"/docs/":             "directory",
			"/assets/app.css":    "app.css",
			"/assets/js/app.js/": "js/app.js/",
		}},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rules, err := Parse(strings.NewReader(file), test.opts...)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			for path, want := range test.want {
				hdr := http.Header{}
				Resolve(rules, path).ApplyTo(hdr)

				got := hdr.Get("X-Rule") + hdr.Get("X-Path")
				if got != want {
					t.Errorf("Resolve(%q) = %q, want %q", path, got, want)
				}
			}
		})
	}
}
