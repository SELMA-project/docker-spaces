package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func parseHTTPHeaders(lines []string) (headers http.Header, err error) {

	headers = http.Header{}

	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		kv := strings.SplitN(line, ":", 2)
		if len(kv) < 2 {
			err = fmt.Errorf("http-parser: HTTP header parser error: malformed header at line %d", i)
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		headers.Add(key, value)
	}

	return
}

type ParsedHTTPResponse struct {
	Version      string
	VersionMajor int
	VersionMinor int
	StatusCode   int
	Status       string
	Headers      http.Header
	// internal
	buff             []byte
	statusLineLength int
	endOfHeaders     int
}

func ParseHTTPResponse(buff []byte) (r *ParsedHTTPResponse, err error) {

	statusLineLength := bytes.Index(buff, []byte("\r\n"))
	if statusLineLength == -1 {
		return // no error, need more data
	}

	// minimum string for HTTP (length 13): HTTP/2 200 OK
	if statusLineLength < 13 {
		err = fmt.Errorf("parse-http-response: input packet too short for HTTP response (%d), got string: '%s'", statusLineLength, string(buff[:statusLineLength]))
		return
	}

	parts := strings.SplitN(string(buff[:statusLineLength]), " ", 3)

	if len(parts) < 3 {
		err = fmt.Errorf("parse-http-response: input packet does not look like HTTP response, has %d parts instead of required at least 3 parts: %s",
			len(parts), string(buff[:statusLineLength]))
		return
	}

	version := parts[0]
	statusCode, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}
	status := parts[2]

	major, minor, httpOK := http.ParseHTTPVersion(version)
	if !httpOK {
		err = fmt.Errorf("parse-http-response: input packet does not contain valid HTTP request, invalid version string")
		return
	}

	// search for end of headers position
	endOfHeaders := bytes.Index(buff, []byte("\r\n\r\n"))
	if endOfHeaders == -1 {
		return
	}

	headers, err := parseHTTPHeaders(strings.Split(string(buff[statusLineLength:endOfHeaders]), "\r\n"))
	if err != nil {
		return
	}

	r = &ParsedHTTPResponse{Version: version, VersionMajor: major, VersionMinor: minor, StatusCode: statusCode, Status: status, Headers: headers,
		buff: buff, statusLineLength: statusLineLength, endOfHeaders: endOfHeaders}

	return
}

func (r *ParsedHTTPResponse) HeaderSize() int {
	return r.endOfHeaders + 4 // + \r\n\r\n
}

func (r *ParsedHTTPResponse) Data(versionUpdated bool, statusUpdated bool, headersUpdated bool) []byte {
	if !versionUpdated && !statusUpdated && !headersUpdated {
		return r.buff
	}

	var out bytes.Buffer

	if statusUpdated || versionUpdated {
		versionString := r.Version
		if versionUpdated {
			versionString = fmt.Sprintf("HTTP/%d.%d", r.VersionMajor, r.VersionMinor)
		}

		out.Write([]byte(versionString + " " + strconv.Itoa(r.StatusCode) + " " + r.Status + "\r\n"))
	} else {
		out.Write(r.buff[:r.statusLineLength+2]) // +2 to include \r\n
	}

	if headersUpdated {
		r.Headers.Write(&out)
	} else {
		out.Write(r.buff[r.statusLineLength+2 : r.endOfHeaders+2]) // +2 to include \r\n
	}

	out.Write(r.buff[r.endOfHeaders+2 : len(r.buff)]) // remaining content

	return out.Bytes()
}

