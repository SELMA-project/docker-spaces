package main

import (
	"crypto/tls"
	"io"
	"net"
	"net/url"
	"strings"
)

type HTTPStaticHostHandler struct {
	ID        string
	proxyHost *url.URL
}

func (h *HTTPStaticHostHandler) String() string {
	return "HTTPStaticHostHandler"
}

func (h *HTTPStaticHostHandler) Closed(logger *ProxyLogger, request *ParsedHTTPRequest) {

	log := logger.WithExtension("-closed")

	log.Trace("connection closed")

	return
}

func (h *HTTPStaticHostHandler) resolveByReferrer(logger *ProxyLogger, request *ParsedHTTPRequest, referrer string) (targetAddress string, secure bool, err error) {

	log := logger
	fmt := log.E

	if len(referrer) == 0 {
		return
	}

	// referrer is non-zero, if fail, then exit
	if strings.HasPrefix(referrer, "/") {
		// absolute path without host starting with /
		_, targetAddress, secure, err = h.parseURLPath(referrer)
		if err != nil {
			err = fmt.Errorf("invalid path: %w", err)
			return
		}
	} else {
		var ref *url.URL
		ref, err = url.Parse(referrer)
		if err != nil {
			return
		}
		// same host is required
		if ref.Host != request.Headers.Get("Host") {
			log.Warn("referrer host %s does not match with the Host header %s", ref.Host, request.Headers.Get("Host"))
			return
		}
		_, targetAddress, secure, err = h.parseURLPath(ref.Path)
		if err != nil {
			return
		}
	}

	return
}

func (h *HTTPStaticHostHandler) processRequestHead(logger *ProxyLogger, request *ParsedHTTPRequest) (level int, targetAddress string, secure bool, err error) {

	// log := logger.WithExtension(": static-host: process-request-head")
	log := logger
	fmt := log.E

	request.Path, targetAddress, secure, err = h.parseURLPath(request.Path)
	if err != nil {
		err = fmt.Errorf("error parsing path: %w", err)
		return
	}
	if len(targetAddress) > 0 {
		level = 0
		log.Info("resolved via path")
	} else {
		// try with referer header
		referrer := request.Headers.Get("Referer")

		targetAddress, secure, err = h.resolveByReferrer(logger, request, referrer)

		if err == nil && len(targetAddress) > 0 {
			level = 1
			log.Info("resolved via referrer:", referrer)
		}
	}

	// prefix, target, targetPath := h.splitURLPath(ref.Path)
	// targetConn, err = HTTPBufferResponse("307 Temporary Redirect", http.Header{"Location": []string{fmt.Sprintf("/%s:%s%s", prefix, target, targetPath)}}, "")

	// prefix, target, _ /*targetPath*/ := h.splitURLPath(ref.Path)
	// targetConn, err = HTTPBufferResponse("307 Temporary Redirect", http.Header{"Location": []string{fmt.Sprintf("/%s:%s%s", prefix, target, request.Path)}}, "")
	// targetID = "redirect" // TODO: add random id or location hash so it won't match, is it necessary?

	if len(targetAddress) == 0 {
		// let's try once more by checking cookies
		cookies := map[string]string{}
		for _, cookiedef := range request.Headers.Values("Cookie") {
			for _, kvdef := range strings.Split(cookiedef, "; ") {
				kv := strings.SplitN(kvdef, "=", 2)
				key := kv[0]
				value := ""
				if len(kv) > 1 {
					value = kv[1]
				}
				cookies[key] = value
			}
		}
		if cookie, ok := cookies["Reverse-Proxy-Host-"+h.ID]; ok {
			// we expect the same format as the referrer header, i.e., http[s]://host:port/[s]host:<host>[:port]
			// so that the first segment of the path can be parsed out

			targetAddress, secure, err = h.resolveByReferrer(logger, request, cookie)

			if err == nil && len(targetAddress) > 0 {
				level = 2
				log.Info("resolved via cookie:", cookie)
			}

			// prefix, target, _ /*targetPath*/ := h.splitURLPath(ref.Path)
			// targetConn, err = HTTPBufferResponse("307 Temporary Redirect", http.Header{"Location": []string{fmt.Sprintf("/%s:%s%s", prefix, target, request.Path)}}, "")
			// targetID = "redirect" // TODO: add random id or location hash so it won't match
			// return
		}
	}

	return
}

