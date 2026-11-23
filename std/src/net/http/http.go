// Package http is a wasmgc rewrite: HTTP client via fetch (docs/design/06, 07, 09).
// ListenAndServe and dial-based transports are out of v1.
package http

import (
	"errors"
	"io"
	"syscall/js"
)

// ErrUnsupported is returned for operations that need a real dialer or server.
var ErrUnsupported = errors.ErrUnsupported

// Header is a minimal header table (no map, to keep Get lean).
type Header struct {
	pairs []headerKV
}

type headerKV struct {
	key, val string
}

func (h Header) Get(key string) string {
	for _, p := range h.pairs {
		if p.key == key {
			return p.val
		}
	}
	return ""
}

func (h *Header) Set(key, value string) {
	for i, p := range h.pairs {
		if p.key == key {
			h.pairs[i].val = value
			return
		}
	}
	h.pairs = append(h.pairs, headerKV{key, value})
}

// Request is a minimal HTTP request for Do.
type Request struct {
	Method string
	URL    string
	Header Header
	Body   io.Reader
}

// Body is the response body (concrete so Read needs no iface dispatch).
type Body struct {
	s string
	i int
}

func (b *Body) Read(p []byte) (int, error) {
	if b == nil || b.i >= len(b.s) {
		return 0, io.EOF
	}
	n := copy(p, b.s[b.i:])
	b.i += n
	return n, nil
}

func (b *Body) Close() error { return nil }

// String returns the unread body contents (full body if unread).
func (b *Body) String() string {
	if b == nil {
		return ""
	}
	if b.i >= len(b.s) {
		return ""
	}
	return b.s[b.i:]
}

// Response is a minimal HTTP response from fetch.
type Response struct {
	Status     string
	StatusCode int
	Header     Header
	Body       *Body
}

// Get issues a GET to url via host js.httpGet (fetch + text).
func Get(url string) (*Response, error) {
	status, body, err := js.HTTPGet(url)
	if err != nil {
		return nil, err
	}
	return &Response{
		StatusCode: status,
		Body:       &Body{s: body},
	}, nil
}

// Head issues a HEAD via fetch options (suspending Await path).
func Head(url string) (*Response, error) {
	return doMethod("HEAD", url, "")
}

// Post issues a POST. Request bodies are deferred.
func Post(url, contentType string, body io.Reader) (*Response, error) {
	if body != nil {
		return nil, ErrUnsupported
	}
	return doMethod("POST", url, contentType)
}

// Do sends an HTTP request via fetch.
func Do(req *Request) (*Response, error) {
	if req == nil {
		return nil, errors.New("http: nil Request")
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}
	if req.Body != nil {
		return nil, ErrUnsupported
	}
	if method == "GET" && req.Header.Get("Content-Type") == "" {
		return Get(req.URL)
	}
	return doMethod(method, req.URL, req.Header.Get("Content-Type"))
}

func doMethod(method, url, contentType string) (*Response, error) {
	if url == "" {
		return nil, errors.New("http: empty url")
	}
	fetch := js.Global().Get("fetch")
	if fetch.IsUndefined() {
		return nil, errors.New("http: fetch is not available")
	}
	opts := js.Global().Get("Object").New()
	opts.Set("method", method)
	if contentType != "" {
		headers := js.Global().Get("Object").New()
		headers.Set("Content-Type", contentType)
		opts.SetValue("headers", headers)
	}
	p := fetch.Invoke(js.ValueOf(url), opts)
	respVal, err := js.Await(p)
	if err != nil {
		return nil, err
	}
	statusCode := respVal.Get("status").Int()
	var bodyR *Body
	if method != "HEAD" {
		textVal, err := js.Await(respVal.Call("text"))
		if err != nil {
			return nil, err
		}
		bodyR = &Body{s: textVal.String()}
	} else {
		bodyR = &Body{}
	}
	return &Response{
		StatusCode: statusCode,
		Body:       bodyR,
	}, nil
}

// ListenAndServe is not supported on wasmgc.
func ListenAndServe(addr string, handler Handler) error {
	return ErrUnsupported
}

// Handler is a stub so signatures type-check; servers are out of v1.
type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}

// ResponseWriter is a stub for the Handler interface.
type ResponseWriter interface {
	Header() Header
	Write([]byte) (int, error)
	WriteHeader(statusCode int)
}

