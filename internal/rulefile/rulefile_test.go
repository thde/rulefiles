package rulefile

import (
	"slices"
	"strings"
	"testing"
)

func TestParsePattern(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source  string
		wantErr bool
	}{
		"root":                     {source: "/"},
		"literal":                  {source: "/docs/backups"},
		"splat":                    {source: "/blog/*"},
		"splat everything":         {source: "/*"},
		"placeholders":             {source: "/:year/:slug"},
		"relative":                 {source: "docs/backups", wantErr: true},
		"absolute":                 {source: "https://example.com/docs", wantErr: true},
		"query":                    {source: "/docs?page=2", wantErr: true},
		"fragment":                 {source: "/docs#section", wantErr: true},
		"splat in the middle":      {source: "/blog/*/comments", wantErr: true},
		"partial splat":            {source: "/blog/po*", wantErr: true},
		"duplicate placeholder":    {source: "/:slug/:slug", wantErr: true},
		"placeholder without name": {source: "/:", wantErr: true},
		// Wildcards within segments are unsupported.
		"extension splat": {source: "/*.jpg", wantErr: true},
		// Placeholders cannot share a segment with wildcards.
		"placeholder before a splat": {source: "/templates/:placeholder*", wantErr: true},
		"splat before a placeholder": {source: "/templates/*:placeholder", wantErr: true},
		// Literal segments are URL-decoded.
		"encoded literal":     {source: "/authors/c%C3%A9line"},
		"escaped colon":       {source: "/%3Anot-a-placeholder"},
		"escaped slash":       {source: "/a%2Fb", wantErr: true},
		"invalid escape":      {source: "/100%", wantErr: true},
		"invalid escape late": {source: "/docs/a%zz", wantErr: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ParsePattern(test.source, PatternOptions{})
			if gotErr := err != nil; gotErr != test.wantErr {
				t.Fatalf("ParsePattern(%q) error = %v, want error = %v", test.source, err, test.wantErr)
			}
		})
	}
}

