package main

import (
	"net/url"
	"path"
	"strings"
)

// URLJoinPath backport from go 1.19, code combined from:

// https://cs.opensource.google/go/go/+/refs/tags/go1.19beta1:src/net/url/url.go;l=1252
// https://cs.opensource.google/go/go/+/refs/tags/go1.19beta1:src/net/url/url.go;l=1252;bpv=1;bpt=1

// JoinPath returns a new URL with the provided path elements joined to
// any existing path and the resulting path cleaned of any ./ or ../ elements.
// Any sequences of multiple / characters will be reduced to a single /.
func URLJoinPath(base string, elem ...string) (result string, err error) {
	u, err := url.Parse(base)
	if err != nil {
		return
	}
	if len(elem) > 0 {
		elem = append([]string{u.EscapedPath()}, elem...)
		p := path.Join(elem...)
		// path.Join will remove any trailing slashes.
		// Preserve at least one.
		if strings.HasSuffix(elem[len(elem)-1], "/") && !strings.HasSuffix(p, "/") {
			p += "/"
		}
		u.Path = p
	}
	result = u.String()

	return
}
