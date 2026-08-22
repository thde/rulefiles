package rulefile

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestScan(t *testing.T) {
	t.Parallel()

	const file = "\n# a comment\n/old /new\n  \t\n\t/indented # a trailing comment\n"

	var got []string
	if err := Scan(strings.NewReader(file), func(line int, text string) {
		got = append(got, strconv.Itoa(line)+":"+text)
	}); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	want := "3:/old /new 5:\t/indented "
	if strings.Join(got, " ") != want {
		t.Errorf("Scan() read %q, want %q", strings.Join(got, " "), want)
	}
}

func TestScanLineTooLong(t *testing.T) {
	t.Parallel()

	file := "/old /new\n/" + strings.Repeat("a", MaxLineLen) + " /new\n"

	var read int
	err := Scan(strings.NewReader(file), func(int, string) { read++ })
	if err == nil {
		t.Fatal("Scan() error = nil, want the long line to be reported")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("Scan() error = %v, want it to report line 2", err)
	}
	if read != 1 {
		t.Errorf("Scan() read %d lines, want the one before the long one", read)
	}
}

func TestScanLongLineWithinTheLimit(t *testing.T) {
	t.Parallel()

	file := "/*\n\tX-Long: " + strings.Repeat("a", 128*1024) + "\n"

	var read int
	if err := Scan(strings.NewReader(file), func(int, string) { read++ }); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if read != 2 {
		t.Errorf("Scan() read %d lines, want 2", read)
	}
}

func TestScanReaderError(t *testing.T) {
	t.Parallel()

	r := io.MultiReader(strings.NewReader("/old /new\n"), errReader{errRead})

	err := Scan(r, func(int, string) {})
	if !errors.Is(err, errRead) {
		t.Fatalf("Scan() error = %v, want it to wrap %v", err, errRead)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("Scan() error = %v, want it to report the line it stopped at", err)
	}
}

// errRead is returned by errReader.
var errRead = errors.New("read failed")

// errReader fails all read calls.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
