package main

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
)

type RootTargetResolver struct {
	TargetAddress string
}

func (r *RootTargetResolver) Resolve(buff []byte) (target ResolvedTarget, err error) {

	request, err := ParseHTTPRequest(buff)
	if request == nil {
		return
	}

	target, err = r.ResolveHTTPRequest(request)

	return
}

func (r *RootTargetResolver) ResolveHTTPRequest(request *ParsedHTTPRequest) (target ResolvedTarget, err error) {

	if len(r.TargetAddress) == 0 {
		return
	}

	log.Debug("root-resolver: got HTTP request:", request.Method, request.Path, request.Version)

	log.Trace("root-resolver: remote address:", r.TargetAddress)

	if len(strings.SplitN(r.TargetAddress, ":", 2)) < 2 {
		// TODO: how to know if it's https?
		r.TargetAddress += ":80"
	}

	proxyHost := request.Headers.Get("Host")
	targetHost := strings.SplitN(r.TargetAddress, ":", 2)[0]

	// request.Headers.Set("Host", targetHost) // set host to target

	// log.Trace("host-resolver: request:", string(request.Data(true, false, true)))

	// target = &BasicResolvedTarget{Address: info.Address, Data: request.Data(true, false, false), ActivityCallback: nil, ClosedCallback: nil}
	target = &ResolvedRootHTTPTarget{BasicResolvedTarget{Address: r.TargetAddress, Data: request.Data(true, false, true), ActivityCallback: nil, ClosedCallback: nil}, proxyHost, targetHost}

	return
}

type ResolvedRootHTTPTarget struct {
	BasicResolvedTarget
	proxyHost  string
	targetHost string
	// prefix string
}

func (t *ResolvedRootHTTPTarget) WrapProxyConnection(conn io.ReadWriter) io.ReadWriter {

	log.Debug("docker-resolver-target: wrap proxy connection")

	conn = NewHTTPRewriteRequestWrapper(conn, func(request *ParsedHTTPRequest) (err error) {

		// request.Path, err = t.parseURLPath(request.Path)
		// log.Trace("docker-resolver: wrapped proxy conn request:", string(request.Data(true, false, true)))
		// ----------------

		log.Debug("root-resolver: got HTTP request:", request.Method, request.Path, request.Version)
		// log.Debug("host-resolver: got HTTP Headers", request.Headers)

		// var info *TargetHostInfo
		// var info string

		// request.Path, info, err = t.parseURLPath(request.Path)
		// if err != nil {
		// 	err = fmt.Errorf("host-resolver: invalid path: %w", err)
		// 	return
		// }
		// log.Trace("host-resolver: request path is now:", request.Path)

		// if info == nil {
		if len(t.Address) == 0 {
			log.Debug("root-resolver: not a host request request:", request.Method, request.Path)
			// TODO: what to do if this fails? drop connection at this point?
			err = fmt.Errorf("root-resolver: not a host request request: %s %s", request.Method, request.Path)
			return
		}

		// if len(info.address) == 0 {
		// 	err = fmt.Errorf("host-resolver: target not found")
		// 	return
		// }

		log.Trace("root-resolver: remote address:", t.Address)

		if len(strings.SplitN(t.Address, ":", 2)) < 2 {
			// TODO: how to know if it's https?
			t.Address += ":80"
		}

		t.proxyHost = request.Headers.Get("Host")
		t.targetHost = strings.SplitN(t.Address, ":", 2)[0]

		request.Headers.Set("Host", t.targetHost) // set host to target

		// log.Trace("host-resolver: request:", string(request.Data(true, false, true)))

		return
	})

	return conn
}

func (t *ResolvedRootHTTPTarget) Connect() (conn io.ReadWriteCloser, err error) {
	// connect and wrap...
	remoteAddress := t.Address
	log.Trace("resolved-root-http-target: connect: connecting to target:", remoteAddress)
	conn, err = net.Dial("tcp", remoteAddress)
	if err != nil {
		err = fmt.Errorf("resolved-root-http-target: connect: remote connection to %s failed: %w", remoteAddress, err)
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
					// targetHost := u.Host
					u.Host = t.proxyHost
					// location, err = URLJoinPath(u.String(), fmt.Sprintf("host:%s", targetHost))
					location = u.String() //URLJoinPath(u.String(), fmt.Sprintf("host:%s", targetHost))
					if err != nil {
						log.Debug("http-root-response-location-rewrite: warning, unable join url path:", err)
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
