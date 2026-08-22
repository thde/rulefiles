package rulefile

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// MaxLineLen is the maximum line length.
const MaxLineLen = 1 << 20

// Scan reads non-empty lines from r. Blank lines and comments are skipped, so the text is never empty.
//
// A line longer than [MaxLineLen], or a failing reader,
// stops the scan and is reported with the line it happened at.
func Scan(r io.Reader, fn func(line int, text string)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, MaxLineLen)

	line := 0
	for scanner.Scan() {
		line++

		text := StripComment(scanner.Text())
		if strings.TrimSpace(text) == "" {
			continue
		}
		fn(line, text)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("line %d: %w", line+1, err)
	}

	return nil
}
