package main

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
)

/*
	Read() - read data from source into internal buffer
	ParseRequest() / ParseResponse() - parse request or response at the beginning of internal buffer
	  returns nil request/response in two cases: not expecting a head; expecting head, but got incomplete head, need more data (but if no head detected, then error)
	Write() - write internal buffer to the target up to the end of the current message or the whole buffer

	typical usage:
	loop {
		Read()
		loop {
			req/resp = ParseRequest() / ParseResponse()
			handle/process req/resp
			Write()
		}
	}
*/

type HTTPPipe struct {
	log         *ProxyLogger
	source      io.Reader
	target      io.Writer     // may not be connected
	buff        []byte        // used to copy chunks to internal buffer, to be reused
	input       *bytes.Buffer // internal buffer
	state       HTTPReaderState
	bodyToRead  int // TODO: merge both into one
	bodyToWrite int
	writeHead   HTTPHeaderWriter
}

type HTTPHeaderWriter interface {
	// HeaderSize() int
	WriteHead(io.Writer) error
}

func NewHTTPPipe(logger *ProxyLogger, source io.Reader, target io.Writer) *HTTPPipe {
	return &HTTPPipe{logger.Derive().SetPrefix("http-pipe"), source, target, make([]byte, 0x1000), &bytes.Buffer{}, HTTPReaderStateHead, 0, 0, nil}
}

func (p *HTTPPipe) SetTarget(target io.Writer) {
	p.target = target
}

func (p *HTTPPipe) SetSource(source io.Reader) {
	p.source = source
}

func (p *HTTPPipe) Logger() *ProxyLogger {
	return p.log
}

func (p *HTTPPipe) ExpectHead() bool {
	return p.state == HTTPReaderStateHead || p.state == HTTPReaderStateIncompleteHead
}

func (p *HTTPPipe) StartHead() bool {
	return p.state == HTTPReaderStateHead
}

func (p *HTTPPipe) Upgraded() bool {
	return p.state == HTTPReaderStateUpgraded
}

func (p *HTTPPipe) Upgrade() {
	p.state = HTTPReaderStateUpgraded
}

// func (p *HTTPPipe) Empty() bool {
// 	return p.input == nil || p.input.Len() < 10 // hack, TODO: either reasonable number or introduce some flag
// }

// simple reads some data from source into internal buffer
func (p *HTTPPipe) Read() (err error) {

	log := p.log.WithExtension("-read")
	fmt := log.E

	if p.source == nil {
		err = fmt.Errorf("no connection")
		return
	}

	log.Trace("about to read from source, state:", p.state, "write head:", p.writeHead != nil, "to write:", p.bodyToWrite)

	n, err := p.source.Read(p.buff)
	if err != nil {
		err = fmt.Errorf("read from source error: %w", err)
		return
	}

	if n == 0 {
		log.Trace("need more data")
		return
	}

	log.Trace("read from source:", n)
	// log.Tracef("read data [%d]: %q", n, string(p.buff[:n]))

	nw, err := p.input.Write(p.buff[:n])
	if err != nil {
		err = fmt.Errorf("write to input buffer error: %w", err)
		return
	}

	log.Trace("written to input buffer", nw)
	// log.Trace("buff prefix after read", p.bodyToRead, p.bodyToWrite, p.state, p.input.Bytes())

	if n != nw {
		err = fmt.Errorf("read and write byte count mismatch, read %d, written %d", n, nw)
		// TODO: retry to write rest of data?
	}

	return
}

