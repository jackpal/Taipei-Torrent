// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// HTTP server.  See RFC 2616.

// TODO(rsc):
//	logging
//	cgi support
//	post support

package http

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
)

// Errors introduced by the HTTP server.
var (
	ErrWriteAfterFlush = os.NewError("Conn.Write called after Flush")
	ErrHijacked        = os.NewError("Conn has been hijacked")
)

// Objects implementing the Handler interface can be
// registered to serve a particular path or subtree
// in the HTTP server.
type Handler interface {
	ServeHTTP(*Conn, *Request)
}

// A Conn represents the server side of a single active HTTP connection.
type Conn struct {
	RemoteAddr string   // network address of remote side
	Req        *Request // current HTTP request

	rwc      io.ReadWriteCloser // i/o connection
	buf      *bufio.ReadWriter  // buffered rwc
	handler  Handler            // request handler
	hijacked bool               // connection has been hijacked by handler

	// state for the current reply
	closeAfterReply bool              // close connection after this reply
	chunking        bool              // using chunked transfer encoding for reply body
	wroteHeader     bool              // reply header has been written
	header          map[string]string // reply header parameters
	written         int64             // number of bytes written in body
	status          int               // status code passed to WriteHeader
}

// Create new connection from rwc.
func newConn(rwc net.Conn, handler Handler) (c *Conn, err os.Error) {
	c = new(Conn)
	if a := rwc.RemoteAddr(); a != nil {
		c.RemoteAddr = a.String()
	}
	c.handler = handler
	c.rwc = rwc
	br := bufio.NewReader(rwc)
	bw := bufio.NewWriter(rwc)
	c.buf = bufio.NewReadWriter(br, bw)
	return c, nil
}

// Read next request from connection.
func (c *Conn) readRequest() (req *Request, err os.Error) {
	if c.hijacked {
		return nil, ErrHijacked
	}
	if req, err = ReadRequest(c.buf.Reader); err != nil {
		return nil, err
	}

	// Reset per-request connection state.
	c.header = make(map[string]string)
	c.wroteHeader = false
	c.Req = req

	// Default output is HTML encoded in UTF-8.
	c.SetHeader("Content-Type", "text/html; charset=utf-8")

	if req.ProtoAtLeast(1, 1) {
		// HTTP/1.1 or greater: use chunked transfer encoding
		// to avoid closing the connection at EOF.
		c.chunking = true
		c.SetHeader("Transfer-Encoding", "chunked")
	} else {
		// HTTP version < 1.1: cannot do chunked transfer
		// encoding, so signal EOF by closing connection.
		// Could avoid closing the connection if there is
		// a Content-Length: header in the response,
		// but everyone who expects persistent connections
		// does HTTP/1.1 now.
		c.closeAfterReply = true
		c.chunking = false
	}

	return req, nil
}

// SetHeader sets a header line in the eventual reply.
// For example, SetHeader("Content-Type", "text/html; charset=utf-8")
// will result in the header line
//
//	Content-Type: text/html; charset=utf-8
//
// being sent.  UTF-8 encoded HTML is the default setting for
// Content-Type in this library, so users need not make that
// particular call.  Calls to SetHeader after WriteHeader (or Write)
// are ignored.
func (c *Conn) SetHeader(hdr, val string) { c.header[CanonicalHeaderKey(hdr)] = val }

// WriteHeader sends an HTTP response header with status code.
// If WriteHeader is not called explicitly, the first call to Write
// will trigger an implicit WriteHeader(http.StatusOK).
// Thus explicit calls to WriteHeader are mainly used to
// send error codes.
func (c *Conn) WriteHeader(code int) {
	if c.hijacked {
		log.Println("http: Conn.WriteHeader on hijacked connection")
		return
	}
	if c.wroteHeader {
		log.Println("http: multiple Conn.WriteHeader calls")
		return
	}
	c.wroteHeader = true
	c.status = code
	c.written = 0
	if !c.Req.ProtoAtLeast(1, 0) {
		return
	}
	proto := "HTTP/1.0"
	if c.Req.ProtoAtLeast(1, 1) {
		proto = "HTTP/1.1"
	}
	codestring := strconv.Itoa(code)
	text, ok := statusText[code]
	if !ok {
		text = "status code " + codestring
	}
	io.WriteString(c.buf, proto+" "+codestring+" "+text+"\r\n")
	for k, v := range c.header {
		io.WriteString(c.buf, k+": "+v+"\r\n")
	}
	io.WriteString(c.buf, "\r\n")
}

