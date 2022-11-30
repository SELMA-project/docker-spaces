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
	broker *Broker
	ID     string
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

func (h *HTTPContainerHandler) processRequestHead(logger *ProxyLogger, request *ParsedHTTPRequest) (level int, info *DockerContainerInfo, err error) {

	// log := logger.WithExtension(": container-handler: process-request-head")
	log := logger
	fmt := log.E

	request.Path, info, err = h.parseURLPath(request.Path)
	if err != nil {
		err = fmt.Errorf("error parsing path: %w", err)
		return
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

	level, info, err := h.processRequestHead(log, request)
	if info != nil && err == nil {
		return level
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

	_, containerInfo, err = h.processRequestHead(logger, request)

	if containerInfo == nil {
		log.Debug("not a container request:", request.Short())
		return
	}

	log.Trace("request path is now:", request.Path)

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

	targetAddress = remoteAddress

	targetID = remoteAddress

	if prevTargetID == targetID && prevTargetConn != nil {
		targetConn = prevTargetConn
	} else {
		// connect
		log.Trace("connecting to target:", remoteAddress)
		secure := false
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

	return
}

func (h *HTTPContainerHandler) ProcessResponse(logger *ProxyLogger, response *ParsedHTTPResponse) (err error) {

	log := logger.WithExtension(": container-handler: process-response")
	// fmt := log.E

	// CORS
	response.Headers.Add("Access-Control-Request-Headers", "Content-Type")
	response.Headers.Add("Access-Control-Allow-Headers", "Content-Type")
	response.Headers.Add("Access-Control-Allow-Origin", "*")
	response.Headers.Add("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
	response.Headers.Add("Access-Control-Allow-Credentials", "true")

	log.Trace("got response from container:", response)

	return
}

// target: http://localhost:7890/container:user=selmaproject;repo=uc0:latest;gpu=true;port=6677;env=RABBITMQ_HOST=rabbitmq.abc.com;/to-be-continued/abc

func (h *HTTPContainerHandler) parseURLPath(path string) (pathRewrite string, info *DockerContainerInfo, err error) {

	pathRewrite = path

	if !strings.HasPrefix(path, "/container:") {
		return
	}

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
	if len(gpu) > 0 && (gpu == "true" || gpu == "1" || gpu == "t" || gpu == "yes" || gpu == "y") {
		info.gpu = true
	}

	info.port, err = strconv.Atoi(args["port"])

	info.envs = envs
	info.Type = args["type"]

	pathRewrite = "/"

	if len(pathParts) > 2 {
		pathRewrite += pathParts[2]
	}

	return
}