func (p *HTTPPipe) ParseRequest() (request *ParsedHTTPRequest, err error) {

	log := p.log.WithExtension("-parse-request")
	fmt := log.E

	log.Trace("state:", p.state, p.bodyToRead)

	if p.state != HTTPReaderStateHead && p.state != HTTPReaderStateIncompleteHead {
		// we do not expect header data
		return
	}

	// log.Trace("got data:", string(p.input.Bytes()), p.input.Bytes(), p.input.Len())
	log.Trace("got data size:", p.input.Len())
	// log.Trace("got data:", string(p.input.Bytes()))
	request, err = ParseHTTPRequest(p.input.Bytes())
	if err != nil {
		err = fmt.Errorf("error parsing HTTP request: %w", err)
		// log.Trace("request:", string(r.input.Bytes()))
		return
	}

	if request == nil {
		// not enough data, caller has to provide more data by calling Read()
		log.Trace("incomplete header, more data needed")
		p.state = HTTPReaderStateIncompleteHead
		return
	}

	contentLength := request.Headers.Get("Content-Length")
	transferEncoding := request.Headers.Get("Transfer-Encoding")
	// https://stackoverflow.com/a/11375745
	if len(contentLength) == 0 && request.VersionMajor == 1 && request.VersionMinor == 1 || transferEncoding == "chunked" {
		p.state = HTTPReaderStateChunkedBody
		p.bodyToRead = 0
		// } else if request.StatusCode == 204 {
		// 	p.state = HTTPReaderStateHead
		// 	p.bodyToRead = 0
		// } else if len(contentLength) == 0 {
		// 	p.state = HTTPReaderStateChunkedBody
		// 	p.bodyToRead = 0
	} else {
		p.state = HTTPReaderStateBody
		// contentLength := response.Headers.Get("Content-Length")
		if len(contentLength) == 0 {
			// err = fmt.Errorf("http-pipe-parse-request: content length is empty")
			// TODO: when the stream closes
			p.bodyToRead = 0
		} else if p.bodyToRead, err = strconv.Atoi(contentLength); err != nil {
			err = fmt.Errorf("invalid content length %s: %w", contentLength, err)
			return
		} else if p.bodyToRead == 0 {
			p.state = HTTPReaderStateHead
		}
	}

	// log.Tracef("content length: %s<  transfer encoding: %s<  state: %s", contentLength, transferEncoding, p.state)

	p.writeHead = request
	p.bodyToWrite = p.bodyToRead

	p.input.Next(request.HeaderSize()) // skip header bytes

	if p.bodyToWrite == 0 {
		p.state = HTTPReaderStateHead
	}
	// log.Trace("got request:", request)

	return
}

func (p *HTTPPipe) ParseResponse() (response *ParsedHTTPResponse, err error) {

	log := p.log.WithExtension("-parse-response")
	fmt := log.E

	log.Trace("state:", p.state)

	if p.state != HTTPReaderStateHead && p.state != HTTPReaderStateIncompleteHead {
		// we do not expect header data
		return
	}

	// log.Trace("got data:", string(p.input.Bytes()), p.input.Bytes(), p.input.Len())
	log.Trace("got data size:", p.input.Len())
	// log.Trace("response data:", string(p.input.Bytes()))
	response, err = ParseHTTPResponse(p.input.Bytes())
	if err != nil {
		err = fmt.Errorf("error parsing HTTP response: %w", err)
		// log.Trace("response:", string(r.input.Bytes()))
		return
	}

	if response == nil {
		// not enough data, caller has to provide more data by calling Read()
		log.Trace("incomplete header, more data needed")
		p.state = HTTPReaderStateIncompleteHead
		return
	}

	contentLength := response.Headers.Get("Content-Length")
	transferEncoding := response.Headers.Get("Transfer-Encoding")
	// https://stackoverflow.com/a/11375745
	if response.StatusCode == 204 && len(contentLength) == 0 && response.VersionMajor == 1 && response.VersionMinor == 1 {
		p.state = HTTPReaderStateHead
		p.bodyToRead = 0
	} else if len(contentLength) == 0 && response.VersionMajor == 1 && response.VersionMinor == 1 && transferEncoding == "chunked" {
		p.state = HTTPReaderStateChunkedBody
		p.bodyToRead = 0
	} else if response.StatusCode == 204 {
		p.state = HTTPReaderStateHead
		p.bodyToRead = 0
		// } else if len(contentLength) == 0 {
		// 	p.state = HTTPReaderStateChunkedBody
		// 	p.bodyToRead = 0
	} else {
		p.state = HTTPReaderStateBody
		// contentLength := response.Headers.Get("Content-Length")
		if len(contentLength) == 0 {
			p.state = HTTPReaderStateBodyStream
			// err = fmt.Errorf("http-pipe-parse-response: content length is empty")
			// TODO: when the stream closes
			p.bodyToRead = 0
		} else if p.bodyToRead, err = strconv.Atoi(contentLength); err != nil {
			err = fmt.Errorf("invalid content length %s: %w", contentLength, err)
			return
		} else if p.bodyToRead == 0 {
			p.state = HTTPReaderStateHead
		}
	}

	// log.Tracef("content length: %s<  transfer encoding: %s<  state: %s", contentLength, transferEncoding, p.state)

	p.writeHead = response
	p.bodyToWrite = p.bodyToRead

	log.Tracef("body to read %d, header size: %d, total buff len %d, bufflen-headersize %d", p.bodyToWrite, response.HeaderSize(), p.input.Len(), p.input.Len()-response.HeaderSize())
	p.input.Next(response.HeaderSize()) // skip header bytes

	/*
		if response.StatusCode == 101 {
			// upgraded connection
			r.state = HTTPReaderStateUpgraded
			if r.input.Len() > 0 {
				r.input.WriteTo(&r.output)
			}
			if r.output.Len() > 0 {
				n, err = r.output.Read(buff)
			}
			log.Tracef("connection upgraded")
			return
		}
	*/

	return
}

