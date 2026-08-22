package header

import (
	"net/http"
	"strings"
	"testing"

	"golang.org/x/net/http/httpguts"
)

func FuzzParse(f *testing.F) {
	seeds := []string{testFile, docFile, "/*\n\tX-A: 1\n", "\tX-A: 1\n", "/*\n", "/*\n\t! X-A\n", "/blog/:slug\n\tX-Slug: :slug\n", "/*\n\tX-A:#1\n", "/*\n\tContent-Length: 0\n", "relative\n\tX-A: 1\n", "/docs/\n\tX-A: 1\n"}
	for _, file := range seeds {
		for _, exactSlash := range []bool{false, true} {
			f.Add(file, exactSlash)
		}
	}

	f.Fuzz(func(t *testing.T, file string, exactSlash bool) {
		opts := fuzzOptions(exactSlash)

		rules, err := Parse(strings.NewReader(file), opts...)
		if err != nil {
			return
		}

		again, err := Parse(strings.NewReader(writeRules(rules)), opts...)
		if err != nil {
			t.Fatalf("Parse() error = %v, want the rules of %q to parse again:\n%s", err, file, writeRules(rules))
		}
		if len(again) != len(rules) {
			t.Fatalf("Parse() returned %d rules, want the %d of %q", len(again), len(rules), file)
		}
		for i, ru := range again {
			if ru.Source != rules[i].Source {
				t.Errorf("rule %d source = %q, want %q", i, ru.Source, rules[i].Source)
			}
			if len(ru.Operations) != len(rules[i].Operations) {
				t.Fatalf("rule %d has %d ops, want %d", i, len(ru.Operations), len(rules[i].Operations))
			}
			for j, op := range ru.Operations {
				if op != rules[i].Operations[j] {
					t.Errorf("rule %d op %d = %+v, want %+v", i, j, op, rules[i].Operations[j])
				}
			}
		}
	})
}

func FuzzResolve(f *testing.F) {
	seeds := []string{"/index.html", "/assets/main.css", "/docs/backups", "/docs/a\nX-Evil: yes", "/docs/%0aX-Evil:%20yes", "/", "", "/docs/backups/"}
	for _, path := range seeds {
		for _, exactSlash := range []bool{false, true} {
			f.Add(testFile, path, exactSlash)
		}
	}
	for _, path := range []string{"/movies/serenity", "/downloads/2026/report.pdf", "/images/photo.jpg"} {
		f.Add(docFile, path, false)
	}

	f.Fuzz(func(t *testing.T, file, path string, exactSlash bool) {
		rules, err := Parse(strings.NewReader(file), fuzzOptions(exactSlash)...)
		if err != nil {
			return
		}

		// Existing header to modify.
		hdr := http.Header{"X-Robots-Tag": []string{"index"}}
		resolved := Resolve(rules, path)
		resolved.ApplyTo(hdr)

		for name, values := range hdr {
			if !httpguts.ValidHeaderFieldName(name) {
				t.Errorf("field name %q is invalid", name)
			}
			for _, value := range values {
				if !httpguts.ValidHeaderFieldValue(value) {
					t.Errorf("value %q of %s is invalid", value, name)
				}
			}
		}

		if resolved == nil {
			return
		}
		for name := range resolved.removed {
			if _, ok := hdr[name]; ok {
				t.Errorf("%s = %q, want the removed field to be absent", name, hdr.Get(name))
			}
		}
		for name := range resolved.set {
			if _, ok := hdr[name]; !ok {
				t.Errorf("%s is absent, want the field the rules set", name)
			}
		}
	})
}

// fuzzOptions converts flags to Parse options.
func fuzzOptions(exactSlash bool) []Option {
	if !exactSlash {
		return nil
	}

	return []Option{WithExactTrailingSlash()}
}

// writeRules formats rules as text.
func writeRules(rules []Rule) string {
	var file strings.Builder

	for _, ru := range rules {
		file.WriteString(ru.Source)
		file.WriteString("\n")
		for _, op := range ru.Operations {
			if op.Remove {
				file.WriteString("\t!")
				file.WriteString(op.Name)
				file.WriteString("\n")

				continue
			}
			file.WriteString("\t")
			file.WriteString(op.Name)
			file.WriteString(":")
			file.WriteString(op.Value)
			file.WriteString("\n")
		}
	}

	return file.String()
}
