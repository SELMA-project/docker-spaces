package main

import (
	"fmt"
	"io"
	"net"
	"strings"
)

type DockerTargetResolver struct {
}

func (r *DockerTargetResolver) parseURLPath(path string) (pathRewrite string, err error) {

	// /host:<host>:<port>/...

	pathRewrite = path // defaults to same path, alternative default: return empty string if no rewrite happens

	if !strings.HasPrefix(path, "/docker:local/") {
		// err = fmt.Errorf("parse-url-path: invalid dynamic run path") // TODO: how to call this
		err = fmt.Errorf("parse-url-path: not an error, skipping request")
		return
	}

	pathParts := strings.SplitN(path, "/", 3)

	pathRewrite = "/"

	if len(pathParts) > 2 {
		pathRewrite += pathParts[2]
	}

	return
}

func (r *DockerTargetResolver) Resolve(buff []byte) (target ResolvedTarget, err error) {

	request, err := ParseHTTPRequest(buff)
	if request == nil {
		return
	}

	target, err = r.ResolveHTTPRequest(request)

	return
}

func (r *DockerTargetResolver) ResolveHTTPRequest(request *ParsedHTTPRequest) (target ResolvedTarget, err error) {

	log.Debug("docker-resolver: got HTTP request:", request.Method, request.Path, request.Version)
	// log.Debug("docker-resolver: got HTTP Headers", request.Headers)

	// request.Path, err = r.parseURLPath(request.Path)
	_, err = r.parseURLPath(request.Path)
	if err != nil {
		err = fmt.Errorf("docker-resolver: invalid path: %w", err)
		return
	}
	log.Trace("docker-resolver: request path is now:", request.Path)

	// log.Trace("docker-resolver: request:", string(request.Data(true, false, true)))

	target = &ResolvedDockerTarget{BasicResolvedTarget{Address: "docker.local", Data: request.Data(true, false, false), ActivityCallback: nil, ClosedCallback: nil}}

	return
}

type ResolvedDockerTarget struct {
	BasicResolvedTarget
}

func (t *ResolvedDockerTarget) parseURLPath(path string) (pathRewrite string, err error) {

	// /host:<host>:<port>/...

	pathRewrite = path // defaults to same path, alternative default: return empty string if no rewrite happens

	if !strings.HasPrefix(path, "/docker:local/") {
		// err = fmt.Errorf("parse-url-path: invalid dynamic run path") // TODO: how to call this
		err = fmt.Errorf("parse-url-path: not an error, skipping request")
		return
	}

	pathParts := strings.SplitN(path, "/", 3)

	pathRewrite = "/"

	if len(pathParts) > 2 {
		pathRewrite += pathParts[2]
	}

	return
}

func (t *ResolvedDockerTarget) WrapProxyConnection(conn io.ReadWriter) io.ReadWriter {

	log.Debug("docker-resolver-target: wrap proxy connection")

	conn = NewHTTPRewriteRequestWrapper(conn, func(request *ParsedHTTPRequest) (err error) {
		request.Path, err = t.parseURLPath(request.Path)
		// log.Trace("docker-resolver: wrapped proxy conn request:", string(request.Data(true, false, true)))
		return
	})

	return conn
}

func (t *ResolvedDockerTarget) Connect() (conn io.ReadWriteCloser, err error) {

	conn, err = net.Dial("unix", "/var/run/docker.sock")

	// conn = NewReadWriterLogger(conn, "DOCKER")

	conn = NewHTTPRewriteResponseWrapper(conn, func(response *ParsedHTTPResponse) (err error) {
		// log.Trace("docker-resolver: connect: got response:", response)
		// request.Path, err = t.parseURLPath(request.Path)
		// log.Trace("docker-resolver: wrapped docker conn response:", string(response.Data(true, false, true)))
		return
	})

	return
}