// Write writes the data to the connection as part of an HTTP reply.
// If WriteHeader has not yet been called, Write calls WriteHeader(http.StatusOK)
// before writing the data.
func (c *Conn) Write(data []byte) (n int, err os.Error) {
	if c.hijacked {
		log.Println("http: Conn.Write on hijacked connection")
		return 0, ErrHijacked
	}
	if !c.wroteHeader {
		c.WriteHeader(StatusOK)
	}
	if len(data) == 0 {
		return 0, nil
	}

	c.written += int64(len(data)) // ignoring errors, for errorKludge

	// TODO(rsc): if chunking happened after the buffering,
	// then there would be fewer chunk headers.
	// On the other hand, it would make hijacking more difficult.
	if c.chunking {
		fmt.Fprintf(c.buf, "%x\r\n", len(data)) // TODO(rsc): use strconv not fmt
	}
	n, err = c.buf.Write(data)
	if err == nil && c.chunking {
		if n != len(data) {
			err = io.ErrShortWrite
		}
		if err == nil {
			io.WriteString(c.buf, "\r\n")
		}
	}

	return n, err
}

// If this is an error reply (4xx or 5xx)
// and the handler wrote some data explaining the error,
// some browsers (i.e., Chrome, Internet Explorer)
// will show their own error instead unless the error is
// long enough.  The minimum lengths used in those
// browsers are in the 256-512 range.
// Pad to 1024 bytes.
func errorKludge(c *Conn, req *Request) {
	const min = 1024

	// Is this an error?
	if kind := c.status / 100; kind != 4 && kind != 5 {
		return
	}

	// Did the handler supply any info?  Enough?
	if c.written == 0 || c.written >= min {
		return
	}

	// Is it a broken browser?
	var msg string
	switch agent := req.UserAgent; {
	case strings.Index(agent, "MSIE") >= 0:
		msg = "Internet Explorer"
	case strings.Index(agent, "Chrome/") >= 0:
		msg = "Chrome"
	default:
		return
	}
	msg += " would ignore this error page if this text weren't here.\n"

	// Is it text?  ("Content-Type" is always in the map)
	baseType := strings.Split(c.header["Content-Type"], ";", 2)[0]
	switch baseType {
	case "text/html":
		io.WriteString(c, "<!-- ")
		for c.written < min {
			io.WriteString(c, msg)
		}
		io.WriteString(c, " -->")
	case "text/plain":
		io.WriteString(c, "\n")
		for c.written < min {
			io.WriteString(c, msg)
		}
	}
}

func (c *Conn) finishRequest() {
	if !c.wroteHeader {
		c.WriteHeader(StatusOK)
	}
	errorKludge(c, c.Req)
	if c.chunking {
		io.WriteString(c.buf, "0\r\n")
		// trailer key/value pairs, followed by blank line
		io.WriteString(c.buf, "\r\n")
	}
	c.buf.Flush()
}

// Flush sends any buffered data to the client.
func (c *Conn) Flush() {
	if !c.wroteHeader {
		c.WriteHeader(StatusOK)
	}
	c.buf.Flush()
}

// Close the connection.
func (c *Conn) close() {
	if c.buf != nil {
		c.buf.Flush()
		c.buf = nil
	}
	if c.rwc != nil {
		c.rwc.Close()
		c.rwc = nil
	}
}

// Serve a new connection.
func (c *Conn) serve() {
	for {
		req, err := c.readRequest()
		if err != nil {
			break
		}
		// HTTP cannot have multiple simultaneous active requests.
		// Until the server replies to this request, it can't read another,
		// so we might as well run the handler in this goroutine.
		c.handler.ServeHTTP(c, req)
		if c.hijacked {
			return
		}
		c.finishRequest()
		if c.closeAfterReply {
			break
		}
	}
	c.close()
}

