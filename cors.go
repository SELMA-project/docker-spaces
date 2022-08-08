package main

import (
	"bytes"
	"io"
)

type HTTPCORSInject struct {
	inner         io.ReadWriteCloser
	headerWritten bool
	input         bytes.Buffer
	output        bytes.Buffer
	buff          [0xfff]byte
}

func NewHTTPCORSInject(conn io.ReadWriter) io.ReadWriter {
	return NewHTTPRewriteResponseWrapper(conn, func(response *ParsedHTTPResponse) (err error) {

		response.Headers.Add("Access-Control-Request-Headers", "Content-Type")
		response.Headers.Add("Access-Control-Allow-Headers", "Content-Type")
		response.Headers.Add("Access-Control-Allow-Origin", "*")
		response.Headers.Add("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE")
		response.Headers.Add("Access-Control-Allow-Credentials", "true")

		log.Trace("http-cors-inject: parsed response headers:", response.Headers)

		return
	})
}

// func NewHTTPCORSInject(conn io.ReadWriteCloser) *HTTPCORSInject {
// 	return &HTTPCORSInject{inner: conn}
// }

func (r *HTTPCORSInject) Read(buff []byte) (n int, err error) {

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

	// add CORS headers
	res.Headers.Add("Access-Control-Request-Headers", "Content-Type")
	res.Headers.Add("Access-Control-Allow-Headers", "Content-Type")
	res.Headers.Add("Access-Control-Allow-Origin", "*")
	res.Headers.Add("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE")
	res.Headers.Add("Access-Control-Allow-Credentials", "true")

	log.Trace("http-cors-inject: parsed response headers:", res.Headers)

	err = res.Write(&r.output, false, false, true)
	if err != nil {
		return
	}

	r.headerWritten = true

	n, err = r.output.Read(buff)

	return
}

func (r *HTTPCORSInject) Write(buff []byte) (n int, err error) {
	return r.inner.Write(buff)
}

func (r *HTTPCORSInject) Close() error {
	return r.inner.Close()
}
