package header

import (
	"io"
	"net/http"
)

// Handler returns a handler that applies the rules matching the path of a
// request to the response next writes.
//
// The fields are applied when next writes its status, so a rule wins over a
// field next sets itself and a "!" removal drops a field next set. A request no
// rule matches reaches next with the response writer it was given.
func Handler(rules []Rule, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := Resolve(rules, r.URL.Path)
		if resolved == nil {
			next.ServeHTTP(w, r)

			return
		}

		rw := &responseWriter{ResponseWriter: w, resolved: resolved}
		next.ServeHTTP(rw, r)
		// A handler that wrote nothing leaves the implicit 200 to net/http,
		// which sends the fields set once ServeHTTP returned.
		rw.apply()
	})
}

// NewHandler reads the rules of a _headers file from r and returns
// [Handler](rules, next).
func NewHandler(r io.Reader, next http.Handler, opts ...Option) (http.Handler, error) {
	rules, err := Parse(r, opts...)
	if err != nil {
		return nil, err
	}

	return Handler(rules, next), nil
}

// responseWriter applies the resolved fields to the header of a response before
// its status is written. It implements the methods that have to run before the
// status is sent, and passes anything else to [http.ResponseController] through
// [responseWriter.Unwrap].
type responseWriter struct {
	http.ResponseWriter

	// resolved holds the fields the rules declare for the request.
	resolved *Resolved
	// applied reports whether the fields were written to the header.
	applied bool
}

// WriteHeader applies the resolved fields and sends status.
func (w *responseWriter) WriteHeader(status int) {
	w.apply()
	w.ResponseWriter.WriteHeader(status)
}

// Write applies the resolved fields and writes b.
func (w *responseWriter) Write(b []byte) (int, error) {
	w.apply()

	//nolint:wrapcheck // The error of the wrapped writer is passed through as it is.
	return w.ResponseWriter.Write(b)
}

// ReadFrom applies the resolved fields and copies src into the response. It
// keeps the [io.ReaderFrom] of the wrapped writer reachable, which net/http
// uses to serve a file without copying it through user space.
func (w *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	w.apply()

	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		//nolint:wrapcheck // The error of the wrapped writer is passed through as it is.
		return rf.ReadFrom(src)
	}

	//nolint:wrapcheck // The error of the wrapped writer is passed through as it is.
	return io.Copy(w.ResponseWriter, src)
}

// Flush applies the resolved fields and flushes what is buffered. Flushing a
// writer that cannot do so is a no-op, as it is for [http.ResponseController].
func (w *responseWriter) Flush() {
	w.apply()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap returns the wrapped writer, so that [http.ResponseController] reaches
// the methods this writer does not implement itself.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// apply writes the resolved fields to the header of the response, once.
func (w *responseWriter) apply() {
	if w.applied {
		return
	}
	w.applied = true

	w.resolved.ApplyTo(w.Header())
}
