package redirect

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
)

// ErrProxy reports a destination that is fetched from another host, resolved by
// a handler that was not given a proxy with [WithProxy].
var ErrProxy = errors.New("no proxy configured")

// HandlerOption configures a [Handler]. It settles what the rules of a file
// leave to the caller: whether a rule may shadow a file next serves, and how a
// destination of another host is fetched.
type HandlerOption func(*handlerOptions)

// handlerOptions holds handler settings.
type handlerOptions struct {
	// exists reports whether next serves the path of a request itself.
	exists func(r *http.Request) bool
	// proxy fetches a destination from another host.
	proxy http.Handler
	// errorHandler answers a request that could not be resolved.
	errorHandler func(w http.ResponseWriter, r *http.Request, err error)
}

// WithExists honours [Rule.Force]: a rule written without a trailing "!" is
// skipped while exists reports that next serves the path of the request itself,
// so that a redirect does not shadow a file of the same name. A later rule
// still matches such a request.
//
// Without it every rule applies, forced or not, as the handler cannot know what
// next serves.
func WithExists(exists func(r *http.Request) bool) HandlerOption {
	return func(o *handlerOptions) { o.exists = exists }
}

// WithProxy serves a destination [Resolved.Proxy] reports with proxy, in a copy
// of the request whose URL is the absolute destination of the rule. Only rules
// parsed with [WithProxying] resolve to such a destination; without a proxy it
// is answered as an error wrapping [ErrProxy].
//
// A [net/http/httputil.ReverseProxy] fetches the URL of the request it is
// given:
//
//	redirect.WithProxy(&httputil.ReverseProxy{
//		Rewrite: func(pr *httputil.ProxyRequest) { pr.Out.URL = pr.In.URL },
//	})
func WithProxy(proxy http.Handler) HandlerOption {
	return func(o *handlerOptions) { o.proxy = proxy }
}

// WithErrorHandler answers a request whose destination could not be built, or
// that resolves to a proxy without [WithProxy]. It defaults to a 500.
func WithErrorHandler(fn func(w http.ResponseWriter, r *http.Request, err error)) HandlerOption {
	return func(o *handlerOptions) { o.errorHandler = fn }
}

// newHandlerOptions returns default handler options with opts applied.
func newHandlerOptions(opts []HandlerOption) handlerOptions {
	var o handlerOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.errorHandler == nil {
		o.errorHandler = serverError
	}

	return o
}

// serverError answers a request that could not be resolved with a 500.
func serverError(w http.ResponseWriter, _ *http.Request, _ error) {
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// Handler returns a handler that resolves the rules against the URL of a
// request and acts on the first rule that matches: a status of 200 rewrites the
// request and passes it to next, a status of 400 and above answers it with that
// status, anything else redirects to the destination. A request no rule matches
// is passed to next as it was received.
//
// [Rule.Force] is only honoured when [WithExists] is given; without it every
// rule applies, whether or not next serves a file of the same name. A
// destination [Resolved.Proxy] reports needs [WithProxy] to be fetched.
//
// The rules are resolved once. A rewrite is not matched against the rules
// again, so a rule may rewrite to a path another rule redirect.
func Handler(rules []Rule, next http.Handler, opts ...HandlerOption) http.Handler {
	o := newHandlerOptions(opts)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved, err := resolve(rules, r.URL, o.skip(r))
		if err != nil {
			o.errorHandler(w, r, err)

			return
		}
		if resolved == nil {
			next.ServeHTTP(w, r)

			return
		}

		switch status := resolved.Rule.Status; {
		case resolved.Proxy():
			if o.proxy == nil {
				o.errorHandler(w, r, fmt.Errorf("source %q: %w", resolved.Rule.Source, ErrProxy))

				return
			}

			o.proxy.ServeHTTP(w, rewrite(r, resolved.To))
		case status == http.StatusOK:
			next.ServeHTTP(w, rewrite(r, resolved.To))
		case status >= 400:
			http.Error(w, http.StatusText(status), status)
		default:
			//nolint:gosec // The destination is declared by the rules of the site, not by the request.
			http.Redirect(w, r, resolved.To.String(), status)
		}
	})
}

// NewHandler reads the rules of a _redirects file from r and returns
// [Handler](rules, next). Use [Parse] and [Handler] to combine the options of
// the parser with those of the handler.
func NewHandler(r io.Reader, next http.Handler, opts ...Option) (http.Handler, error) {
	rules, err := Parse(r, opts...)
	if err != nil {
		return nil, err
	}

	return Handler(rules, next), nil
}

// skip returns the rule filter of a request, or nil if every rule applies. The
// request is only looked up once, and only if a rule that is not forced matches
// it.
func (o handlerOptions) skip(r *http.Request) func(ru Rule) bool {
	if o.exists == nil {
		return nil
	}

	served := sync.OnceValue(func() bool { return o.exists(r) })

	return func(ru Rule) bool { return !ru.Force && served() }
}

// rewrite returns a copy of r that is served as to, the way [http.StripPrefix]
// hands a rewritten request on. Its RequestURI still holds the URL as it was
// requested.
func rewrite(r *http.Request, to *url.URL) *http.Request {
	rewritten := *r
	rewritten.URL = to

	return &rewritten
}