func (p *HTTPPipe) Write() (err error) {

	log := p.log.WithExtension("-write")
	fmt := log.E

	if p.writeHead != nil {
		log.Trace("writing head")
		err = p.writeHead.WriteHead(p.target)
		if err != nil {
			err = fmt.Errorf("error writing head: %w", err)
			return
		}
		p.writeHead = nil
	}

	if p.state == HTTPReaderStateBodyStream {
		err = p.Pipe()
		return
	}

	// if err != nil {
	// 	return
	// }

	if p.bodyToWrite > 0 || p.state == HTTPReaderStateChunkedBody && p.bodyToWrite == 0 {
		var n int
		var nw int
		log.Trace("state:", p.state)

		if p.state == HTTPReaderStateBody {

			// we have data expected to be written to target
			for p.input.Len() > 0 && p.bodyToWrite > 0 {
				if p.bodyToWrite >= len(p.buff) {
					n, err = p.input.Read(p.buff)
				} else {
					n, err = p.input.Read(p.buff[:p.bodyToWrite])
				}
				if err != nil {
					err = fmt.Errorf("error reading from input buffer: %w", err)
					return
				}

				if n == 0 {
					// nothing read, need more data
					return
				}

				nw, err = p.target.Write(p.buff[:n])
				if err != nil {
					err = fmt.Errorf("error writing to target: %w", err)
					return
				}
				if n != nw {
					err = fmt.Errorf("read and write byte count mismatch, read %d, written %d", n, nw)
				}
				p.bodyToWrite -= nw
			}

			if p.bodyToWrite == 0 {
				p.state = HTTPReaderStateHead // body is written, expect head at next parse
			} else if p.bodyToWrite < 0 {
				err = fmt.Errorf("body to write has unexpected value: %d", p.bodyToWrite)
			}

		} else if p.state == HTTPReaderStateChunkedBody {

			log.Tracef("chunk input length: %d, left to write: %d", p.input.Len(), p.bodyToWrite)

			for p.input.Len() > 0 {

				// now try to write the body chunk data
				if p.input.Len() > 0 && p.bodyToWrite > 0 {

					if p.bodyToWrite >= len(p.buff) {
						n, err = p.input.Read(p.buff)
					} else {
						n, err = p.input.Read(p.buff[:p.bodyToWrite])
					}
					if err != nil {
						err = fmt.Errorf("error reading from input buffer: %w", err)
						return
					}

					if n == 0 {
						// nothing read, need more data
						return
					}

					nw, err = p.target.Write(p.buff[:n])
					if err != nil {
						err = fmt.Errorf("error writing to target: %w", err)
						return
					}
					if n != nw {
						err = fmt.Errorf("read and write byte count mismatch, read %d, written %d", n, nw)
					}
					p.bodyToWrite -= nw

				}

				if p.bodyToWrite < 0 {
					err = fmt.Errorf("body to write has unexpected value: %d", p.bodyToWrite)
					return
				}

				// if p.bodyToWrite > 0 {
				// }

				if p.input.Len() == 0 {
					break
				}

				b := p.input.Bytes()
				var size, offset int64
				size, offset, err = _parseHTTPChunkHead(b)
				if err != nil {
					err = fmt.Errorf("error parsing HTTP chunk header: %w", err)
					return
				}
				if offset == 0 {
					// r.lastBodyParse = r.input.Len()
					// need more data, http chunked header not parsed
					return
				}
				// r.lastBodyParse = 0
				log.Tracef("got chunk of size: %d, offset: %d, size+offset: %d, input length: %d", size, offset, size+offset, p.input.Len())

				if size == 0 {
					// final terminating chunk
					if int64(len(b)) >= offset+2 /* include terminating \r\n */ {
						// we must have the whole final chunk into the read buffer, so write it to the target
						log.Trace("got final body chunk")
						n, err = p.target.Write(b[:offset+2])
						if err != nil {
							return
						}
						// reset to wait for next request
						p.state = HTTPReaderStateHead
						// r.finalChunk = false
						p.input.Next(int(offset + 2)) // chunked head is written to output, so advance input buffer pointer
						return
					} else {
						// need more data to read the final chunk, i.e., we need the terminating \r\n, thus exit
						log.Trace("expecting final body chunk")
						// r.finalChunk = true
						return
					}
				} else {
					log.Tracef("got new body chunk of size %d bytes", int(size+2))
				}

				p.bodyToRead = int(size + 2) // + \r\n
				p.bodyToWrite = p.bodyToRead

				_, err = p.target.Write(b[:offset])
				if err != nil {
					err = fmt.Errorf("chunked body: error writing to output buffer: %w", err)
					return
				}
				p.input.Next(int(offset)) // chunked head is written to output, so advance input buffer pointer
			}
		}
	}

	return
}

