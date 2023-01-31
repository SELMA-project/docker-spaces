package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type HTTPContainerHandler struct {
	broker    *Broker
	ID        string
	proxyHost *url.URL
}

func (h *HTTPContainerHandler) String() string {
	return "HTTPContainerHandler"
}

func (h *HTTPContainerHandler) Closed(logger *ProxyLogger, request *ParsedHTTPRequest) {

	log := logger.WithExtension(": container-handler: closed")

	if request == nil {
		log.Warn("request is nil")
		return
	}

	if request.UserData == nil {
		log.Warn("request user data is empty")
		return
	}

	if slot, ok := request.UserData.(*BrokerSlot); ok {
		slot.Send(NewBrokerMessage(BrokerMessageRelease, false))
	}

	return
}

func (h *HTTPContainerHandler) resolveByReferrer(logger *ProxyLogger, request *ParsedHTTPRequest, referrer string) (info *DockerContainerInfo, err error) {

	log := logger
	fmt := log.E

	if len(referrer) == 0 {
		return
	}

	// referrer is non-zero, if fail, then exit
	if strings.HasPrefix(referrer, "/") {
		// absolute path without host starting with /
		_, info, err = h.parseURLPath(referrer)
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
			log.Warnf("referrer host %s does not match with the Host header %s", ref.Host, request.Headers.Get("Host"))
			return
		}
		_, info, err = h.parseURLPath(ref.Path)
		if err != nil {
			return
		}
	}

	return
}