func (h *HTTPStaticHostHandler) RespondsAtLevel(logger *ProxyLogger, request *ParsedHTTPRequest) int {

	log := logger.WithExtension(": static-host: responds-at-level")

	level, targetAddress, _, err := h.processRequestHead(log, request)
	if len(targetAddress) > 0 && err == nil {
		return level
	}

	return -1
}

func (h *HTTPStaticHostHandler) ProcessRequest(
	logger *ProxyLogger,
	request *ParsedHTTPRequest,
	prevTargetConn io.ReadWriteCloser,
	prevTargetID string) (targetConn io.ReadWriteCloser, targetID string, err error) {

	log := logger.WithExtension(": static-host: process-request")
	fmt := log.E

	var targetAddress string

	defer func() {
		// close previous connection if not passed through
		if prevTargetConn != nil && prevTargetConn != targetConn {
			prevTargetConn.Close()
		}
		// on error close target connection if by some reason it is open
		if err != nil && targetConn != nil {
			targetConn.Close()
			targetConn = nil
		}
		if targetConn != nil && request != nil {
			log.Debugf("forwarding request to target %s: %s\n", targetAddress, request)
		}
	}()

	log.Debug("got HTTP request:", request)

	_, targetAddress, secure, err := h.processRequestHead(logger, request)

	if len(targetAddress) == 0 {
		log.Debug("not a host request:", request.Method, request.Path)
		return
	}

	log.Trace("request path is now:", request.Path)

	// if len(info.address) == 0 {
	// 	err = fmt.Errorf("target not found")
	// 	return
	// }

	log.Trace("remote address:", targetAddress)

	// add default port to remote address if missing
	if len(strings.SplitN(targetAddress, ":", 2)) < 2 {
		if secure {
			targetAddress += ":443"
		} else {
			targetAddress += ":80"
		}
	}

	targetID = targetAddress

	if secure {
		targetID = "https://" + targetID
	}

	if prevTargetID == targetID && prevTargetConn != nil {
		targetConn = prevTargetConn
	} else {
		// connect
		log.Trace("connect: connecting to target:", targetAddress)
		if secure {
			targetConn, err = tls.Dial("tcp", targetAddress, nil)
		} else {
			targetConn, err = net.Dial("tcp", targetAddress)
		}
		// log.Tracef("connection %v, err: %v", targetConn, err)
		if err != nil {
			err = fmt.Errorf("connect: remote connection to %s failed: %w", targetAddress, err)
			// if prevTargetConn != nil {
			// 	prevTargetConn.Close()
			// }
			return
		}
	}

	// log.Trace("resolved before cookie cleanup:", "\n"+httpHeadersToString(request.Headers, "> "))

	// remove proxy cookie
	if cookiesets, present := request.Headers["Cookie"]; present {
		for i, cookieset := range cookiesets {
			cookies := strings.Split(cookieset, "; ")
			for j, cookie := range cookies {
				if strings.HasPrefix(cookie, "Reverse-Proxy-Host-"+h.ID+"=") {
					cookies = append(cookies[:j], cookies[j+1:]...)
					cookiesets[i] = strings.Join(cookies, "; ")
					break
				}
			}
		}
	}

	// log.Trace("resolved after cookie cleanup:", "\n"+httpHeadersToString(request.Headers, "> "))

	proxyHost := request.Headers.Get("Host") // host:port
	if !strings.HasPrefix(proxyHost, "http:") && !strings.HasPrefix(proxyHost, "https:") {
		if sourceInfo, ok := request.ConnectionInfo.(*ProxyConnInfo); ok {
			if sourceInfo.TLS {
				proxyHost = "https://" + proxyHost
			} else {
				proxyHost = "http://" + proxyHost
			}
		} else {
			log.Warn("unable to decode request connection info: %+v", request.ConnectionInfo)
			proxyHost = "http://" + proxyHost
		}
	}
	log.Trace("proxy host:", proxyHost)
	// if len(proxyHost) > 0 {
	h.proxyHost, err = url.Parse(proxyHost)
	if err != nil {
		err = fmt.Errorf("unable to parse Host header: %w", err)
	}
	// }
	// h.proxyHost = request.Headers.Get("Host")
	// targetHost := strings.SplitN(info, ":", 2)[0]
	targetHost := targetAddress

	request.Headers.Set("Host", targetHost) // set host to target

	request.UserData = secure

	// some resource at root,i.e., GET /some-resource
	// with referrer: http://proxy/http://target

	// convert to GET /http://target/some-resource

	// fix referrer
	if true {
		var ref *url.URL
		referer := request.Headers.Get("Referer")
		if len(referer) > 0 {
			log.Info("REFERER", referer)
			ref, _ = url.Parse(referer)
			log.Info("PARSED REFERER", ref)
			log.Info("HOST", request.Headers.Get("Host"), "==", ref.Host)

			// TODO: in case of host:port, the parse will fill .Scheme and .Opaque
			// this checks for /path case when Host is empty or if the request is for the same host
			if ref != nil /* && (len(ref.Host) == 0 || ref.Host == request.Headers.Get("Host")) */ {
				log.Info("UPDATING REFERER")
				ref.Path, targetAddress, secure, err = h.parseURLPath(ref.Path)
				if err != nil {
					log.Errorf("error parsing referer path: %v", err)
				} else {
					log.Info("referrer:", ref.Path)
				}
				ref.Host = targetHost

				// prefix, target, targetPath := h.splitURLPath(ref.Path)
				// targetConn, err = HTTPBufferResponse("307 Temporary Redirect", http.Header{"Location": []string{fmt.Sprintf("/%s:%s%s", prefix, target, targetPath)}}, "")

				// prefix, target, _ /*targetPath*/ := h.splitURLPath(ref.Path)
				// targetConn, err = HTTPBufferResponse("307 Temporary Redirect", http.Header{"Location": []string{fmt.Sprintf("/%s:%s%s", prefix, target, request.Path)}}, "")
				// targetID = "redirect" // TODO: add random id or location hash so it won't match, is it necessary?
				// return

				request.Headers.Set("Referer", ref.String())
			}
		}
	}
	if true {
		var ref *url.URL
		origin := request.Headers.Get("Origin")
		if len(origin) > 0 {
			ref, _ = url.Parse(origin)

			if ref != nil /* && (len(ref.Host) == 0 || ref.Host == request.Headers.Get("Host")) */ {
				ref.Host = targetHost
				request.Headers.Set("Origin", ref.String())
			}
		}
	}

	return
}