// Hijack lets the caller take over the connection.
// After a call to c.Hijack(), the HTTP server library
// will not do anything else with the connection.
// It becomes the caller's responsibility to manage
// and close the connection.
func (c *Conn) Hijack() (rwc io.ReadWriteCloser, buf *bufio.ReadWriter, err os.Error) {
	if c.hijacked {
		return nil, nil, ErrHijacked
	}
	c.hijacked = true
	rwc = c.rwc
	buf = c.buf
	c.rwc = nil
	c.buf = nil
	return
}

// The HandlerFunc type is an adapter to allow the use of
// ordinary functions as HTTP handlers.  If f is a function
// with the appropriate signature, HandlerFunc(f) is a
// Handler object that calls f.
type HandlerFunc func(*Conn, *Request)

// ServeHTTP calls f(c, req).
func (f HandlerFunc) ServeHTTP(c *Conn, req *Request) {
	f(c, req)
}

// Helper handlers

// NotFound replies to the request with an HTTP 404 not found error.
func NotFound(c *Conn, req *Request) {
	c.SetHeader("Content-Type", "text/plain; charset=utf-8")
	c.WriteHeader(StatusNotFound)
	io.WriteString(c, "404 page not found\n")
}

// NotFoundHandler returns a simple request handler
// that replies to each request with a ``404 page not found'' reply.
func NotFoundHandler() Handler { return HandlerFunc(NotFound) }

// Redirect replies to the request with a redirect to url,
// which may be a path relative to the request path.
func Redirect(c *Conn, url string, code int) {
	// RFC2616 recommends that a short note "SHOULD" be included in the
	// response because older user agents may not understand 301/307.
	note := "<a href=\"%v\">" + statusText[code] + "</a>.\n"
	if c.Req.Method == "POST" {
		note = ""
	}

	u, err := ParseURL(url)
	if err != nil {
		goto finish
	}

	// If url was relative, make absolute by
	// combining with request path.
	// The browser would probably do this for us,
	// but doing it ourselves is more reliable.

	// NOTE(rsc): RFC 2616 says that the Location
	// line must be an absolute URI, like
	// "http://www.google.com/redirect/",
	// not a path like "/redirect/".
	// Unfortunately, we don't know what to
	// put in the host name section to get the
	// client to connect to us again, so we can't
	// know the right absolute URI to send back.
	// Because of this problem, no one pays attention
	// to the RFC; they all send back just a new path.
	// So do we.
	oldpath := c.Req.URL.Path
	if oldpath == "" { // should not happen, but avoid a crash if it does
		oldpath = "/"
	}
	if u.Scheme == "" {
		// no leading http://server
		if url == "" || url[0] != '/' {
			// make relative path absolute
			olddir, _ := path.Split(oldpath)
			url = olddir + url
		}

		// clean up but preserve trailing slash
		trailing := url[len(url)-1] == '/'
		url = path.Clean(url)
		if trailing && url[len(url)-1] != '/' {
			url += "/"
		}
	}

finish:
	c.SetHeader("Location", url)
	c.WriteHeader(code)
	fmt.Fprintf(c, note, url)
}

// Redirect to a fixed URL
type redirectHandler struct {
	url  string
	code int
}

func (rh *redirectHandler) ServeHTTP(c *Conn, req *Request) {
	Redirect(c, rh.url, rh.code)
}

// RedirectHandler returns a request handler that redirects
// each request it receives to the given url using the given
// status code.
func RedirectHandler(url string, code int) Handler {
	return &redirectHandler{url, code}
}

// ServeMux is an HTTP request multiplexer.
// It matches the URL of each incoming request against a list of registered
// patterns and calls the handler for the pattern that
// most closely matches the URL.
//
// Patterns named fixed paths, like "/favicon.ico",
// or subtrees, like "/images/" (note the trailing slash).
// Patterns must begin with /.
// Longer patterns take precedence over shorter ones, so that
// if there are handlers registered for both "/images/"
// and "/images/thumbnails/", the latter handler will be
// called for paths beginning "/images/thumbnails/" and the
// former will receiver requests for any other paths in the
// "/images/" subtree.
//
// In the future, the pattern syntax may be relaxed to allow
// an optional host-name at the beginning of the pattern,
// so that a handler might register for the two patterns
// "/codesearch" and "codesearch.google.com/"
// without taking over requests for http://www.google.com/.
//
// ServeMux also takes care of sanitizing the URL request path,
// redirecting any request containing . or .. elements to an
// equivalent .- and ..-free URL.
type ServeMux struct {
	m map[string]Handler
}