func (h *HTTPContainerHandler) processRequestHead(logger *ProxyLogger, request *ParsedHTTPRequest, dryRun bool) (level int, info *DockerContainerInfo, err error) {

	// log := logger.WithExtension(": container-handler: process-request-head")
	log := logger
	fmt := log.E

	level = -1

	var newPath string
	newPath, info, err = h.parseURLPath(request.Path)
	if err != nil {
		err = fmt.Errorf("error parsing path: %w", err)
		return
	}
	if !dryRun {
		request.Path = newPath
	}
	if info != nil {
		level = 0
		log.Info("resolved via path")
	} else {
		// try with referer header
		referrer := request.Headers.Get("Referer")

		info, err = h.resolveByReferrer(logger, request, referrer)

		log.Tracef("process-request-head: resolve by referrer %s got error: %v", referrer, err)

		if err == nil && info != nil {
			level = 1
			log.Info("resolved via referrer:", referrer)
		}
	}

	// prefix, target, targetPath := h.splitURLPath(ref.Path)
	// targetConn, err = HTTPBufferResponse("307 Temporary Redirect", http.Header{"Location": []string{fmt.Sprintf("/%s:%s%s", prefix, target, targetPath)}}, "")

	// prefix, target, _ /*targetPath*/ := h.splitURLPath(ref.Path)
	// targetConn, err = HTTPBufferResponse("307 Temporary Redirect", http.Header{"Location": []string{fmt.Sprintf("/%s:%s%s", prefix, target, request.Path)}}, "")
	// targetID = "redirect" // TODO: add random id or location hash so it won't match, is it necessary?

	if info == nil {
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

			info, err = h.resolveByReferrer(logger, request, cookie)

			if err == nil && info != nil {
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

func (h *HTTPContainerHandler) RespondsAtLevel(logger *ProxyLogger, request *ParsedHTTPRequest) int {

	log := logger.WithExtension(": container-handler: responds-at-level")

	level, info, err := h.processRequestHead(log, request, true)
	if info != nil && err == nil {
		return level
	}

	if err != nil {
		log.Tracef("responds-at-level: error: %v", err)
	}

	return -1
}

func (h *HTTPContainerHandler) ProcessRequest(
	logger *ProxyLogger,
	request *ParsedHTTPRequest,
	prevTargetConn io.ReadWriteCloser,
	prevTargetID string) (targetConn io.ReadWriteCloser, targetID string, err error) {

	log := logger.WithExtension(": container-handler: process-request")
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

	var containerInfo *DockerContainerInfo

	yType := false

	_, containerInfo, err = h.processRequestHead(logger, request, false)

	if containerInfo == nil {
		log.Debug("not a container request:", request.Short())
		return
	}

	log.Trace("request path is now:", request.Path)

	proxyHost := request.Headers.Get("Host") // host:port
	h.proxyHost, err = parseURL(proxyHost)
	log.Tracef("parsed proxy host %s to: %#v", proxyHost, h.proxyHost)
	if err != nil {
		err = fmt.Errorf("unable to parse Host header: %w", err)
	} else {
		if sourceInfo, ok := request.ConnectionInfo.(*ProxyConnInfo); ok {
			if sourceInfo.TLS {
				h.proxyHost.Scheme = "https"
				// proxyHost = "https://" + proxyHost
			} else {
				h.proxyHost.Scheme = "http"
				// proxyHost = "http://" + proxyHost
			}
		} else {
			log.Warn("unable to decode request connection info: %+v", request.ConnectionInfo)
			// proxyHost = "http://" + proxyHost
		}
		proxyHost = h.proxyHost.String()
	}
	log.Trace("proxy host:", proxyHost)

	/// -------

	yType = containerInfo.Type == "y"

	typePrefix := "x:"
	if yType {
		typePrefix = "y:"
	}

	slotType := typePrefix + containerInfo.image
	log.Trace("got slot type:", slotType)

	slot := h.broker.GetSourceSlot()
	log.Tracef("got free slot: %+v", slot)

	request.UserData = slot

	targetID = "waiting-for-container"

	// slot.Send(NewBrokerMessage(BrokerMessageAcquire, fmt.Sprint("%s:%d", image, port))) // acquire
	slot.Send(NewBrokerMessage(BrokerMessageAcquire, BrokerAcquireMessageData{slotType, containerInfo})) // acquire
	message := slot.Read()

	log.Debug("TCP end got message:", message)

	var remoteAddress string

	switch message.Type() {
	case BrokerMessageAcquired:
		remoteAddress = message.PayloadString()
	case BrokerMessageError:
		err = fmt.Errorf("broker error: %v", message.PayloadString())
		// slot.Send(NewBrokerMessage(BrokerMessageRelease, false))
		return
	default:
		err = fmt.Errorf("unknown message from broker: %v", message)
		return
	}

	// activityNotifier := func() {
	// 	slot.Send(NewBrokerMessage(BrokerMessageRelease, false))
	// }

	if len(remoteAddress) == 0 {
		err = fmt.Errorf("target not found")
		return
	}

	// add default port to remote address if missing
	if len(strings.SplitN(remoteAddress, ":", 2)) < 2 {
		if containerInfo.tls {
			remoteAddress += ":443"
		} else {
			remoteAddress += ":80"
		}
	}

	targetAddress = remoteAddress

	targetID = remoteAddress

	targetURL, err := parseURL(remoteAddress)
	if err == nil {
		request.Headers.Set("Host", targetURL.Host) // set host to target
	}

	if prevTargetID == targetID && prevTargetConn != nil {
		targetConn = prevTargetConn
	} else {
		// connect
		log.Trace("connecting to target:", remoteAddress)
		secure := false // TODO: ?!
		if secure {
			targetConn, err = tls.Dial("tcp", remoteAddress, nil)
		} else {
			targetConn, err = net.Dial("tcp", remoteAddress)
		}
		// log.Tracef("host-resolver: connection %v, err: %v", targetConn, err)
		if err != nil {
			err = fmt.Errorf("remote connection to %s failed: %w", remoteAddress, err)
			return
		}
	}

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
				ref.Path, _, err = h.parseURLPath(ref.Path)
				if err != nil {
					log.Errorf("error parsing referer path: %v", err)
				} else {
					log.Info("referrer:", ref.Path)
				}
				ref.Host = request.Headers.Get("Host")

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
				ref.Host = request.Headers.Get("Host")
				request.Headers.Set("Origin", ref.String())
			}
		}
	}

	return
}

