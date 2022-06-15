package main

import (
	"bytes"
	"fmt"
	"io"
)

type HTTPSetCookieReplacer struct {
	inner         io.ReadWriteCloser
	SetCookie     string
	headerWritten bool
	// buff          *bytes.Buffer
	input       bytes.Buffer
	output      bytes.Buffer
	buff        [0xfff]byte
	headersRead bool
}

func NewHTTPSetCookieReplacer(conn io.ReadWriteCloser, setCookie string) *HTTPSetCookieReplacer {
	return &HTTPSetCookieReplacer{}
}

func (r *HTTPSetCookieReplacer) Read(buff []byte) (n int, err error) {
	// this function reads from inner connection
	// and returns HTTP header with spliced set-cookie header

	if r.output.Len() > 0 {
		// return r.output.Read(buff)
		n, err = r.output.Read(buff)
		if !r.headersRead {
			k := bytes.Index(buff[:n], []byte("\r\n\r\n"))
			if k == -1 {
				fmt.Println(string(buff[:n]))
			} else {
				fmt.Println(string(buff[:k]))
				r.headersRead = true
			}
		}
		return
	} else if r.headerWritten {
		// return r.inner.Read(buff)
		n, err = r.inner.Read(buff)
		if !r.headersRead {
			k := bytes.Index(buff[:n], []byte("\r\n\r\n"))
			if k == -1 {
				fmt.Println(">>>", string(buff[:n]))
			} else {
				fmt.Println(">>>", string(buff[:k]))
				r.headersRead = true
			}
		}
		return
	}

	var in int
	// var out int

	in, err = r.inner.Read(r.buff[:])
	if err != nil {
		return
	}

	_, err = r.input.Write(r.buff[:in])
	if err != nil {
		return
	}

	b := r.input.Bytes()

	requestLineLength := bytes.Index(b, []byte("\r\n"))
	if requestLineLength == -1 {
		return
	}

	r.output.Write(b[:requestLineLength])
	r.output.Write([]byte("\r\nSet-Cookie: " + r.SetCookie + "; path=/"))
	r.output.Write(b[requestLineLength:])

	r.headerWritten = true

	// return r.output.Read(buff)
	n, err = r.output.Read(buff)
	if !r.headersRead {
		k := bytes.Index(buff[:n], []byte("\r\n\r\n"))
		if k == -1 {
			fmt.Println(">>>", string(buff[:n]))
		} else {
			fmt.Println(">>>", string(buff[:k]))
			r.headersRead = true
		}
	}

	return

	/*
		if r.headerWritten && r.buff == nil {
			fmt.Println("READ NORMAL")
			return r.inner.Read(buff)
		} else if r.headerWritten && r.buff != nil && r.buff.Len() > 0 {
			fmt.Println("READ BUFF")
			n, err = r.buff.Read(buff)
			if err == nil && r.buff.Len() == 0 {
				r.buff = nil
			}
			return
		}

		if r.buff == nil {
			r.buff = bytes.NewBuffer(make([]byte, 0xfff))
		}

		b := make([]byte, 0xfff)

		var N int
		N, err = r.inner.Read(b)
		if err != nil {
			return
		}

		fmt.Println("INNER READ", string(b), "<")

		N, err = r.buff.Write(b[:N])
		if err != nil {
			return
		}

		bb := r.buff.Bytes()

		requestLineLength := bytes.Index(bb, []byte("\r\n"))
		if requestLineLength == -1 {
			return
		}

		newbuff := bytes.NewBuffer(make([]byte, len(bb)+len(r.SetCookie)+14+0xff [# rezerve #]))

		// ok, got request line, add set cookie
		// newbuff := bb[:requestLineLength] + []byte("\r\nSet-Cookie: "+r.SetCookie) + bb[requestLineLength:]
		newbuff.Write(bb[:requestLineLength])
		newbuff.Write([]byte("\r\nSet-Cookie: " + r.SetCookie))
		newbuff.Write(bb[requestLineLength:])

		r.buff = newbuff

		fmt.Println("INTERNAL BUFF:", string(r.buff.Bytes()), "<", n)

		r.headerWritten = true

		fmt.Println("before WROTE READ:", r.buff.Len())
		n, err = r.buff.Read(buff)
		if err == nil && r.buff.Len() == 0 {
			r.buff = nil
		}
		fmt.Println("WROTE READ:", string(buff[:n]), "<", n, r.buff.Len(), len(r.buff.Bytes()), len(buff))
		return
	*/

	// return r.inner.Read(buff)

	// try this great example for proof of concept:
	// s := []int{0, 1, 2}
	// n := copy(s[1:], []int{4, 5, 6, 7})
	// fmt.Println("result", s, n)
	// // output: result [0 4 5] 2
}

func (r *HTTPSetCookieReplacer) Write(buff []byte) (n int, err error) {
	return r.inner.Write(buff)
	/*
		fmt.Println("WRITE")
		if r.headerWritten {
			fmt.Println("writing out, headers written")
			return r.inner.Write(buff)
		}
		fmt.Println("writing out, parseing header")
		if r.buff == nil {
			r.buff = bytes.NewBuffer(make([]byte, 0xfff))
		}
		// write to internal buff
		r.buff.Write(buff)

		// TODO: now try to parse buff, if ok, then write to inner and set headerWritten = true
		b := r.buff.Bytes()
		requestLineLength := bytes.Index(b, []byte("\r\n"))
		if requestLineLength == -1 {
			return
		}

		fmt.Println("!!!!! writin to inner container from buff")

		var N int

		N, err = r.inner.Write(b[:requestLineLength])
		if err != nil {
			return
		}
		n += N

		N, err = r.inner.Write([]byte("\r\nSet-Cookie: " + r.SetCookie))
		if err != nil {
			return
		}
		n += N

		N, err = r.inner.Write(b[requestLineLength:])
		if err != nil {
			return
		}
		n += N

		r.headerWritten = true

		return
	*/
}

func (r *HTTPSetCookieReplacer) Close() error {
	return r.inner.Close()
}