// NewServeMux allocates and returns a new ServeMux.
func NewServeMux() *ServeMux { return &ServeMux{make(map[string]Handler)} }

// DefaultServeMux is the default ServeMux used by Serve.
var DefaultServeMux = NewServeMux()

// Does path match pattern?
func pathMatch(pattern, path string) bool {
	if len(pattern) == 0 {
		// should not happen
		return false
	}
	n := len(pattern)
	if pattern[n-1] != '/' {
		return pattern == path
	}
	return len(path) >= n && path[0:n] == pattern
}

// Return the canonical path for p, eliminating . and .. elements.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	// path.Clean removes trailing slash except for root;
	// put the trailing slash back if necessary.
	if p[len(p)-1] == '/' && np != "/" {
		np += "/"
	}
	return np
}

// ServeHTTP dispatches the request to the handler whose
// pattern most closely matches the request URL.
func (mux *ServeMux) ServeHTTP(c *Conn, req *Request) {
	// Clean path to canonical form and redirect.
	if p := cleanPath(req.URL.Path); p != req.URL.Path {
		c.SetHeader("Location", p)
		c.WriteHeader(StatusMovedPermanently)
		return
	}

	// Most-specific (longest) pattern wins.
	var h Handler
	var n = 0
	for k, v := range mux.m {
		if !pathMatch(k, req.URL.Path) {
			continue
		}
		if h == nil || len(k) > n {
			n = len(k)
			h = v
		}
	}
	if h == nil {
		h = NotFoundHandler()
	}
	h.ServeHTTP(c, req)
}

// Handle registers the handler for the given pattern.
func (mux *ServeMux) Handle(pattern string, handler Handler) {
	if pattern == "" || pattern[0] != '/' {
		panic("http: invalid pattern" + pattern)
	}

	mux.m[pattern] = handler

	// Helpful behavior:
	// If pattern is /tree/, insert permanent redirect for /tree.
	n := len(pattern)
	if n > 0 && pattern[n-1] == '/' {
		mux.m[pattern[0:n-1]] = RedirectHandler(pattern, StatusMovedPermanently)
	}
}

// Handle registers the handler for the given pattern
// in the DefaultServeMux.
func Handle(pattern string, handler Handler) { DefaultServeMux.Handle(pattern, handler) }

// Serve accepts incoming HTTP connections on the listener l,
// creating a new service thread for each.  The service threads
// read requests and then call handler to reply to them.
// Handler is typically nil, in which case the DefaultServeMux is used.
func Serve(l net.Listener, handler Handler) os.Error {
	if handler == nil {
		handler = DefaultServeMux
	}
	for {
		rw, e := l.Accept()
		if e != nil {
			return e
		}
		c, err := newConn(rw, handler)
		if err != nil {
			continue
		}
		go c.serve()
	}
	panic("not reached")
}

// ListenAndServe listens on the TCP network address addr
// and then calls Serve with handler to handle requests
// on incoming connections.  Handler is typically nil,
// in which case the DefaultServeMux is used.
//
// A trivial example server is:
//
//	package main
//
//	import (
//		"http";
//		"io";
//	)
//
//	// hello world, the web server
//	func HelloServer(c *http.Conn, req *http.Request) {
//		io.WriteString(c, "hello, world!\n");
//	}
//
//	func main() {
//		http.Handle("/hello", http.HandlerFunc(HelloServer));
//		err := http.ListenAndServe(":12345", nil);
//		if err != nil {
//			panic("ListenAndServe: ", err.String())
//		}
//	}
func ListenAndServe(addr string, handler Handler) os.Error {
	l, e := net.Listen("tcp", addr)
	if e != nil {
		return e
	}
	e = Serve(l, handler)
	l.Close()
	return e
}
