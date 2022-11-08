package main

import (
	"bytes"
	"fmt"
	"io"
)

type PeekableReadWriter struct {
	inner   io.ReadWriter
	buff    *bytes.Buffer
	pos     int
	maxSize int
	chunk   []byte
	err     error
}

func NewPeekableReadWriter(inner io.ReadWriter) *PeekableReadWriter {
	chunkSize := 1024 // 1 KB
	maxChunks := 8    // total buffer size: 8 * 1 KB = 8 KB
	return &PeekableReadWriter{inner, &bytes.Buffer{}, 0, chunkSize * maxChunks, make([]byte, chunkSize), nil}
}

func NewPeekableReadWriterWithSize(inner io.ReadWriter, chunkSize int, maxChunks int) *PeekableReadWriter {
	return &PeekableReadWriter{inner, &bytes.Buffer{}, 0, chunkSize * maxChunks, make([]byte, chunkSize), nil}
}

func (p *PeekableReadWriter) SetMaxPeekBufferSize(size int) {
	p.maxSize = size
}

func (p *PeekableReadWriter) MaxPeekBufferSize() int {
	return p.maxSize
}

func (p *PeekableReadWriter) SetChunkSize(size int) {
	p.chunk = make([]byte, size)
}

func (p *PeekableReadWriter) ChunkSize() int {
	return len(p.chunk)
}

func (p *PeekableReadWriter) Reset() (err error) {
	if p.buff == nil {
		if p.pos > 0 {
			err = fmt.Errorf("reset error: data from the source consumed past the peek buffer: loss of data is inevitable; increase max peek buffer size")
			return
		}
		p.buff = &bytes.Buffer{}
	}
	p.pos = 0
	p.err = nil
	return
}

func (p *PeekableReadWriter) StopPeeking() {
	p.maxSize = 0
}

func (p *PeekableReadWriter) Read(buff []byte) (n int, err error) {

	if p.buff != nil {

		// read more data from source if needed and allowed to
		if p.buff.Len() == p.pos && p.pos < p.maxSize {
			n, err := p.inner.Read(p.chunk)
			if n > 0 {
				p.buff.Write(p.chunk[:n])
			}
			if err != nil {
				p.err = err
			}
		}

		// if we have some data in the buffer, then return the data immediately (because reading more may require waiting)
		if p.buff.Len() > p.pos {
			n = copy(buff, p.buff.Bytes()[p.pos:])
			p.pos += n
		}

		if p.pos == p.buff.Len() {
			err = p.err // last chunk, return with error if set
			// moving past allowed buffer or failing with error
			if p.pos >= p.maxSize {
				p.buff = nil
			}
		}

		return
	}

	return p.inner.Read(buff)
}

func (p *PeekableReadWriter) Write(buff []byte) (n int, err error) {
	return p.inner.Write(buff)
}
