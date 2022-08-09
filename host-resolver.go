package main

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
)

type TargetHostInfo struct {
	Address string
	Port    int
}

type HostTargetResolver struct {
}

// func (r *HostTargetResolver) parseURLPath(path string) (pathRewrite string, info *TargetHostInfo, err error) {
func (r *HostTargetResolver) parseURLPath(path string) (pathRewrite string, info string, err error) {

	// /host:<host>:<port>/...

	pathRewrite = path // defaults to same path, alternative default: return empty string if no rewrite happens

	if !strings.HasPrefix(path, "/host:") {
		// err = fmt.Errorf("parse-url-path: invalid dynamic run path") // TODO: how to call this
		return
	}

	pathParts := strings.SplitN(path, "/", 3)

	// assert pathParts[0] == "" // not absolute path

	ps := strings.SplitN(pathParts[1], ":", 2)

	// /host:<host>:<port>/...
	if len(ps) < 2 {
		err = fmt.Errorf("parse-url-path: invalid path format")
		return
	}

	// port, err := strconv.Atoi(ps[2])
	// if err != nil {
	// 	err = fmt.Errorf("parse-url-path: invalid path: invalid port %s: %v", ps[2], err)
	// 	return
	// }
	//
	// info = &TargetHostInfo{Address: ps[1], Port: port}

	info = ps[1]

	pathRewrite = "/"

	if len(pathParts) > 2 {
		pathRewrite += pathParts[2]
	}

	return
}

func (r *HostTargetResolver) Resolve(buff []byte) (target ResolvedTarget, err error) {

	request, err := ParseHTTPRequest(buff)
	if request == nil {
		return
	}

	target, err = r.ResolveHTTPRequest(request)

	return
}

func (r *HostTargetResolver) ResolveHTTPRequest(request *ParsedHTTPRequest) (target ResolvedTarget, err error) {

	log.Debug("host-resolver: got HTTP request:", request.Method, request.Path, request.Version)
	// log.Debug("host-resolver: got HTTP Headers", request.Headers)

	// var info *TargetHostInfo
	var info string

	// request.Path, info, err = r.parseURLPath(request.Path)
	_, info, err = r.parseURLPath(request.Path)
	if err != nil {
		err = fmt.Errorf("host-resolver: invalid path: %w", err)
		return
	}
	log.Trace("host-resolver: request path is now:", request.Path)

	// if info == nil {
	if len(info) == 0 {
		log.Debug("host-resolver: not a host request request:", request.Method, request.Path)
		return
	}

	// if len(info.address) == 0 {
	// 	err = fmt.Errorf("host-resolver: target not found")
	// 	return
	// }

	log.Trace("host-resolver: remote address:", info)

	if len(strings.SplitN(info, ":", 2)) < 2 {
		// TODO: how to know if it's https?
		info += ":80"
	}

	proxyHost := request.Headers.Get("Host")
	targetHost := strings.SplitN(info, ":", 2)[0]

	// request.Headers.Set("Host", targetHost) // set host to target

	// log.Trace("host-resolver: request:", string(request.Data(true, false, true)))

	// target = &BasicResolvedTarget{Address: info.Address, Data: request.Data(true, false, false), ActivityCallback: nil, ClosedCallback: nil}
	target = &ResolvedHTTPTarget{BasicResolvedTarget{Address: info, Data: request.Data(true, false, true), ActivityCallback: nil, ClosedCallback: nil}, proxyHost, targetHost}

	return
}

type ResolvedHTTPTarget struct {
	BasicResolvedTarget
	proxyHost  string
	targetHost string
	// prefix string
}

func (t *ResolvedHTTPTarget) parseURLPath(path string) (pathRewrite string, info string, err error) {

	// /host:<host>:<port>/...

	pathRewrite = path // defaults to same path, alternative default: return empty string if no rewrite happens

	if !strings.HasPrefix(path, "/host:") {
		// err = fmt.Errorf("parse-url-path: invalid dynamic run path") // TODO: how to call this
		return
	}

	pathParts := strings.SplitN(path, "/", 3)

	// assert pathParts[0] == "" // not absolute path

	ps := strings.SplitN(pathParts[1], ":", 2)

	// /host:<host>:<port>/...
	if len(ps) < 2 {
		err = fmt.Errorf("parse-url-path: invalid path format")
		return
	}

	// port, err := strconv.Atoi(ps[2])
	// if err != nil {
	// 	err = fmt.Errorf("parse-url-path: invalid path: invalid port %s: %v", ps[2], err)
	// 	return
	// }
	//
	// info = &TargetHostInfo{Address: ps[1], Port: port}

	info = ps[1]

	pathRewrite = "/"

	if len(pathParts) > 2 {
		pathRewrite += pathParts[2]
	}

	return
}