func (h *HTTPContainerHandler) ProcessResponse(logger *ProxyLogger, response *ParsedHTTPResponse) (err error) {

	log := logger.WithExtension(": container-handler: process-response")
	// fmt := log.E

	defer func() {
		log.Debug("forwarding response to caller:", response)
	}()

	log.Trace("got response from container:", response)

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

	request := response.Request

	// determine base path (with handler annotation) based on request path or request referrer
	basePath := ""
	rewritePath, info, err := h.parseURLPath(request.Original.Path)
	if err == nil && info != nil {
		index := strings.LastIndex(request.Original.Path, rewritePath)
		if index != -1 {
			basePath = request.Original.Path[:index]
		}
	} else {
		referrer := request.Original.Headers.Get("Referer")
		if len(referrer) > 0 {
			u, err := parseURL(referrer)
			if err == nil && len(u.Path) > 0 {
				rewritePath, info, err = h.parseURLPath(u.Path)
				if err == nil && info != nil {
					index := strings.LastIndex(referrer, rewritePath)
					if index != -1 {
						basePath = referrer[:index]
					}
				}
			}
		}
	}

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
				// targetHost := u.Host
				// secure := ""
				// if u.Scheme == "https" {
				// 	secure = "s"
				// }
				// u.Host = h.proxyHost
				u.Host = h.proxyHost.Host
				u.Scheme = h.proxyHost.Scheme
				if len(u.Path) == 0 {
					u.Path = "/"
				}
				// u.Path = fmt.Sprintf("http%s:%s%s", secure, targetHost, u.Path)
				u.Path = basePath + u.Path
				// log.Trace("response location rewrite: after", u.String(), ";", u)
				log.Debug("rewrite redirect location:", location, "==>", u.String())
				// location, err = URLJoinPath(u.String(), fmt.Sprintf("%shost:%s", secure, targetHost))
				// if err != nil {
				// 	log.Debug("warning, unable join url path:", err)
				// }
				// location = path.Join(u.String(), fmt.Sprintf("host:%s", u.Host))
				response.Headers.Set("Location", u.String())
				// response.Headers.Set("Location", location)
			} else if len(u.Path) > 0 {
				u, err := URLJoinPath(basePath, location)
				if err == nil {
					response.Headers.Set("Location", u)
				}
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

		// secure := ""
		// if info, ok := request.UserData.(*DockerContainerInfo); ok {
		// 	if info.tls {
		// 		secure = "s"
		// 	}
		// }

		if len(u.Path) == 0 {
			u.Path = "/"
		}
		// TODO: nested hosts?
		// u.Path = fmt.Sprintf("http%s:%s%s", secure, request.Headers.Get("Host"), u.Path)
		u.Path = basePath + u.Path

		response.Headers.Add("Set-Cookie", "Reverse-Proxy-Host-"+h.ID+"="+u.String()+"; path=/")
	}

	return
}

func (h *HTTPContainerHandler) ResponseTransferred(logger *ProxyLogger, request *ParsedHTTPRequest, response *ParsedHTTPResponse) {

	log := logger.WithExtension(": container-handler: response-transferred")
	// fmt := log.E

	log.Trace("response transferred:", response.Short())

	if request == nil {
		log.Warn("request is nil")
		return
	}

	if request.UserData == nil {
		log.Warn("request user data is empty")
		return
	}

	if slot, ok := request.UserData.(*BrokerSlot); ok {
		slot.Send(NewBrokerMessage(BrokerMessageRelease, false))
	}

	return
}

// target: http://localhost:7890/container:user=selmaproject;repo=uc0:latest;gpu=true;port=6677;env=RABBITMQ_HOST=rabbitmq.abc.com;/to-be-continued/abc

