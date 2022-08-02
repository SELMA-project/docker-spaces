package main

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
)

type HTTPRequestTargetResolver interface {
	ResolveHTTPRequest(*ParsedHTTPRequest) (ResolvedTarget, error)
}

type HTTPProtocolTargetResolver struct {
	resolvers []HTTPRequestTargetResolver
}

func NewHTTPProtocolTargetResolver(requestResolvers ...HTTPRequestTargetResolver) *HTTPProtocolTargetResolver {
	return &HTTPProtocolTargetResolver{resolvers: requestResolvers}
}

func (r *HTTPProtocolTargetResolver) Resolve(buff []byte) (target ResolvedTarget, err error) {

	request, err := ParseHTTPRequest(buff)
	if request == nil {
		return
	}

	for _, resolver := range r.resolvers {
		target, err = resolver.ResolveHTTPRequest(request)
		if err == nil && target != nil {
			log.Trace("http-resolver: remote address:", target.RemoteAddress())
			return
		}
	}

	if err != nil {
		err = fmt.Errorf("http-protocol-target-resolver: unable to resolve target: %w", err)
	} else {
		err = fmt.Errorf("http-protocol-target-resolver: unable to resolve target")
	}

	return
}

type HTTPRequestRewriteFunc func(*ParsedHTTPRequest) error

type HTTPReaderState int

const (
	HTTPReaderStateRequestHead HTTPReaderState = iota
	HTTPReaderStateBody
	HTTPReaderStateChunkedBody
	HTTPReaderStateUpgraded
)

func (s HTTPReaderState) String() string {
	return [...]string{"HTTPReaderStateRequestHead", "HTTPReaderStateBody", "HTTPReaderStateChunkedBody", "HTTPReaderStateUpgraded"}[s]
}

type HTTPRequestURLRewriterReadWriteCloserWrapper struct {
	inner         io.ReadWriter
	rewriteFunc   HTTPRequestRewriteFunc
	input         bytes.Buffer
	output        bytes.Buffer
	buff          [0xfff]byte // 4KB
	state         HTTPReaderState
	bodyToRead    int
	lastBodyParse int
	finalChunk    bool
}

func NewHTTPRequestWrapper(inner io.ReadWriter, rewriteFunc HTTPRequestRewriteFunc) *HTTPRequestURLRewriterReadWriteCloserWrapper {
	return &HTTPRequestURLRewriterReadWriteCloserWrapper{inner: inner, rewriteFunc: rewriteFunc}
}

func (r *HTTPRequestURLRewriterReadWriteCloserWrapper) Unwrap() io.ReadWriteCloser {
	return r.inner.(io.ReadWriteCloser)
}