func (r *ParsedHTTPResponse) Write(out io.Writer, versionUpdated bool, statusUpdated bool, headersUpdated bool) (err error) {
	if !versionUpdated && !statusUpdated && !headersUpdated {
		_, err = out.Write(r.buff)
		return
	}

	if statusUpdated || versionUpdated {
		versionString := r.Version
		if versionUpdated {
			versionString = fmt.Sprintf("HTTP/%d.%d", r.VersionMajor, r.VersionMinor)
		}

		_, err = out.Write([]byte(versionString + " " + strconv.Itoa(r.StatusCode) + " " + r.Status + "\r\n"))
		if err != nil {
			return
		}
	} else {
		_, err = out.Write(r.buff[:r.statusLineLength+2]) // +2 to include \r\n
		if err != nil {
			return
		}
	}

	if headersUpdated {
		r.Headers.Write(out)
	} else {
		_, err = out.Write(r.buff[r.statusLineLength+2 : r.endOfHeaders+2]) // +2 to include \r\n
		if err != nil {
			return
		}
	}

	_, err = out.Write(r.buff[r.endOfHeaders+2 : len(r.buff)]) // remaining content
	if err != nil {
		return
	}

	return
}

func (r *ParsedHTTPResponse) WriteHeader(out io.Writer, versionUpdated bool, statusUpdated bool, headersUpdated bool) (err error) {
	if !versionUpdated && !statusUpdated && !headersUpdated {
		_, err = out.Write(r.buff)
		return
	}

	if statusUpdated || versionUpdated {
		versionString := r.Version
		if versionUpdated {
			versionString = fmt.Sprintf("HTTP/%d.%d", r.VersionMajor, r.VersionMinor)
		}

		_, err = out.Write([]byte(versionString + " " + strconv.Itoa(r.StatusCode) + " " + r.Status + "\r\n"))
		if err != nil {
			return
		}
	} else {
		_, err = out.Write(r.buff[:r.statusLineLength+2]) // +2 to include \r\n
		if err != nil {
			return
		}
	}

	if headersUpdated {
		r.Headers.Write(out)
	} else {
		_, err = out.Write(r.buff[r.statusLineLength+2 : r.endOfHeaders+2]) // +2 to include \r\n
		if err != nil {
			return
		}
	}

	_, err = out.Write([]byte("\r\n"))
	if err != nil {
		return
	}

	// _, err = out.Write(r.buff[r.endOfHeaders+2 : len(r.buff)]) // remaining content
	// if err != nil {
	// 	return
	// }

	return
}

type ParsedHTTPRequest struct {
	Method       string
	Path         string
	Query        string
	Version      string
	VersionMajor int
	VersionMinor int
	Headers      http.Header
	// internal
	buff              []byte
	requestLineLength int
	endOfHeaders      int
	response          bool
}

func ParseHTTPRequest(buff []byte) (r *ParsedHTTPRequest, err error) {

	requestLineLength := bytes.Index(buff, []byte("\r\n"))
	if requestLineLength == -1 {
		return // no error, need more data
	}

	// minimum string for HTTP (length 12): GET / HTTP/2
	if requestLineLength < 12 {
		err = fmt.Errorf("parse-http-request: input packet too short for HTTP request (%d), got string: '%s'", requestLineLength, string(buff[:requestLineLength]))
		return
	}

	parts := strings.Split(string(buff[:requestLineLength]), " ")

	if len(parts) != 3 {
		err = fmt.Errorf("parse-http-request: input packet does not look like HTTP request, has %d parts instead of required 3 parts", len(parts))
		return
	}

	method := parts[0]
	pathquery := strings.SplitN(parts[1], "?", 2)
	path := pathquery[0]
	query := ""
	if len(pathquery) > 1 {
		query = "?" + pathquery[1]
	}
	version := parts[2]

	major, minor, httpOK := http.ParseHTTPVersion(version)
	if !httpOK {
		err = fmt.Errorf("parse-http-request: input packet does not contain valid HTTP request, invalid version string")
		return
	}

	// search for end of headers position
	endOfHeaders := bytes.Index(buff, []byte("\r\n\r\n"))
	if endOfHeaders == -1 {
		return
	}

	headers, err := parseHTTPHeaders(strings.Split(string(buff[requestLineLength:endOfHeaders]), "\r\n"))
	if err != nil {
		return
	}

	r = &ParsedHTTPRequest{Method: method, Path: path, Query: query, Version: version, VersionMajor: major, VersionMinor: minor, Headers: headers,
		buff: buff, requestLineLength: requestLineLength, endOfHeaders: endOfHeaders}

	return
}