func (h *HTTPStaticHostHandler) ProcessResponse(logger *ProxyLogger, response *ParsedHTTPResponse) (err error) {

	log := logger.WithExtension(": static-host: process-response")
	fmt := log.E

	request := response.Request

	// TODO: rewrite set-cookie?
	// < Set-Cookie: AEC=AakniGN3yfD9T7JL04uIHj66kEw9J64_4QoPZ-VGGGr_lyGqkZhnJjTGyw; expires=Sun, 05-Feb-2023 02:19:40 GMT; path=/; domain=.google.com; Secure; HttpOnly; SameSite=lax
	// TODO: rewrite to change scheme, e.g., http->https. /host: -> /https-host: or /secure-host:

	defer func() {
		log.Debug("forwarding response to caller:", response)
	}()

	// log.Debug("got response:", response)

	log.Debug("incoming response from", request.Headers.Get("Host"), "for request:", request.Short())
	log.Debug("request to proxy was:", request.Original.Short())
	log.Debug("response:", response)

	// log.Debug("response set-cookie:", response.Headers["Set-Cookie"])

	// TODO: modify set-cookie
	// modify set-cookie headers to remove domain
	if setCookies, present := response.Headers["Set-Cookie"]; present {
		for i, setCookie := range setCookies {
			// name, value, attrs, unparsed := parseSetCookieHeader(setCookie)
			// TODO: check name
			_, _, _, unparsed := parseSetCookieHeader(setCookie)
			for j, attrval := range unparsed {
				// remove domain attr
				// TODO: copy domain attr, in case the app tries to access it's original backend, e.g., like google.com tries to store the consent
				if strings.HasPrefix(strings.ToLower(attrval), "domain=") {
					unparsed = append(unparsed[:j], unparsed[j+1:]...)
					break
				}
			}
			setCookies[i] = strings.Join(unparsed, "; ")
		}
	}

	// log.Debug("response set-cookie after:", response.Headers["Set-Cookie"])

	if response.StatusCode >= 300 && response.StatusCode < 400 {
		// redirect to target host url, e.g., --> http://www.google.com
		// rewrite to --> proxyhost/http[s]:targethost/target/host/path
		location := response.Headers.Get("Location")
		if len(location) > 0 {
			u, err := url.Parse(location)
			if err != nil {
				log.Debug("warning, unable to parse Location header value:", err)
			} else if len(u.Host) > 0 {
				// log.Debug("rewrite redirect from", location, "to proxy host", h.proxyHost)
				// log.Trace("response location rewrite: before", u.String(), ";", u)
				targetHost := u.Host
				secure := ""
				if u.Scheme == "https" {
					secure = "s"
				}
				// u.Host = h.proxyHost
				u.Host = h.proxyHost.Host
				u.Scheme = h.proxyHost.Scheme
				if len(u.Path) == 0 {
					u.Path = "/"
				}
				u.Path = fmt.Sprintf("http%s:%s%s", secure, targetHost, u.Path)
				// log.Trace("response location rewrite: after", u.String(), ";", u)
				log.Debug("rewrite redirect location:", location, "==>", u.String())
				// location, err = URLJoinPath(u.String(), fmt.Sprintf("%shost:%s", secure, targetHost))
				// if err != nil {
				// 	log.Debug("warning, unable join url path:", err)
				// }
				// location = path.Join(u.String(), fmt.Sprintf("host:%s", u.Host))
				response.Headers.Set("Location", u.String())
				// response.Headers.Set("Location", location)
			}
		}
	} // else {
	if true {
		// TODO: check if redirect to the same remote host
		// TODO: set cookie specific to this host/path, set path as /
		var u url.URL
		u.Host = h.proxyHost.Host
		u.Scheme = h.proxyHost.Scheme
		// TODO: determine if it's host or shost, i.e., to use TLS for the target connection

		secure := ""
		if useTLS, ok := request.UserData.(bool); ok {
			if useTLS {
				secure = "s"
			}
		}

		if len(u.Path) == 0 {
			u.Path = "/"
		}
		// TODO: nested hosts?
		u.Path = fmt.Sprintf("http%s:%s%s", secure, request.Headers.Get("Host"), u.Path)

		response.Headers.Add("Set-Cookie", "Reverse-Proxy-Host-"+h.ID+"="+u.String()+"; path=/")
	}

	return
}

