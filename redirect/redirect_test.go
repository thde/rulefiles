package redirect

import (
	"net/url"
	"strings"
	"testing"
)

func TestRuleDestination(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		rule string
		from string
		want string // "-" if no rule matches.
		opts []Option
	}{
		"exact match":                 {rule: "/old /new", from: "/old", want: "/new"},
		"no match":                    {rule: "/old /new", from: "/other", want: "-"},
		"prefix is no match":          {rule: "/old /new", from: "/old/deeper", want: "-"},
		"source trailing slash":       {rule: "/old/ /new", from: "/old", want: "/new"},
		"request trailing slash":      {rule: "/old /new", from: "/old/", want: "/new"},
		"case sensitive":              {rule: "/old /new", from: "/Old", want: "-"},
		"splat":                       {rule: "/blog/* /posts/:splat", from: "/blog/2026/hello", want: "/posts/2026/hello"},
		"empty splat":                 {rule: "/blog/* /posts/:splat", from: "/blog", want: "/posts/"},
		"splat everything":            {rule: "/* /docs/:splat", from: "/a/b", want: "/docs/a/b"},
		"placeholders":                {rule: "/:year/:slug /blog/:slug-:year", from: "/2026/hello", want: "/blog/hello-2026"},
		"placeholder needs a segment": {rule: "/:year/:slug /blog/:slug", from: "/2026", want: "-"},
		"absolute target":             {rule: "/old https://example.com/new", from: "/old", want: "https://example.com/new"},
		"escaped target":              {rule: "/blog/* /posts/:splat", from: "/blog/a b", want: "/posts/a%20b"},
		"query is kept":               {rule: "/old /new", from: "/old?page=2", want: "/new?page=2"},
		"target query wins":           {rule: "/old /new?page=1", from: "/old?page=2", want: "/new?page=1"},
		"queries are merged":          {rule: "/old /new?page=1", from: "/old?locale=de", want: "/new?locale=de&page=1"},
		"placeholder in the query":    {rule: "/blog/:slug /new?post=:slug", from: "/blog/hello", want: "/new?post=hello"},
		"placeholder in the fragment": {rule: "/blog/:slug /new#:slug", from: "/blog/hello", want: "/new#hello"},

		// Test cases from provider documentation.
		"docs: splat":                   {rule: "/news/* /blog/:splat", from: "/news/2026/hello", want: "/blog/2026/hello"},
		"docs: reordered placeholders":  {rule: "/news/:month/:date/:year/:slug /blog/:year/:month/:date/:slug", from: "/news/01/02/2026/hello", want: "/blog/2026/01/02/hello"},
		"docs: placeholder in a target": {rule: "/movies/:title /media/:title", from: "/movies/Top%20Gun", want: "/media/Top%20Gun"},
		"docs: custom 404 page":         {rule: "/en/* /en/404.html 404", from: "/en/missing", want: "/en/404.html"},
		"docs: forced rewrite":          {rule: "/best-pets/dogs /best-pets/cats.html 200!", from: "/best-pets/dogs", want: "/best-pets/cats.html"},
		"docs: placeholders in a query": {rule: "/products/:code/:name /products?code=:code&name=:name", from: "/products/1/hat", want: "/products?code=1&name=hat"},
		"docs: target without a path":   {rule: "/twitch https://twitch.tv", from: "/twitch", want: "https://twitch.tv"},
		"docs: target with a query":     {rule: "/querystrings /?query=string", from: "/querystrings", want: "/?query=string"},
		"docs: target with a fragment":  {rule: "/page/ /page2/#fragment", from: "/page/", want: "/page2/#fragment"},
		"docs: external splat":          {rule: "/blog/* https://blog.my.domain/:splat", from: "/blog/a/b", want: "https://blog.my.domain/a/b"},

		// Test URL-encoded and literal characters.
		"docs: encoded source":   {rule: "/authors/c%C3%A9line /authors/about-c%C3%A9line", from: "/authors/c%C3%A9line", want: "/authors/about-c%C3%A9line"},
		"encoded source only":    {rule: "/authors/c%C3%A9line /authors/about-c%C3%A9line", from: "/authors/céline", want: "/authors/about-c%C3%A9line"},
		"unencoded source only":  {rule: "/authors/céline /authors/about-céline", from: "/authors/c%C3%A9line", want: "/authors/about-c%C3%A9line"},
		"nothing encoded":        {rule: "/authors/céline /authors/about-céline", from: "/authors/céline", want: "/authors/about-c%C3%A9line"},
		"an encoded space":       {rule: "/a%20b /c", from: "/a%20b", want: "/c"},
		"an escaped placeholder": {rule: "/%3Aslug /literal", from: "/:slug", want: "/literal"},

		// Ignore trailing slashes by default.
		"a trailing slash cannot be added":   {rule: "/trailing /trailing/ 301", from: "/trailing/", want: "/trailing/"},
		"a trailing slash cannot be removed": {rule: "/notrailing/ /notrailing 301", from: "/notrailing", want: "/notrailing"},

		// Prevent captures from injecting URL components.
		"a capture cannot add a query":     {rule: "/blog/* /posts/:splat", from: "/blog/a%3Fpage=9", want: "/posts/a%3Fpage=9"},
		"a capture cannot add a fragment":  {rule: "/blog/* /posts/:splat", from: "/blog/a%23top", want: "/posts/a%23top"},
		"a capture cannot add a host":      {rule: "/blog/* /:splat", from: "/blog/%2F%2Fexample.com", want: "/example.com"},
		"a capture is escaped in a query":  {rule: "/blog/:slug /new?post=:slug", from: "/blog/a%26admin=1", want: "/new?post=a%26admin%3D1"},
		"a capture cannot walk up":         {rule: "/spa/* /files/:splat 200", from: "/spa/..%2F..%2Fetc", want: "/files/etc"},
		"a capture walking up mid-segment": {rule: "/spa/* /files/x:splat 200", from: "/spa/..%2Fetc", want: "/files/xetc"},
		"the target may walk up itself":    {rule: "/spa/* /files/../public/:splat 200", from: "/spa/a", want: "/public/a"},
		"a query of the request is kept":   {rule: "/blog/* /posts/:splat", from: "/blog/a%3Fpage=9?page=2", want: "/posts/a%3Fpage=9?page=2"},

		// Test exact trailing slash matching.
		"exact: a trailing slash is added":     {rule: "/trailing /trailing/ 301", from: "/trailing", want: "/trailing/", opts: []Option{WithExactTrailingSlash()}},
		"exact: a trailing slash is removed":   {rule: "/notrailing/ /notrailing 301", from: "/notrailing/", want: "/notrailing", opts: []Option{WithExactTrailingSlash()}},
		"exact: the added one does not loop":   {rule: "/trailing /trailing/ 301", from: "/trailing/", want: "-", opts: []Option{WithExactTrailingSlash()}},
		"exact: the removed one does not loop": {rule: "/notrailing/ /notrailing 301", from: "/notrailing", want: "-", opts: []Option{WithExactTrailingSlash()}},
		"exact: a splat keeps the slash":       {rule: "/blog/* /posts/:splat", from: "/blog/2026/hello/", want: "/posts/2026/hello/", opts: []Option{WithExactTrailingSlash()}},
		"exact: a splat of nothing":            {rule: "/blog/* /posts/:splat", from: "/blog/", want: "/posts/", opts: []Option{WithExactTrailingSlash()}},

		// Test proxying rewrite rules.
		"proxy: absolute rewrite": {rule: "/api/* https://example.com/:splat 200", from: "/api/v1/users", want: "https://example.com/v1/users", opts: []Option{WithProxying()}},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ru, err := parseRule(test.rule, newOptions(test.opts))
			if err != nil {
				t.Fatalf("parseRule(%q) error = %v", test.rule, err)
			}
			from, err := url.Parse(test.from)
			if err != nil {
				t.Fatalf("url.Parse(%q) error = %v", test.from, err)
			}

			captures, ok := ru.Match(from.Path)
			if !ok {
				if test.want != "-" {
					t.Fatalf("%q does not match %q, want %q", test.from, test.rule, test.want)
				}
				return
			}
			if test.want == "-" {
				t.Fatalf("%q matches %q, want no match", test.from, test.rule)
			}

			to, err := ru.Destination(captures, from)
			if err != nil {
				t.Fatalf("Destination() error = %v", err)
			}
			if got := to.String(); got != test.want {
				t.Errorf("Destination() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()

	const file = "/blog/* /posts/:splat\n/blog/old /posts/new\n/spa/* /index.html 200\n"

	// orderFile is the example the Netlify docs give for the rule processing order.
	const orderFile = `
# This will redirect /jobs/customer-ninja-rockstar
/jobs/customer-ninja-rockstar  /careers/support-engineer

# This will redirect all paths under /jobs except the path above
/jobs/*                        /careers/:splat

# This will never trigger, because the rule above will trigger first
/jobs/outdated-job-link        /careers/position-filled
`

	// chainFile is the rewrite chain the Cloudflare docs rule out:
	// "/a" renders "/b" and "/b" renders "/c", but "/a" never renders "/c".
	const chainFile = "/a /b 200\n/b /c 200\n"

	tests := map[string]struct {
		file string
		from string
		// want is the destination, "-" if no rule matches.
		want   string
		status int
	}{
		"first match wins": {file: file, from: "/blog/old", want: "/posts/old", status: statusDefault},
		"later rule":       {file: file, from: "/spa/deep/link", want: "/index.html", status: 200},
		"no match":         {file: file, from: "/about", want: "-"},

		// Rules are resolved in the order they are declared.
		"order: the rule before the splat": {file: orderFile, from: "/jobs/customer-ninja-rockstar", want: "/careers/support-engineer", status: statusDefault},
		"order: the splat":                 {file: orderFile, from: "/jobs/anything-else", want: "/careers/anything-else", status: statusDefault},
		"order: the rule after the splat":  {file: orderFile, from: "/jobs/outdated-job-link", want: "/careers/outdated-job-link", status: statusDefault},

		// A rewrite is not resolved again.
		"a rewrite does not chain": {file: chainFile, from: "/a", want: "/b", status: 200},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rules, err := Parse(strings.NewReader(test.file))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			from, err := url.Parse(test.from)
			if err != nil {
				t.Fatalf("url.Parse(%q) error = %v", test.from, err)
			}

			resolved, err := Resolve(rules, from)
			if err != nil {
				t.Fatalf("Resolve(%q) error = %v", test.from, err)
			}
			if test.want == "-" {
				if resolved != nil {
					t.Fatalf("Resolve(%q) = %+v, want nil", test.from, resolved)
				}

				return
			}
			if resolved == nil {
				t.Fatalf("Resolve(%q) = nil, want %q", test.from, test.want)
			}
			if got := resolved.To.String(); got != test.want {
				t.Errorf("Resolve(%q) destination = %q, want %q", test.from, got, test.want)
			}
			if resolved.Rule.Status != test.status {
				t.Errorf("Resolve(%q) status = %d, want %d", test.from, resolved.Rule.Status, test.status)
			}
		})
	}
}

func TestResolvedProxyNil(t *testing.T) {
	t.Parallel()

	// A nil *Resolved must not be proxied.
	var none *Resolved
	if none.Proxy() {
		t.Error("(*Resolved)(nil).Proxy() = true, want false")
	}
}

func TestUnparsedRuleMatchesNothing(t *testing.T) {
	t.Parallel()

	// Unparsed rules must match nothing.
	ru := Rule{Source: "/old", Target: "/new", Status: statusDefault}
	for _, path := range []string{"/old", "/", ""} {
		if _, ok := ru.Match(path); ok {
			t.Errorf("Match(%q) = true, want false for a rule that was not parsed", path)
		}
	}
}