func (r *ParsedHTTPRequest) HeaderSize() int {
	return r.endOfHeaders + 4 // + \r\n\r\n
}

func (r *ParsedHTTPRequest) Data(pathUpdated bool, versionUpdated bool, headersUpdated bool) []byte {
	if !pathUpdated && !versionUpdated && !headersUpdated {
		return r.buff
	}

	var out bytes.Buffer

	if pathUpdated || versionUpdated {
		versionString := r.Version
		if versionUpdated {
			versionString = fmt.Sprintf("HTTP/%d.%d", r.VersionMajor, r.VersionMinor)
		}

		out.Write([]byte(r.Method + " " + r.Path + r.Query + " " + versionString + "\r\n"))
	} else {
		out.Write(r.buff[:r.requestLineLength+2]) // +2 to include \r\n
	}

	if headersUpdated {
		r.Headers.Write(&out)
	} else {
		out.Write(r.buff[r.requestLineLength+2 : r.endOfHeaders+2]) // +2 to include \r\n
	}

	out.Write(r.buff[r.endOfHeaders+2 : len(r.buff)]) // remaining content

	return out.Bytes()
}

func (r *ParsedHTTPRequest) Write(out io.Writer, pathUpdated bool, versionUpdated bool, headersUpdated bool) (err error) {
	if !pathUpdated && !versionUpdated && !headersUpdated {
		_, err = out.Write(r.buff)
		return
	}

	if pathUpdated || versionUpdated {
		versionString := r.Version
		if versionUpdated {
			versionString = fmt.Sprintf("HTTP/%d.%d", r.VersionMajor, r.VersionMinor)
		}

		_, err = out.Write([]byte(r.Method + " " + r.Path + r.Query + " " + versionString + "\r\n"))
		if err != nil {
			return
		}
	} else {
		_, err = out.Write(r.buff[:r.requestLineLength+2]) // +2 to include \r\n
		if err != nil {
			return
		}
	}

	if headersUpdated {
		r.Headers.Write(out)
	} else {
		_, err = out.Write(r.buff[r.requestLineLength+2 : r.endOfHeaders+2]) // +2 to include \r\n
		if err != nil {
			return
		}
	}

	_, err = out.Write(r.buff[r.endOfHeaders+2 : len(r.buff)]) // remaining content
	if err != nil {
		return
	}

	return
}

func (r *ParsedHTTPRequest) WriteHeader(out io.Writer, pathUpdated bool, versionUpdated bool, headersUpdated bool) (err error) {
	if !pathUpdated && !versionUpdated && !headersUpdated {
		_, err = out.Write(r.buff)
		return
	}

	if pathUpdated || versionUpdated {
		versionString := r.Version
		if versionUpdated {
			versionString = fmt.Sprintf("HTTP/%d.%d", r.VersionMajor, r.VersionMinor)
		}

		_, err = out.Write([]byte(r.Method + " " + r.Path + r.Query + " " + versionString + "\r\n"))
		if err != nil {
			return
		}
	} else {
		_, err = out.Write(r.buff[:r.requestLineLength+2]) // +2 to include \r\n
		if err != nil {
			return
		}
	}

	if headersUpdated {
		r.Headers.Write(out)
	} else {
		_, err = out.Write(r.buff[r.requestLineLength+2 : r.endOfHeaders+2]) // +2 to include \r\n
		if err != nil {
			return
		}
	}

	_, err = out.Write([]byte("\r\n"))
	if err != nil {
		return
	}

	// _, err = out.Write(r.buff[r.endOfHeaders+2 : len(r.buff)]) // remaining content
	// if err != nil {
	// 	return
	// }

	return
}