func (h *HTTPStaticHostHandler) parseURLPath(path string) (pathRewrite string, target string, tls bool, err error) {

	prefix, target, pathRewrite := h.splitURLPath(path)
	if len(prefix) == 0 {
		return
	}

	tls = prefix == "https"

	return
}

func (h *HTTPStaticHostHandler) splitURLPath(path string) (prefix string, target string, targetPath string) {

	for _, pre := range []string{"/host:", "/shost:", "/http:", "/https:"} {
		if !strings.HasPrefix(path, pre) {
			continue
		}
		// prefix = strings.Trim(pre, "/:")

		url := strings.SplitN(strings.TrimLeft(path, "/") /* omit leading / */, ":", 2)

		scheme := url[0] // schema

		for _, httpPrefix := range []string{"http://", "https://"} {
			if strings.HasPrefix(url[1], httpPrefix) {
				scheme = "http"
				url[1] = strings.TrimPrefix(url[1], httpPrefix)
				break
			}
		}

		// normalize scheme
		if scheme == "host" {
			scheme = "http"
		} else if scheme == "shost" {
			scheme = "https"
		}

		prefix = scheme

		hostPath := strings.SplitN(strings.TrimLeft(url[1], "/") /* trim leading /'s => host/path */, "/", 2)

		host := hostPath[0]

		target = host

		if len(hostPath) == 2 {
			targetPath = "/" + hostPath[1]
		} else {
			targetPath = "/"
		}

		return
	}

	targetPath = path
	return
}
