package redirect

import (
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"testing"
)

// fuzzFile contains seed redirect rules.
const fuzzFile = `
/old-path             /new-path
/blog/:year/:slug     /posts/:slug-:year#top
/docs/*               /articles/:splat?v=1
/spa/*                /index.html            200
/gone                 /                      410
/elsewhere            https://example.com/x
`

func FuzzParse(f *testing.F) {
	seeds := []string{fuzzFile, "/old /new\n", "/old /new 302!\n", "/old\n", "/old /new 601\n", "/blog/* /posts/:splat\n", "/api/* https://example.com/:splat 200\n", "/old /new%zz\n", "# a comment\n", "/\r#\n", "/trailing /trailing/\n"}
	for _, file := range seeds {
		for _, exactSlash := range []bool{false, true} {
			for _, proxy := range []bool{false, true} {
				f.Add(file, exactSlash, proxy)
			}
		}
	}

	f.Fuzz(func(t *testing.T, file string, exactSlash, proxy bool) {
		opts := fuzzOptions(exactSlash, proxy)

		rules, err := Parse(strings.NewReader(file), opts...)
		if err != nil {
			return
		}

		// Round-trip parsed rules through writer.
		again, err := Parse(strings.NewReader(writeRules(rules)), opts...)
		if err != nil {
			t.Fatalf("Parse() error = %v, want the rules of %q to parse again:\n%s", err, file, writeRules(rules))
		}
		if len(again) != len(rules) {
			t.Fatalf("Parse() returned %d rules, want the %d of %q", len(again), len(rules), file)
		}
		for i, ru := range again {
			if ru.Source != rules[i].Source || ru.Target != rules[i].Target ||
				ru.Status != rules[i].Status || ru.Force != rules[i].Force {
				t.Errorf("rule %d = %+v, want %+v", i, ru, rules[i])
			}
		}
	})
}

func FuzzResolve(f *testing.F) {
	seeds := []string{"/old-path", "/blog/2026/hello", "/docs/a/b?page=2", "/spa/..%2F..%2Fetc", "/docs/%2F%2Fexample.com", "/docs/a%3Fpage=9%23top", "/gone", "/elsewhere", "/", "", "/docs/a/b/"}
	for _, from := range seeds {
		for _, exactSlash := range []bool{false, true} {
			f.Add(fuzzFile, from, exactSlash, false)
		}
	}
	// Seed target with network authority.
	f.Add("/ //@\n", "#", false, false)
	f.Add("/old //example.com/new\n", "/old", false, false)
	// Seed URL-encoded source paths.
	f.Add("/authors/c%C3%A9line /authors/about-c%C3%A9line\n", "/authors/c%C3%A9line", false, false)
	// Seed proxied rewrite rule.
	f.Add("/api/* https://example.com/:splat 200\n", "/api/v1/users", false, true)
	// Seed trailing slash rules.
	f.Add("/trailing /trailing/\n/notrailing/ /notrailing\n", "/trailing", true, false)

	f.Fuzz(func(t *testing.T, file, rawURL string, exactSlash, proxy bool) {
		rules, err := Parse(strings.NewReader(file), fuzzOptions(exactSlash, proxy)...)
		if err != nil {
			return
		}
		from, err := url.Parse(rawURL)
		if err != nil {
			return
		}

		resolved, err := Resolve(rules, from)
		if err != nil || resolved == nil {
			return
		}

		// Target must retain original URL structure.
		target, err := url.Parse(resolved.Rule.Target)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v, want the target of a parsed rule to parse", resolved.Rule.Target, err)
		}
		to := resolved.To
		switch {
		case to.Scheme != target.Scheme:
			t.Errorf("Resolve(%q) scheme = %q, want %q of the target %q", rawURL, to.Scheme, target.Scheme, resolved.Rule.Target)
		case to.Opaque != target.Opaque:
			t.Errorf("Resolve(%q) opaque = %q, want %q of the target %q", rawURL, to.Opaque, target.Opaque, resolved.Rule.Target)
		case to.Host != target.Host:
			t.Errorf("Resolve(%q) host = %q, want %q of the target %q", rawURL, to.Host, target.Host, resolved.Rule.Target)
		case userinfo(to.User) != userinfo(target.User):
			t.Errorf("Resolve(%q) userinfo = %q, want %q of the target %q", rawURL, userinfo(to.User), userinfo(target.User), resolved.Rule.Target)
		case target.Fragment == "" && to.Fragment != "":
			t.Errorf("Resolve(%q) fragment = %q, want none for the target %q", rawURL, to.Fragment, resolved.Rule.Target)
		case target.RawQuery == "" && from.RawQuery == "" && to.RawQuery != "":
			t.Errorf("Resolve(%q) query = %q, want none for the target %q", rawURL, to.RawQuery, resolved.Rule.Target)
		}

		// Check proxy rewriting behavior.
		foreign := to.Scheme != "" || to.Opaque != "" || to.Host != "" || to.User != nil
		switch rewrite := resolved.Rule.Status == http.StatusOK; {
		case rewrite && foreign && !proxy:
			t.Errorf("Resolve(%q) = %q, want a rewrite to name a path", rawURL, to)
		case resolved.Proxy() != (rewrite && foreign):
			t.Errorf("Resolve(%q) = %q, Proxy() = %v, want %v", rawURL, to, resolved.Proxy(), rewrite && foreign)
		}

		// Expanded path must be clean.
		if to.Path != target.Path && cleanPath(to.Path) != to.Path {
			t.Errorf("Resolve(%q) path = %q, want it cleaned", rawURL, to.Path)
		}

		// Destination must stay in target directory.
		if dir, ok := targetDir(target.Path); ok && !strings.HasPrefix(to.Path, dir) {
			t.Errorf("Resolve(%q) path = %q, want it under %q of the target %q", rawURL, to.Path, dir, resolved.Rule.Target)
		}

		// Destination URL must round-trip correctly.
		again, err := url.Parse(to.String())
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v, want the destination to parse", to, err)
		}
		if again.String() != to.String() {
			t.Errorf("url.Parse(%q).String() = %q, want the destination unchanged", to, again)
		}
	})
}

// targetDir returns the target path directory.
func targetDir(p string) (string, bool) {
	name := strings.IndexByte(p, ':')
	if name < 0 || cleanPath(p) != p {
		return "", false
	}

	dir := path.Dir(p[:name])
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}

	return dir, true
}

// userinfo returns the URL userinfo string.
func userinfo(u *url.Userinfo) string {
	if u == nil {
		return ""
	}

	return u.String()
}

// fuzzOptions builds parser options for fuzzing.
func fuzzOptions(exactSlash, proxy bool) []Option {
	var opts []Option

	if exactSlash {
		opts = append(opts, WithExactTrailingSlash())
	}
	if proxy {
		opts = append(opts, WithProxying())
	}

	return opts
}

// writeRules formats rules as text.
func writeRules(rules []Rule) string {
	var file strings.Builder

	for _, ru := range rules {
		file.WriteString(ru.Source)
		file.WriteString(" ")
		file.WriteString(ru.Target)
		file.WriteString(" ")
		file.WriteString(strconv.Itoa(ru.Status))
		if ru.Force {
			file.WriteString("!")
		}
		file.WriteString("\n")
	}

	return file.String()
}
