package main

import (
	"io"
	"net"
	"strings"
)

type HTTPDockerLocalHandler struct {
}

func (h *HTTPDockerLocalHandler) String() string {
	return "HTTPDockerLocalHandler"
}

func (h *HTTPDockerLocalHandler) Closed(logger *ProxyLogger, request *ParsedHTTPRequest) {

	log := logger.WithExtension(": docker-local: closed")

	log.Trace("connection closed")

	return
}

func (h *HTTPDockerLocalHandler) RespondsAtLevel(logger *ProxyLogger, request *ParsedHTTPRequest) int {

	// log := logger.WithExtension(": docker-local: responds")

	_, ok, err := h.parseURLPath(request.Path)
	if err != nil || !ok {
		return -1
		// return false
	}

	return 0
}

func (h *HTTPDockerLocalHandler) ProcessRequest(
	logger *ProxyLogger,
	request *ParsedHTTPRequest,
	prevTargetConn io.ReadWriteCloser,
	prevTargetID string) (targetConn io.ReadWriteCloser, targetID string, err error) {

	log := logger.WithExtension(": docker-local: process-request")
	fmt := log.E

	ok := false

	log.Debug("got HTTP request:", request)

	// parse url
	request.Path, ok, err = h.parseURLPath(request.Path)
	if !ok {
		if err != nil {
			err = fmt.Errorf("invalid path: %w", err)
		}
		if prevTargetConn != nil {
			prevTargetConn.Close()
		}
		return
	}

	targetID = "docker:local"

	if prevTargetID == targetID && prevTargetConn != nil {
		targetConn = prevTargetConn
	} else {
		log.Info("connecting to local docker")
		targetConn, err = net.Dial("unix", "/var/run/docker.sock")
		// log.Tracef("host-resolver: connection %v, err: %v", targetConn, err)
		if err != nil {
			err = fmt.Errorf("remote connection to local docker failed: %w", err)
			if prevTargetConn != nil {
				prevTargetConn.Close()
			}
			return
		}
	}

	return
}

func (h *HTTPDockerLocalHandler) ProcessResponse(logger *ProxyLogger, response *ParsedHTTPResponse) (err error) {

	log := logger.WithExtension(": docker-local: process-response")
	// fmt := log.E

	log.Trace("got response from docker:", response)

	return
}

func (h *HTTPDockerLocalHandler) ResponseTransferred(logger *ProxyLogger, request *ParsedHTTPRequest, response *ParsedHTTPResponse) {

	log := logger.WithExtension(": docker-local: response-transferred")
	// fmt := log.E

	log.Trace("response transferred:", response.Short())

	return
}

func (h *HTTPDockerLocalHandler) parseURLPath(path string) (pathRewrite string, ok bool, err error) {

	// /docker:local/...

	pathRewrite = path // defaults to same path, alternative default: return empty string if no rewrite happens

	if !strings.HasPrefix(path, "/docker:local/") {
		// err = fmt.Errorf("parse-url-path: not an error, skipping request")
		return
	}

	ok = true

	pathParts := strings.SplitN(path, "/", 3)

	pathRewrite = "/"

	if len(pathParts) > 2 {
		pathRewrite += pathParts[2]
	}

	return
}