func TestPatternMatch(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source string
		path   string
		want   string // key=value captures, or "-" for none
		opts   PatternOptions
	}{
		"exact match":                 {source: "/docs", path: "/docs", want: ""},
		"no match":                    {source: "/docs", path: "/other", want: "-"},
		"prefix is no match":          {source: "/docs", path: "/docs/deeper", want: "-"},
		"case sensitive":              {source: "/docs", path: "/Docs", want: "-"},
		"pattern trailing slash":      {source: "/docs/", path: "/docs", want: ""},
		"path trailing slash":         {source: "/docs", path: "/docs/", want: ""},
		"root":                        {source: "/", path: "/", want: ""},
		"root is no prefix":           {source: "/", path: "/docs", want: "-"},
		"splat":                       {source: "/blog/*", path: "/blog/2026/hello", want: "splat=2026/hello"},
		"empty splat":                 {source: "/blog/*", path: "/blog", want: "splat="},
		"splat everything":            {source: "/*", path: "/a/b", want: "splat=a/b"},
		"placeholders":                {source: "/:year/:slug", path: "/2026/hello", want: "slug=hello year=2026"},
		"placeholder needs a segment": {source: "/:year/:slug", path: "/2026", want: "-"},
		"literal and placeholder":     {source: "/docs/:slug", path: "/docs/backups", want: "slug=backups"},

		// Decoded patterns match decoded paths.
		"encoded literal":            {source: "/authors/c%C3%A9line", path: "/authors/céline", want: ""},
		"unencoded literal":          {source: "/authors/céline", path: "/authors/céline", want: ""},
		"encoded space":              {source: "/a%20b", path: "/a b", want: ""},
		"an escaped percent":         {source: "/a%2520b", path: "/a%20b", want: ""},
		"an escaped colon":           {source: "/%3Aslug", path: "/:slug", want: ""},
		"an escaped colon literally": {source: "/%3Aslug", path: "/anything", want: "-"},

		// Exact trailing slash matching.
		"exact: pattern trailing slash":  {source: "/docs/", path: "/docs", want: "-", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: path trailing slash":     {source: "/docs", path: "/docs/", want: "-", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: both trailing slashes":   {source: "/docs/", path: "/docs/", want: "", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: neither trailing slash":  {source: "/docs", path: "/docs", want: "", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: root":                    {source: "/", path: "/", want: "", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: root is no empty path":   {source: "/", path: "", want: "-", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: placeholder":             {source: "/blog/:slug", path: "/blog/hello/", want: "-", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: splat keeps the slash":   {source: "/blog/*", path: "/blog/2026/hello/", want: "splat=2026/hello/", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: splat without a slash":   {source: "/blog/*", path: "/blog/2026/hello", want: "splat=2026/hello", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: splat of nothing":        {source: "/blog/*", path: "/blog/", want: "splat=", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: splat everything":        {source: "/*", path: "/a/b/", want: "splat=a/b/", opts: PatternOptions{ExactTrailingSlash: true}},
		"exact: splat of the root":       {source: "/*", path: "/", want: "splat=", opts: PatternOptions{ExactTrailingSlash: true}},
		"default: a slash is ignored":    {source: "/docs/", path: "/docs", want: ""},
		"default: a splat drops a slash": {source: "/blog/*", path: "/blog/2026/hello/", want: "splat=2026/hello"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pattern, err := ParsePattern(test.source, test.opts)
			if err != nil {
				t.Fatalf("ParsePattern(%q) error = %v", test.source, err)
			}

			captures, ok := pattern.Match(test.path)
			if !ok {
				if test.want != "-" {
					t.Fatalf("%q does not match %q, want the captures %q", test.path, test.source, test.want)
				}
				return
			}
			if test.want == "-" {
				t.Fatalf("%q matches %q, want no match", test.path, test.source)
			}

			var pairs []string
			for name, value := range captures {
				pairs = append(pairs, name+"="+value)
			}
			slices.Sort(pairs)
			if got := strings.Join(pairs, " "); got != test.want {
				t.Errorf("Match(%q) captures = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestZeroPatternMatchesNothing(t *testing.T) {
	t.Parallel()

	// Uninitialized patterns must match nothing.
	var pattern Pattern
	for _, path := range []string{"/", "", "/docs", "/a/b"} {
		if _, ok := pattern.Match(path); ok {
			t.Errorf("Match(%q) = true, want false for the zero Pattern", path)
		}
	}
}

func TestPatternUncaptured(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source string
		s      string
		want   string
	}{
		"no placeholder":     {source: "/blog/:slug", s: "/posts", want: ""},
		"captured":           {source: "/blog/:slug", s: "/posts/:slug", want: ""},
		"captured splat":     {source: "/blog/*", s: "/posts/:splat", want: ""},
		"uncaptured":         {source: "/blog/:slug", s: "/posts/:year", want: "year"},
		"uncaptured splat":   {source: "/blog", s: "/posts/:splat", want: "splat"},
		"several uncaptured": {source: "/blog", s: ":a/:b", want: "a b"},
		"port is no capture": {source: "/blog", s: "https://example.com:8443/posts", want: ""},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pattern, err := ParsePattern(test.source, PatternOptions{})
			if err != nil {
				t.Fatalf("ParsePattern(%q) error = %v", test.source, err)
			}

			want := strings.Fields(test.want)
			got := pattern.Uncaptured(test.s)
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("Uncaptured(%q) = %v, want %v", test.s, got, want)
			}
		})
	}
}

func TestExpand(t *testing.T) {
	t.Parallel()

	captures := map[string]string{"slug": "backups", "year": "2026"}

	tests := map[string]string{
		"/posts":                     "/posts",
		"/posts/:slug":               "/posts/backups",
		"/posts/:year/:slug":         "/posts/2026/backups",
		"/posts/:slug-:year":         "/posts/backups-2026",
		"/posts/:month":              "/posts/:month",
		"https://example.com:8443/x": "https://example.com:8443/x",
		"a :: b":                     "a :: b",
		"trailing colon:":            "trailing colon:",
	}

	for s, want := range tests {
		if got := Expand(s, captures); got != want {
			t.Errorf("Expand(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestStripComment(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"# a comment":              "",
		"  # an indented comment":  "  ",
		"/old /new # a redirect":   "/old /new ",
		"/old /new#section":        "/old /new#section",
		"/old /new":                "/old /new",
		"\tX-Robots-Tag: noindex#": "\tX-Robots-Tag: noindex#",
		"/old\t/new\t# a comment":  "/old\t/new\t",
		// Only spaces and tabs start comments.
		"/\r#":       "/\r#",
		"/old\v#new": "/old\v#new",
		"/a\u00a0#b": "/a\u00a0#b",
	}

	for line, want := range tests {
		if got := StripComment(line); got != want {
			t.Errorf("StripComment(%q) = %q, want %q", line, got, want)
		}
	}
}