func (t *ResolvedHTTPTarget) WrapProxyConnection(conn io.ReadWriter) io.ReadWriter {

	log.Debug("docker-resolver-target: wrap proxy connection")

	conn = NewHTTPRewriteRequestWrapper(conn, func(request *ParsedHTTPRequest) (err error) {

		// request.Path, err = t.parseURLPath(request.Path)
		// log.Trace("docker-resolver: wrapped proxy conn request:", string(request.Data(true, false, true)))
		// ----------------

		log.Debug("host-resolver: got HTTP request:", request.Method, request.Path, request.Version)
		// log.Debug("host-resolver: got HTTP Headers", request.Headers)

		// var info *TargetHostInfo
		var info string

		request.Path, info, err = t.parseURLPath(request.Path)
		if err != nil {
			err = fmt.Errorf("host-resolver: invalid path: %w", err)
			return
		}
		log.Trace("host-resolver: request path is now:", request.Path)

		// if info == nil {
		if len(info) == 0 {
			log.Debug("host-resolver: not a host request request:", request.Method, request.Path)
			// TODO: what to do if this fails? drop connection at this point?
			err = fmt.Errorf("host-resolver: not a host request request: %s %s", request.Method, request.Path)
			return
		}

		// if len(info.address) == 0 {
		// 	err = fmt.Errorf("host-resolver: target not found")
		// 	return
		// }

		log.Trace("host-resolver: remote address:", info)

		if len(strings.SplitN(info, ":", 2)) < 2 {
			// TODO: how to know if it's https?
			info += ":80"
		}

		t.proxyHost = request.Headers.Get("Host")
		t.targetHost = strings.SplitN(info, ":", 2)[0]

		request.Headers.Set("Host", t.targetHost) // set host to target

		// log.Trace("host-resolver: request:", string(request.Data(true, false, true)))

		return
	})

	return conn
}

func (t *ResolvedHTTPTarget) Connect() (conn io.ReadWriteCloser, err error) {
	// connect and wrap...
	remoteAddress := t.Address
	log.Trace("resolved-http-target: connect: connecting to target:", remoteAddress)
	conn, err = net.Dial("tcp", remoteAddress)
	if err != nil {
		err = fmt.Errorf("resolved-http-target: connect: remote connection to %s failed: %w", remoteAddress, err)
		return
	}

	// conn = NewHTTPResponseLocationRewrite(conn, t.proxyHost, t.targetHost)

	conn = NewHTTPRewriteResponseWrapper(conn, func(response *ParsedHTTPResponse) (err error) {

		// TODO: rewrite set-cookie?
		// < Set-Cookie: AEC=AakniGN3yfD9T7JL04uIHj66kEw9J64_4QoPZ-VGGGr_lyGqkZhnJjTGyw; expires=Sun, 05-Feb-2023 02:19:40 GMT; path=/; domain=.google.com; Secure; HttpOnly; SameSite=lax
		// TODO: rewrite to change scheme, e.g., http->https. /host: -> /https-host: or /secure-host:

		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location := response.Headers.Get("Location")
			if len(location) > 0 {
				u, err := url.Parse(location)
				if err != nil {
					log.Debug("http-response-location-rewrite: warning, unable to parse Location header value:", err)
				} else if len(u.Host) > 0 {
					targetHost := u.Host
					u.Host = t.proxyHost
					location, err = URLJoinPath(u.String(), fmt.Sprintf("host:%s", targetHost))
					if err != nil {
						log.Debug("http-response-location-rewrite: warning, unable join url path:", err)
					}
					// location = path.Join(u.String(), fmt.Sprintf("host:%s", u.Host))
					response.Headers.Set("Location", location)
				}
			}
		}

		return
	})

	return
}

// TODO: move permanently rewrite Location to proxy with new host: scheme

/*
type HTTPResponseLocationRewrite struct {
	proxyHost     string
	targetHost    string
	inner         io.ReadWriteCloser
	headerWritten bool
	input         bytes.Buffer
	output        bytes.Buffer
	buff          [0xfff]byte
}

func NewHTTPResponseLocationRewrite(conn io.ReadWriteCloser, proxyHost, targetHost string) *HTTPResponseLocationRewrite {
	return &HTTPResponseLocationRewrite{proxyHost: proxyHost, targetHost: targetHost, inner: conn}
}

func (r *HTTPResponseLocationRewrite) Read(buff []byte) (n int, err error) {

	if r.output.Len() > 0 {
		n, err = r.output.Read(buff)
		return
	} else if r.headerWritten {
		n, err = r.inner.Read(buff)
		return
	}

	var in int

	in, err = r.inner.Read(r.buff[:])
	if err != nil {
		return
	}

	_, err = r.input.Write(r.buff[:in])
	if err != nil {
		return
	}

	b := r.input.Bytes()

	res, err := ParseHTTPResponse(b)

	if err != nil {
		return
	}
	if res == nil {
		return
	}

	if res.StatusCode >= 300 && res.StatusCode < 400 {
		location := res.Headers.Get("Location")
		if len(location) > 0 {
			u, err := url.Parse(location)
			if err != nil {
				log.Debug("http-response-location-rewrite: warning, unable to parse Location header value:", err)
			} else if len(u.Host) > 0 {
				targetHost := u.Host
				u.Host = r.proxyHost
				location, err = URLJoinPath(u.String(), fmt.Sprintf("host:%s", targetHost))
				if err != nil {
					log.Debug("http-response-location-rewrite: warning, unable join url path:", err)
				}
				// location = path.Join(u.String(), fmt.Sprintf("host:%s", u.Host))
				res.Headers.Set("Location", location)
			}
		}
	}

	// log.Trace("http-response-location-rewrite: parsed response headers:", res.Headers)

	err = res.Write(&r.output, false, false, true)
	if err != nil {
		return
	}

	r.headerWritten = true

	n, err = r.output.Read(buff)

	return
}

func (r *HTTPResponseLocationRewrite) Write(buff []byte) (n int, err error) {
	return r.inner.Write(buff)
}

func (r *HTTPResponseLocationRewrite) Close() error {
	return r.inner.Close()
}
*/
