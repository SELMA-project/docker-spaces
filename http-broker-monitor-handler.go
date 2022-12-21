package main

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type HTTPBrokerMonitorHandler struct {
	broker *Broker
}

func (h *HTTPBrokerMonitorHandler) String() string {
	return "HTTPBrokerMonitorHandler"
}

func (h *HTTPBrokerMonitorHandler) Closed(logger *ProxyLogger, request *ParsedHTTPRequest) {

	log := logger.WithExtension(": monitor-broker: closed")

	log.Trace("connection closed")

	return
}

func (h *HTTPBrokerMonitorHandler) RespondsAtLevel(logger *ProxyLogger, request *ParsedHTTPRequest) int {

	// log := logger.WithExtension(": monitor-broker: responds")

	_, ok, err := h.parseURLPath(request.Path)
	if err != nil || !ok {
		return -1
		// return false
	}

	return 0
}

type HTTPBrokerMonitorResponse struct {
	b bytes.Buffer // NOTE: if Buffer is exposed, then anyone, who type casts it to bytes.Buffer will avoid our custom Read/Write methods
	// headers http.Header
	immutable bool
}

func (r *HTTPBrokerMonitorResponse) Close() error {
	return nil
}

func (r *HTTPBrokerMonitorResponse) Read(buff []byte) (n int, err error) {
	n, err = r.b.Read(buff)
	return
}

func (r *HTTPBrokerMonitorResponse) Write(buff []byte) (n int, err error) {
	if r.immutable {
		return len(buff), nil // simulate everything ok
	}
	return r.b.Write(buff)
}

func (h *HTTPBrokerMonitorHandler) ProcessRequest(
	logger *ProxyLogger,
	request *ParsedHTTPRequest,
	prevTargetConn io.ReadWriteCloser,
	prevTargetID string) (targetConn io.ReadWriteCloser, targetID string, err error) {

	log := logger.WithExtension(": monitor-broker: process-request")
	fmt := log.E

	if prevTargetConn != nil {
		prevTargetConn.Close()
	}

	ok := false

	log.Debug("got HTTP request:", request)

	// parse url
	request.Path, ok, err = h.parseURLPath(request.Path)
	if !ok {
		if err != nil {
			err = fmt.Errorf("invalid path: %w", err)
		}
		return
	}

	targetID = "monitor:broker"

	response := &HTTPBrokerMonitorResponse{}

	headers := http.Header{}
	content := &bytes.Buffer{}

	// content.Write([]byte("broker state:\n"))
	h.broker.State = h.broker.JSON()
	content.Write(h.broker.State)

	headers.Add("Content-Length", strconv.Itoa(content.Len()))
	headers.Add("Content-Type", "application/json")
	headers.Add("Host", "localhost")

	response.Write([]byte("HTTP/1.1 200 OK\r\n"))
	headers.Write(response)
	response.Write([]byte{13, 10})
	response.Write(content.Bytes())

	response.immutable = true

	targetConn = response

	return
}

func (h *HTTPBrokerMonitorHandler) ProcessResponse(logger *ProxyLogger, response *ParsedHTTPResponse) (err error) {

	log := logger.WithExtension(": monitor-broker: process-response")
	// fmt := log.E

	log.Trace("got response:", response)

	return
}

func (h *HTTPBrokerMonitorHandler) parseURLPath(path string) (pathRewrite string, ok bool, err error) {

	// /docker:local/...

	pathRewrite = path // defaults to same path, alternative default: return empty string if no rewrite happens

	if !strings.HasPrefix(path, "/monitor:broker/") && path != "/monitor:broker" {
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