func (h *HTTPContainerHandler) parseBool(value string, defaultValue bool) (result bool, err error) {
	v := strings.ToLower(value)
	if len(v) > 0 {
		if v == "true" || v == "1" || v == "t" || v == "yes" || v == "y" {
			result = true
			return
		}
		if v == "false" || v == "0" || v == "f" || v == "no" || v == "n" {
			result = false
			return
		}
	} else {
		result = defaultValue
		return
	}
	err = fmt.Errorf("parse-bool: invalid value")
	return
}

func (h *HTTPContainerHandler) parseURLPath(path string) (pathRewrite string, info *DockerContainerInfo, err error) {

	pathRewrite = path

	if strings.HasPrefix(path, "/container:") {

		pathParts := strings.SplitN(path, "/", 3)

		var pts []string

		pts = strings.SplitN(pathParts[1], ":", 2)

		argDefs := strings.Split(pts[1], ";")

		args := map[string]string{}
		envs := map[string]string{}

		for _, argDef := range argDefs {
			kv := strings.SplitN(argDef, "=", 2)
			if kv[0] == "env" {
				kv = strings.SplitN(kv[1], "=", 2)
				envs[kv[0]] = kv[1]
			} else {
				args[kv[0]] = kv[1]
			}
		}

		info = &DockerContainerInfo{}

		// user=selmaproject;repo=uc0:latest;gpu=true;port=6677;env=RABBITMQ_HOST=rabbitmq.abc.com;/to-be-continued/abc
		info.image = fmt.Sprintf("%s/%s", args["user"], args["image"])

		gpu := strings.ToLower(args["gpu"])
		info.gpu, err = h.parseBool(gpu, false)
		if err != nil {
			err = fmt.Errorf("parse-url-path: error parsing gpu value: %w", err)
			info = nil
			return
		}

		info.port, err = strconv.Atoi(args["port"])

		info.envs = envs
		info.Type = args["type"]

		info.tls, err = h.parseBool(strings.ToLower(args["tls"]), false)
		if err != nil {
			err = fmt.Errorf("parse-url-path: error parsing tls value: %w", err)
			info = nil
			return
		}

		pathRewrite = "/"

		if len(pathParts) > 2 {
			pathRewrite += pathParts[2]
		}

	} else if strings.HasPrefix(path, "/x:") || strings.HasPrefix(path, "/y:") {

		// path string) (pathRewrite string, info *DockerContainerInfo, err error) {
		// path string) (pathRewrite string, yType bool, info *DockerContainerInfo, err error) {

		// example:
		// http://194.8.1.235:8888/x-selmaproject-tts-777-5002/
		// selmaproject/tts:777 with external port 8765

		yType := false

		if strings.HasPrefix(path, "/y:") {
			yType = true
		}

		pathParts := strings.SplitN(path, "/", 3)

		// assert pathParts[0] == "" // not absolute path

		ps := strings.SplitN(pathParts[1], ":", 6)

		// /x:registry:repo:tag:port/...
		if len(ps) < 5 {
			err = fmt.Errorf("parse-url-path: invalid dynamic run request")
			return
		}

		envs := map[string]string{}

		if len(ps) == 6 {
			envDefs := strings.Split(ps[5], ";")
			for _, envDef := range envDefs {
				kv := strings.SplitN(envDef, "=", 2)
				envs[kv[0]] = kv[1]
			}
		}

		image := fmt.Sprintf("%s/%s:%s", ps[1], ps[2], ps[3])

		info = &DockerContainerInfo{}

		info.image = image

		// gpu := strings.ToLower(args["gpu"])
		// if len(gpu) > 0 && (gpu == "true" || gpu == "1" || gpu == "t" || gpu == "yes" || gpu == "y") {
		// 	info.gpu = true
		// }

		info.port, err = strconv.Atoi(ps[4])

		info.envs = envs

		if yType {
			info.Type = "y"
			// } else {
			// 	info.Type = "x"
		}

		pathRewrite = "/"

		if len(pathParts) > 2 {
			pathRewrite += pathParts[2]
		}
	}

	return
}