func (r *HTTPRequestURLRewriterReadWriteCloserWrapper) Read(buff []byte) (n int, err error) {

	// TODO: handle upgraded connections
	// if r.state == HTTPReaderStateUpgraded {
	// 	n, err = r.inner.Read(buff)
	// 	return
	// }

	// NOTE: inner.Read() can return 0 bytes read

	var in int

	if r.state == HTTPReaderStateRequestHead {

		// read more data into input only if input is empty or the buffer is the same from previous call (determined by input buffer size)
		if r.input.Len() == 0 || r.lastBodyParse == r.input.Len() {
			// read from inner into input buffer
			in, err = r.inner.Read(r.buff[:])
			if err != nil {
				err = fmt.Errorf("http-request-read: error reading from source: %w", err)
				return
			}

			if in == 0 {
				return
			}

			// TODO: add written byte count check
			_, err = r.input.Write(r.buff[:in])
			if err != nil {
				err = fmt.Errorf("http-request-read: error writing to internal buffer: %w", err)
				return
			}
		}

		// parse HTTP header in input buffer

		var request *ParsedHTTPRequest

		request, err = ParseHTTPRequest(r.input.Bytes())
		if err != nil {
			err = fmt.Errorf("http-request-reader: error parsing HTTP request: %w", err)
			log.Trace("request:", string(r.input.Bytes()))
			return
		}

		if request == nil {
			// not yet enough data
			r.lastBodyParse = r.input.Len()
			return
		}

		// // TODO: handle connection upgrade
		// upgrade := request.Headers.Get("Upgrade")
		// if len(upgrade) > 0 {
		// }

		transferEncoding := request.Headers.Get("Transfer-Encoding")
		if transferEncoding == "chunked" {
			r.state = HTTPReaderStateChunkedBody
			r.bodyToRead = 0
		} else {
			r.state = HTTPReaderStateBody
			contentLength := request.Headers.Get("Content-Length")
			if len(contentLength) == 0 {
				// err = fmt.Errorf("http-request-reader: content length is empty")
				r.bodyToRead = 0
			} else if r.bodyToRead, err = strconv.Atoi(contentLength); err != nil {
				err = fmt.Errorf("http-request-reader: invalid content length %s: %w", contentLength, err)
				return
			}
		}

		if r.rewriteFunc != nil {
			err = r.rewriteFunc(request)
			if err != nil {
				err = fmt.Errorf("http-request-reader: request rewrite func error: %w", err)
				return
			}
		}

		// write new header to ouput buffer
		err = request.WriteHeader(&r.output, true, true, true)
		if err != nil {
			err = fmt.Errorf("http-request-reader: error writing header: %w", err)
			return
		}

		// advance input buffer past the old header
		r.input.Next(request.HeaderSize()) // skip header bytes

		if r.bodyToRead == 0 {
			r.state = HTTPReaderStateRequestHead
			r.lastBodyParse = 0
		}
	}

	// return data from output buffer
	if r.output.Len() > 0 {
		n, err = r.output.Read(buff)
		return
	}

	// NOTE: if inner Read() returns 0 bytes, then return to caller

	if r.state == HTTPReaderStateBody {

		// inner -> input -> output -> return to caller
		// TODO: aggregate all data into output and then write?

		// TODO: in case the buff is larger than n, allow to read from next source if there are data available, i.e., from input and then from inner

		// read from input buffer
		if r.input.Len() > 0 {
			if r.bodyToRead > len(buff) {
				n, err = r.input.Read(buff)
			} else {
				n, err = r.input.Read(buff[:r.bodyToRead]) // so it won't try to read bytes past the start of the next request
			}
			r.bodyToRead -= n
			if r.bodyToRead == 0 {
				r.state = HTTPReaderStateRequestHead
				r.lastBodyParse = 0
			}
			return
		}

		// read directly from inner
		if r.bodyToRead > len(buff) {
			n, err = r.inner.Read(buff)
		} else {
			n, err = r.inner.Read(buff[:r.bodyToRead]) // so it won't try to read bytes past the start of the next  request
		}
		r.bodyToRead -= n
		if r.bodyToRead == 0 {
			r.state = HTTPReaderStateRequestHead
			r.lastBodyParse = 0
		}

		return

	} else if r.state == HTTPReaderStateChunkedBody {

		// switched from head just now, then output will contain the head

		// return data from output buffer
		if r.output.Len() > 0 {
			n, err = r.output.Read(buff)
			return
		}

		// parse at input buffer, TODO: output may use as an returned data aggregator

		// for r.output.Len() >= len(buff) {
		if true {

			if r.bodyToRead < 0 {
				err = fmt.Errorf("http-request-read: unexpected error: body to read is below 0: %d", r.bodyToRead)
				return
			}

			if r.bodyToRead == 0 {
				// we should be at the beginning of a new chunk
				// read from inner into input buffer
				in, err = r.inner.Read(r.buff[:])
				if err != nil {
					err = fmt.Errorf("http-request-read: error reading from source: %w", err)
					return
				}

				if in == 0 {
					return
				}

				// TODO: add written byte count check
				_, err = r.input.Write(r.buff[:in])
				if err != nil {
					err = fmt.Errorf("http-request-read: error writing to internal buffer: %w", err)
					return
				}
			}

			if r.input.Len() > 0 && r.bodyToRead == 0 {
				// we should be at the beginning of a new chunk and already have some data into input buffer
				b := r.input.Bytes()
				var size, offset int64
				size, offset, err = parseHTTPChunkHead(b)
				if err != nil {
					err = fmt.Errorf("http-request-read: error parsing http chunk header: %w", err)
					return
				}

				if size == 0 {
					// final terminating chunk
					if int64(len(b)) >= offset+2 /* include terminating \r\n */ {
						n, err = r.input.Read(buff[:offset+2])
						if err != nil {
							return
						}
						// reset to wait for next request
						r.state = HTTPReaderStateRequestHead
						r.lastBodyParse = 0
						// r.finalChunk = false
						return
					} else {
						r.finalChunk = true
					}
				}

				r.bodyToRead = int(size + 2) // + \r\n
				_, err = r.output.Write(b[:offset])
				if err != nil {
					err = fmt.Errorf("http-request-read: chunked body: error writing to output buffer: %w", err)
					return
				}
				r.input.Next(int(offset)) // chunked head is written to output, so advance input buffer pointer
			}

			if r.output.Len() > 0 {
				n, err = r.output.Read(buff)
				return
			}

			if r.bodyToRead > 0 {
				if r.input.Len() > 0 {
					if len(buff) > r.bodyToRead {
						n, err = r.input.Read(buff[:r.bodyToRead])
					} else {
						n, err = r.input.Read(buff)
					}
				} else {
					if len(buff) > r.bodyToRead {
						n, err = r.inner.Read(buff[:r.bodyToRead])
					} else {
						n, err = r.inner.Read(buff)
					}
				}
				r.bodyToRead -= n
				if r.bodyToRead == 0 && r.finalChunk {
					r.state = HTTPReaderStateRequestHead
					r.lastBodyParse = 0
					r.finalChunk = false
					return
				}
				return
			}

		}
	}

	// write output buffer
	if r.output.Len() > 0 {
		n, err = r.output.Read(buff)
		return
	}

	return
}

func (r *HTTPRequestURLRewriterReadWriteCloserWrapper) Write(buff []byte) (n int, err error) {
	return r.inner.Write(buff)
}

// func (r *HTTPRequestURLRewriterReadWriteCloserWrapper) Close() error {
// 	return r.inner.Close()
// }

func parseHTTPChunkHead(buff []byte) (dataSize int64, dataOffset int64, err error) {

	if len(buff) < 3 {
		err = fmt.Errorf("parse-http-chunk-head: not enough data")
		return
	}

	lineLength := bytes.Index(buff, []byte("\r\n"))
	if lineLength == -1 {
		err = fmt.Errorf("parse-http-chunk-head: invalid chunk head", err)
		return
	}
	dataSize, err = strconv.ParseInt(string(buff[:lineLength]), 16, 64)
	if err != nil {
		err = fmt.Errorf("parse-http-chunk-head: unable to parse chunk length: %w", err)
		return
	}

	dataOffset = int64(lineLength + 2)

	return
}
