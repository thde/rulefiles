package rulefile

import (
	"slices"
	"strings"
	"testing"
)

// fuzzCapture is a dummy placeholder value.
const fuzzCapture = "="

func FuzzParsePattern(f *testing.F) {
	seeds := []string{"/", "/*", "/docs/backups", "/docs/:slug", "/:year/:slug", "/blog/*", "/blog/*/comments", "/blog/po*", "/:", "/:slug/:slug", "docs", "https://example.com/docs", "/docs?page=2", "/docs#section", "/authors/c%C3%A9line", "/a%2Fb", "/100%", "/%3Aslug", "/docs/"}
	for _, source := range seeds {
		for _, exact := range []bool{false, true} {
			f.Add(source, "/blog/2026/hello", exact)
			f.Add(source, "/blog/2026/hello/", exact)
		}
	}

	f.Fuzz(func(t *testing.T, source, path string, exact bool) {
		pattern, err := ParsePattern(source, PatternOptions{ExactTrailingSlash: exact})
		if err != nil {
			return
		}

		captures, ok := pattern.Match(path)
		if !ok {
			if captures != nil {
				t.Errorf("Match(%q) = %v, want no captures for a path that does not match", path, captures)
			}

			return
		}

		// Match returns all declared placeholder captures.
		if len(captures) != len(pattern.names) {
			t.Errorf("Match(%q) captured %d values, want the %d the pattern declares", path, len(captures), len(pattern.names))
		}
		for name := range captures {
			if _, ok := pattern.names[name]; !ok {
				t.Errorf("Match(%q) captured %q, which %q does not declare", path, name, source)
			}
		}
	})
}

func FuzzExpand(f *testing.F) {
	for _, seed := range []string{"/posts/:slug", "/posts/:year/:slug", "/posts/:slug-:year", "/posts/:month", "https://example.com:8443/x", "a :: b", "trailing colon:", ":splat", "::slug", ":slug:month"} {
		f.Add(seed)
	}

	// Pattern with multiple placeholders.
	pattern, err := ParsePattern("/:year/:slug/*", PatternOptions{})
	if err != nil {
		f.Fatalf("ParsePattern() error = %v", err)
	}
	captures := map[string]string{}
	for name := range pattern.names {
		captures[name] = fuzzCapture
	}

	f.Fuzz(func(t *testing.T, s string) {
		expanded := Expand(s, captures)

		if got := ExpandFunc(s, captures, nil); got != expanded {
			t.Errorf("ExpandFunc(%q, nil) = %q, want the result of Expand, %q", s, got, expanded)
		}
		if got := Expand(s, nil); got != s {
			t.Errorf("Expand(%q, nil) = %q, want it unchanged without captures", s, got)
		}

		// Uncaptured placeholders remain in expanded text.
		unknown := pattern.Uncaptured(s)
		for _, name := range unknown {
			if !strings.Contains(expanded, ":"+name) {
				t.Errorf("Expand(%q) = %q, want the uncaptured %q to be left in place", s, expanded, ":"+name)
			}
		}
		if got := pattern.Uncaptured(expanded); !slices.Equal(got, unknown) {
			t.Errorf("Uncaptured(%q) = %v after expanding %q, want %v", expanded, got, s, unknown)
		}
	})
}

func FuzzStripComment(f *testing.F) {
	for _, seed := range []string{"# a comment", "  # an indented comment", "/old /new # a redirect", "/old /new#section", "\tX-Robots-Tag: noindex#", "#", ""} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, line string) {
		stripped := StripComment(line)

		if !strings.HasPrefix(line, stripped) {
			t.Fatalf("StripComment(%q) = %q, want a prefix of the line", line, stripped)
		}
		if len(stripped) < len(line) && line[len(stripped)] != '#' {
			t.Errorf("StripComment(%q) = %q, want it to stop at a %q", line, stripped, "#")
		}
		if got := StripComment(stripped); got != stripped {
			t.Errorf("StripComment(%q) = %q, want the stripped line unchanged", stripped, got)
		}
	})
}

func FuzzScan(f *testing.F) {
	for _, seed := range []string{"", "\n", "# a comment\n", "/old /new\n\n\t/indented # a trailing comment\n", "/*\r\n\tX-A: 1\r\n", "/old /new", "  \t"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, file string) {
		lines := strings.Count(file, "\n") + 1

		last := 0
		err := Scan(strings.NewReader(file), func(line int, text string) {
			if line <= last {
				t.Errorf("Scan() read line %d after line %d, want the lines in order", line, last)
			}
			if line > lines {
				t.Errorf("Scan() read line %d, want at most %d", line, lines)
			}
			// Blank lines should be skipped.
			if strings.TrimSpace(text) == "" {
				t.Errorf("Scan() reported the blank line %d as %q, want it skipped", line, text)
			}
			last = line
		})
		if err != nil && len(file) <= MaxLineLen {
			t.Errorf("Scan() error = %v, want none for a file within the line limit", err)
		}
	})
}
