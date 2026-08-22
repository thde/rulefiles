package redirect

import (
	"errors"
	"strings"
	"testing"

	"thde.io/rulefiles/internal/rulefile"
)

func TestParse(t *testing.T) {
	t.Parallel()

	const documentedFile = `
/home301 / 301
/home302 / 302
/querystrings /?query=string 301
/twitch https://twitch.tv
/trailing /trailing/ 301
/notrailing/ /nottrailing 301
/page/ /page2/#fragment 301
/blog/* https://blog.my.domain/:splat
/products/:code/:name /products?code=:code&name=:name
`

	// The default status is the one a rule that omits it is given.
	const defaultStatusFile = "/old /new\n/explicit /new 307\n"

	tests := map[string]struct {
		file string
		opts []Option
		// want is the canonical text of the parsed rules.
		want string
	}{
		"comments and blank lines": {
			file: `
# a comment
/old-path      /new-path       301   # a trailing comment

/blog/*        /posts/:splat
/forced/*      /posts/:splat   302!
/gone          /nowhere        410
`,
			want: "/old-path /new-path 301\n" +
				"/blog/* /posts/:splat 301\n" +
				"/forced/* /posts/:splat 302!\n" +
				"/gone /nowhere 410\n",
		},
		"tabs between fields": {
			file: "/old\t/new\t301\t# a comment\n/page /page2#fragment\n",
			want: "/old /new 301\n" +
				"/page /page2#fragment 301\n",
		},
		"the documented file": {
			file: documentedFile,
			want: "/home301 / 301\n" +
				"/home302 / 302\n" +
				"/querystrings /?query=string 301\n" +
				"/twitch https://twitch.tv 301\n" +
				"/trailing /trailing/ 301\n" +
				"/notrailing/ /nottrailing 301\n" +
				"/page/ /page2/#fragment 301\n" +
				"/blog/* https://blog.my.domain/:splat 301\n" +
				"/products/:code/:name /products?code=:code&name=:name 301\n",
		},
		"the default status of netlify": {
			file: defaultStatusFile,
			want: "/old /new 301\n/explicit /new 307\n",
		},
		"the default status of cloudflare": {
			file: defaultStatusFile,
			opts: []Option{WithDefaultStatus(302)},
			want: "/old /new 302\n/explicit /new 307\n",
		},
		"a default status that rewrites": {
			file: defaultStatusFile,
			opts: []Option{WithDefaultStatus(200)},
			want: "/old /new 200\n/explicit /new 307\n",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rules, err := Parse(strings.NewReader(test.file), test.opts...)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got := writeRules(rules); got != test.want {
				t.Errorf("Parse() rules =\n%s\nwant\n%s", got, test.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	file := `/only-a-source
/bad-status /target 200x
/unknown/:year /target/:month
`

	_, err := Parse(strings.NewReader(file))
	if err == nil {
		t.Fatal("Parse() error = nil, want errors for all three lines")
	}
	for _, line := range []string{"line 1", "line 2", "line 3"} {
		if !strings.Contains(err.Error(), line) {
			t.Errorf("Parse() error = %v, want it to report %s", err, line)
		}
	}
	// The kind of the line errors survives being reported together.
	if !errors.Is(err, ErrSyntax) {
		t.Errorf("Parse() error = %v, want it to wrap %v", err, ErrSyntax)
	}
}

func TestParseLineTooLong(t *testing.T) {
	t.Parallel()

	file := "/old /new\n/" + strings.Repeat("a", rulefile.MaxLineLen) + " /new\n"

	_, err := Parse(strings.NewReader(file))
	if err == nil {
		t.Fatal("Parse() error = nil, want the long line to be reported")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("Parse() error = %v, want it to report the line it stopped at", err)
	}
}

func TestParseRule(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		line string
		opts []Option
		// want is the error kind the line is rejected with, nil if it parses.
		want error
	}{
		"path to path":             {line: "/old /new"},
		"explicit status":          {line: "/old /new 302"},
		"forced status":            {line: "/old /new 302!"},
		"splat":                    {line: "/blog/* /posts/:splat"},
		"placeholders":             {line: "/:year/:slug /blog/:slug/:year"},
		"absolute target":          {line: "/old https://example.com/new"},
		"target with port":         {line: "/old https://example.com:8443/new"},
		"rewrite":                  {line: "/spa/* /index.html 200"},
		"error status":             {line: "/gone /404.html 410"},
		"missing target":           {line: "/old", want: ErrSyntax},
		"extra condition":          {line: "/old /new 301 Country=ch", want: ErrUnsupported},
		"unknown status":           {line: "/old /new 601", want: ErrUnsupported},
		"invalid status":           {line: "/old /new soon", want: ErrSyntax},
		"relative source":          {line: "old /new", want: ErrUnsupported},
		"absolute source":          {line: "https://example.com/old /new", want: ErrUnsupported},
		"query in source":          {line: "/old?page=2 /new", want: ErrUnsupported},
		"splat in the middle":      {line: "/blog/*/comments /posts/:splat", want: ErrUnsupported},
		"partial splat":            {line: "/blog/po* /posts", want: ErrUnsupported},
		"duplicate placeholder":    {line: "/:slug/:slug /blog/:slug", want: ErrSyntax},
		"uncaptured splat":         {line: "/blog /posts/:splat", want: ErrSyntax},
		"uncaptured placeholder":   {line: "/:year /blog/:month", want: ErrSyntax},
		"proxied rewrite":          {line: "/api/* https://example.com/:splat 200", want: ErrUnsupported},
		"placeholder without name": {line: "/: /new", want: ErrSyntax},
		"target is no URL":         {line: "/api/* :splat", want: ErrSyntax},
		"invalid escape in target": {line: "/old /new%zz", want: ErrSyntax},
		"placeholder in the host":  {line: "/:sub/* https://:sub.example.com/:splat", want: ErrSyntax},

		// Test cases from provider documentation.
		"docs: see other":                 {line: "/home / 303"},
		"docs: temporary redirect":        {line: "/home / 307"},
		"docs: permanent redirect":        {line: "/home / 308"},
		"docs: custom 404 page":           {line: "/en/* /en/404.html 404"},
		"docs: forced rewrite":            {line: "/best-pets/dogs /best-pets/cats.html 200!"},
		"docs: target without a path":     {line: "/twitch https://twitch.tv"},
		"docs: target with a fragment":    {line: "/page/ /page2/#fragment 301"},
		"docs: placeholders in a query":   {line: "/products/:code/:name /products?code=:code&name=:name"},
		"docs: query parameter condition": {line: "/store id=:id /blog/:id 301", want: ErrUnsupported},
		"docs: query parameter only":      {line: "/path/* param1=:value1 /otherpath/:value1/:splat", want: ErrSyntax},
		"docs: country condition":         {line: "/ /anz 302 Country=au,nz", want: ErrUnsupported},
		"docs: language condition":        {line: "/israel/* /israel/he/:splat 302 Language=he", want: ErrUnsupported},
		"docs: cookie condition":          {line: "/* /legacy/:splat 200 Cookie=is_legacy,my_other_cookie", want: ErrUnsupported},
		"docs: domain level redirect":     {line: "http://blog.yoursite.com/* https://www.yoursite.com/blog/:splat 301!", want: ErrUnsupported},
		"docs: proxy to another host":     {line: "https://frontend.yoursite.com/login/* https://backend.yoursite.com/:splat 200", want: ErrUnsupported},
		"docs: encoded source":            {line: "/authors/c%C3%A9line /authors/about-c%C3%A9line"},

		// Test URL-encoded source validation.
		"escaped slash in the source":  {line: "/a%2Fb /new", want: ErrUnsupported},
		"invalid escape in the source": {line: "/100% /new", want: ErrSyntax},

		// Test authority target validation.
		"rewrite to an authority":  {line: "/spa/* //example.com/x 200", want: ErrUnsupported},
		"redirect to an authority": {line: "/old //example.com/new"},

		// Test proxy target validation.
		"proxy: absolute target":   {line: "/api/* https://example.com/:splat 200", opts: []Option{WithProxying()}},
		"proxy: target with port":  {line: "/api/* https://example.com:8443/x 200", opts: []Option{WithProxying()}},
		"proxy: path is still ok":  {line: "/spa/* /index.html 200", opts: []Option{WithProxying()}},
		"proxy: authority target":  {line: "/api/* //example.com/x 200", opts: []Option{WithProxying()}, want: ErrSyntax},
		"proxy: opaque target":     {line: "/api/* mailto:x@example.com 200", opts: []Option{WithProxying()}, want: ErrSyntax},
		"proxy: relative target":   {line: "/api/* new 200", opts: []Option{WithProxying()}, want: ErrSyntax},
		"proxy: host in a capture": {line: "/api/:host https://:host/x 200", opts: []Option{WithProxying()}, want: ErrSyntax},

		// Test field separator whitespace.
		"tab separated":              {line: "/old\t/new\t301"},
		"other whitespace is no gap": {line: "/\r#", want: ErrSyntax},
		"unicode space is no gap":    {line: "/a /b", want: ErrSyntax},
		"vertical tab is no gap":     {line: "/old\v/new", want: ErrSyntax},

		// Test options during rule parsing.
		"exact: trailing slash": {line: "/old/ /new 301", opts: []Option{WithExactTrailingSlash()}},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parseRule(test.line, newOptions(test.opts))
			if test.want == nil {
				if err != nil {
					t.Fatalf("parseRule(%q) error = %v, want none", test.line, err)
				}

				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("parseRule(%q) error = %v, want it to wrap %v", test.line, err, test.want)
			}
			// The two kinds are exclusive.
			other := ErrUnsupported
			if errors.Is(test.want, ErrUnsupported) {
				other = ErrSyntax
			}
			if errors.Is(err, other) {
				t.Errorf("parseRule(%q) error = %v, want it not to wrap %v as well", test.line, err, other)
			}
		})
	}
}

// TestParseWithInvalidDefaultStatus tests invalid default statuses.
func TestParseWithInvalidDefaultStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []int{0, 201, 299, 600} {
		_, err := Parse(strings.NewReader("/old /new\n"), WithDefaultStatus(status))
		if err == nil {
			t.Errorf("Parse(WithDefaultStatus(%d)) error = nil, want the status to be rejected", status)

			continue
		}
		if !strings.Contains(err.Error(), "default status") {
			t.Errorf("Parse(WithDefaultStatus(%d)) error = %v, want it to name the default status", status, err)
		}
	}
}