func (p *HTTPPipe) Pipe() (err error) {

	log := p.log.WithExtension("-pipe")
	fmt := log.E

	if p.target == nil {
		err = fmt.Errorf("no target connection")
		return
	}

	var n int
	var nw int

	// pipe input buffer
	for p.input.Len() > 0 {
		n, err = p.input.Read(p.buff)
		if err != nil {
			err = fmt.Errorf("read from input buffer error: %w", err)
			return
		}

		// must not happen with input buffer
		// if n == 0 {
		// 	log.Trace("need more data")
		// 	// TODO: sleep?
		// 	continue
		// }

		nw, err = p.target.Write(p.buff[:n])
		if err != nil {
			err = fmt.Errorf("write to target error: %w", err)
			return
		}

		if n != nw {
			err = fmt.Errorf("read and write byte count mismatch, read %d, written %d", n, nw)
			// TODO: retry to write rest of data?
			return
		}
	}

	if p.source == nil {
		err = fmt.Errorf("no connection")
		return
	}

	// then pipe directly from source
	for {
		n, err = p.source.Read(p.buff)
		if err != nil {
			err = fmt.Errorf("read from source error: %w", err)
			return
		}

		if n == 0 {
			log.Trace("need more data")
			// TODO: sleep?
			continue
		}

		nw, err = p.target.Write(p.buff[:n])
		if err != nil {
			err = fmt.Errorf("write to target error: %w", err)
			return
		}

		if n != nw {
			err = fmt.Errorf("read and write byte count mismatch, read %d, written %d", n, nw)
			// TODO: retry to write rest of data?
			return
		}
	}

	return
}

func _parseHTTPChunkHead(buff []byte) (dataSize int64, dataOffset int64, err error) {

	if len(buff) < 3 {
		// err = fmt.Errorf("parse-http-chunk-head: not enough data")
		return
	}

	lineLength := bytes.Index(buff, []byte("\r\n"))
	if lineLength == -1 {
		// err = fmt.Errorf("parse-http-chunk-head: invalid chunk head", err)
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
