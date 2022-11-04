package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type MonitorTargetResolver struct {
	broker *Broker
}

// func (r *HostTargetResolver) parseURLPath(path string) (pathRewrite string, info *TargetHostInfo, err error) {
func (r *MonitorTargetResolver) parseURLPath(path string) (pathRewrite string, info string, err error) {

	// /host:<host>:<port>/...

	pathRewrite = path // defaults to same path, alternative default: return empty string if no rewrite happens

	if !strings.HasPrefix(path, "/monitor:broker") {
		// err = fmt.Errorf("parse-url-path: invalid dynamic run path") // TODO: how to call this
		return
	}

	pathParts := strings.SplitN(path, "/", 3)

	// assert pathParts[0] == "" // not absolute path

	ps := strings.SplitN(pathParts[1], ":", 2)

	// /host:<host>:<port>/...
	if len(ps) < 2 {
		err = fmt.Errorf("monitor-parse-url-path: invalid path format")
		return
	}

	// port, err := strconv.Atoi(ps[2])
	// if err != nil {
	// 	err = fmt.Errorf("parse-url-path: invalid path: invalid port %s: %v", ps[2], err)
	// 	return
	// }
	//
	// info = &TargetHostInfo{Address: ps[1], Port: port}

	info = "monitor:broker"

	/*
		info = ps[1]

		pathRewrite = "/"

		if len(pathParts) > 2 {
			pathRewrite += pathParts[2]
		}
	*/

	return
}

func (r *MonitorTargetResolver) Resolve(buff []byte) (target ResolvedTarget, err error) {

	request, err := ParseHTTPRequest(buff)
	if request == nil {
		return
	}

	target, err = r.ResolveHTTPRequest(request)

	return
}

func (r *MonitorTargetResolver) ResolveHTTPRequest(request *ParsedHTTPRequest) (target ResolvedTarget, err error) {

	log.Debug("monitor-resolver: got HTTP request:", request.Method, request.Path, request.Version)
	// log.Debug("host-resolver: got HTTP Headers", request.Headers)

	// var info *TargetHostInfo
	var info string

	// request.Path, info, err = r.parseURLPath(request.Path)
	_, info, err = r.parseURLPath(request.Path)
	if err != nil {
		err = fmt.Errorf("monitor-resolver: invalid path: %w", err)
		return
	}
	log.Trace("monitor-resolver: request path is now:", request.Path)

	// if info == nil {
	if len(info) == 0 {
		log.Debug("monitor-resolver: not a host request request:", request.Method, request.Path)
		return
	}

	// if len(info.address) == 0 {
	// 	err = fmt.Errorf("host-resolver: target not found")
	// 	return
	// }

	log.Trace("monitor-resolver: remote address:", info)

	/*
		if len(strings.SplitN(info, ":", 2)) < 2 {
			// TODO: how to know if it's https?
			info += ":80"
		}

		proxyHost := request.Headers.Get("Host")
		targetHost := strings.SplitN(info, ":", 2)[0]
	*/

	// request.Headers.Set("Host", targetHost) // set host to target

	// log.Trace("host-resolver: request:", string(request.Data(true, false, true)))

	// target = &BasicResolvedTarget{Address: info.Address, Data: request.Data(true, false, false), ActivityCallback: nil, ClosedCallback: nil}

	target = &ResolvedMonitorTarget{BasicResolvedTarget{Address: info, Data: request.Data(true, false, true), ActivityCallback: nil, ClosedCallback: nil} /*, proxyHost, targetHost*/, r.broker}

	return
}

type ResolvedMonitorTarget struct {
	BasicResolvedTarget
	broker *Broker
	// proxyHost  string
	// targetHost string
	// prefix string
}

/*
func (t *ResolvedMonitorTarget) parseURLPath(path string) (pathRewrite string, info string, err error) {

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
*/

/*
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
*/

type HTTPResponse struct {
	bytes.Buffer
	// headers http.Header
}

func (r *HTTPResponse) Close() error {
	return nil
}

func (t *ResolvedMonitorTarget) Connect() (conn io.ReadWriteCloser, err error) {

	conn = &HTTPResponse{}

	headers := http.Header{}
	content := &bytes.Buffer{}

	// content.Write([]byte("broker state:\n"))
	t.broker.State = t.broker.JSON()
	content.Write(t.broker.State)

	headers.Add("Content-Length", strconv.Itoa(content.Len()))
	headers.Add("Content-Type", "application/json")
	headers.Add("Host", "localhost")

	conn.Write([]byte("HTTP/1.1 200 OK\r\n"))
	headers.Write(conn)
	conn.Write([]byte{13, 10})
	conn.Write(content.Bytes())

	return

	// connect and wrap...
	// remoteAddress := t.Address
	// log.Trace("resolved-monitor-target: connect: connecting to target:", remoteAddress)
	// conn, err = net.Dial("tcp", remoteAddress)
	// if err != nil {
	// 	err = fmt.Errorf("resolved-http-target: connect: remote connection to %s failed: %w", remoteAddress, err)
	// 	return
	// }

	// conn = NewHTTPResponseLocationRewrite(conn, t.proxyHost, t.targetHost)

	// conn = NewHTTPRewriteResponseWrapper(conn, func(response *ParsedHTTPResponse) (err error) {

	// TODO: rewrite set-cookie?
	// < Set-Cookie: AEC=AakniGN3yfD9T7JL04uIHj66kEw9J64_4QoPZ-VGGGr_lyGqkZhnJjTGyw; expires=Sun, 05-Feb-2023 02:19:40 GMT; path=/; domain=.google.com; Secure; HttpOnly; SameSite=lax
	// TODO: rewrite to change scheme, e.g., http->https. /host: -> /https-host: or /secure-host:

	/*
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
	*/

	// 	return
	// })

	return
}
