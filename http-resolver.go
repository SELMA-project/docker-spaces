package main

import "fmt"

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
