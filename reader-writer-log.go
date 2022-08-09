package main

import "io"

type ReadWriterLogger struct {
	inner io.ReadWriter
	name  string
}

func NewReadWriterLogger(inner io.ReadWriter, name string) *ReadWriterLogger {
	return &ReadWriterLogger{inner: inner, name: name}
}

func (r *ReadWriterLogger) Read(buff []byte) (n int, err error) {

	n, err = r.inner.Read(buff)

	log.Tracef("connection %s read buffer (%d bytes): %s", r.name, n, string(buff[:n]))

	return
}

func (r *ReadWriterLogger) Write(buff []byte) (n int, err error) {
	log.Tracef("connection %s write buffer (%d bytes): %s", r.name, len(buff), string(buff))
	// return r.inner.Write(buff)
	n, err = r.inner.Write(buff)
	log.Tracef("connection %s write completed: %d bytes attempted, %d bytes succeeded", r.name, len(buff), n)
	return
}

// func (r *ReadWriterLogger) Close() error {
// 	return r.inner.Close()
// }
