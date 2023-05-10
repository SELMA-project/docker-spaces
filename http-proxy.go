package main

import (
	"io"
	"strings"
)

// stateless, to be reused
type HTTPHandler interface {
	// NOTE: each handler may have internally a common responder function for both RespondsAtLevel and ProcessRequest
	RespondsAtLevel(logger *ProxyLogger, request *ParsedHTTPRequest) int
	ProcessRequest(logger *ProxyLogger, request *ParsedHTTPRequest, prevTargetConn io.ReadWriteCloser, prevTargetID string) (targetConn io.ReadWriteCloser, targetID string, err error)
	ProcessResponse(logger *ProxyLogger, response *ParsedHTTPResponse) (err error)
	ResponseTransferred(logger *ProxyLogger, request *ParsedHTTPRequest, response *ParsedHTTPResponse)
	Closed(logger *ProxyLogger, request *ParsedHTTPRequest)
}

type HTTPResponseProcessor interface {
	ProcessResponse(logger *ProxyLogger, response *ParsedHTTPResponse) (err error)
}

type HTTPProxyConfiguration struct {
	Handlers              []HTTPHandler
	ResponsePostProcessor HTTPResponseProcessor
}

func NewHTTPProxyConfiguration(handlers ...HTTPHandler) *HTTPProxyConfiguration {
	return &HTTPProxyConfiguration{handlers, nil}
}

func (c *HTTPProxyConfiguration) NewProxy(logger *ProxyLogger, source io.ReadWriter, sourceInfo *ProxyConnInfo) ReverseProxy {
	return NewHTTPProxy(logger, source, sourceInfo, c.Handlers, c.ResponsePostProcessor)
}

type HTTPProxy struct {
	log                   *ProxyLogger
	sourceInfo            *ProxyConnInfo
	source                io.ReadWriter
	target                io.ReadWriteCloser
	forward               *HTTPPipe
	backward              *HTTPPipe
	requestHandlerLogger  *ProxyLogger
	responseHandlerLogger *ProxyLogger
	requestChan           chan *ParsedHTTPRequest
	request               *ParsedHTTPRequest
	backwardRequest       *ParsedHTTPRequest
	response              *ParsedHTTPResponse
	targetID              string
	handler               HTTPHandler
	handlers              []HTTPHandler
	initialParseDone      bool
	responsePostProcessor HTTPResponseProcessor
}

func NewHTTPProxy(logger *ProxyLogger, source io.ReadWriter, sourceInfo *ProxyConnInfo, handlers []HTTPHandler, responsePostProcessor HTTPResponseProcessor) *HTTPProxy {

	log := logger.Derive().SetPrefix("http-proxy")

	return &HTTPProxy{
		log:                   log,
		sourceInfo:            sourceInfo,
		source:                source,
		target:                nil,
		forward:               NewHTTPPipe(log, source, nil),
		backward:              nil, //NewHTTPPipe(log, nil, source),
		requestHandlerLogger:  log.Derive().SetPrefix("http-handler"),
		responseHandlerLogger: log.Derive().SetPrefix("http-handler"),
		request:               nil,
		requestChan:           make(chan *ParsedHTTPRequest, 3),
		handler:               nil,
		handlers:              handlers,
		responsePostProcessor: responsePostProcessor,
	}
}

func (p *HTTPProxy) String() string {
	return "HTTP proxy"
}

func (p *HTTPProxy) Close() (err error) {

	log := p.log.WithExtension("-close")
	fmt := log.E

	log.Info("closing connection")

	close(p.requestChan)

	if p.handler != nil {
		p.handler.Closed(log.Derive().SetPrefix("http-handler"), p.request)
		p.request = nil
	}

	if p.target != nil {
		if closer, ok := p.target.(io.Closer); ok {
			err = closer.Close()
			if err != nil {
				err = fmt.Errorf("close error: %w", err)
			}
		} else {
			err = fmt.Errorf("close error: target is not a closer")
		}
		return
	}

	log.Debugf("close without target connection")

	return
}

func (p *HTTPProxy) TransferChunkForward() (err error) {

	log := p.log.WithExtension("-transfer-chunk-forward")
	fmt := log.E

	if p.forward.Upgraded() {
		err = p.forward.Pipe()
		return
	}

	log.Trace("about to read from source")

	err = p.forward.Read()

	log.Tracef("read from source with err: %v", err)

	if p.forward.Upgraded() {
		err = p.forward.Pipe()
		return
	}

	/*
		if errors.Is(err, io.EOF) [# && p.forward.Empty() #] {
			log.Trace("EOF", p.forward.input.Len())
			// proceed as normal
			err = fmt.Errorf("EOF: %w", err)
			return
		} else if err != nil {
			err = fmt.Errorf("forward pipe error: read error: %w", err)
			return
		}
	*/
	if err != nil {
		err = fmt.Errorf("forward pipe error: read error: %w", err)
		return
	}

	var (
		request  *ParsedHTTPRequest
		target   io.ReadWriteCloser
		targetID string
	)

	for {

		p.forward.Logger().SetTarget("", false)

		request, err = p.forward.ParseRequest()
		if err != nil {
			p.request = nil
			err = fmt.Errorf("error parsing forward pipe: %w", err)
			break
		}

		if request == nil && p.forward.ExpectHead() && p.initialParseDone == false {
			// read more data
			log.Trace("incomplete head, about to read more data from source")
			err = p.forward.Read()
			if err != nil {
				err = fmt.Errorf("forward pipe error: read error: %w", err)
				return
			}
			continue
		}

		// more data is needed from the source
		// if request == nil && p.forward.state == HTTPReaderStateHead /* && !p.initialParseDone */ {
		// 	log.Debug("more data is needed for initial header parse to parse HTTP header")
		// 	p.request = nil
		// 	err = ErrProxyNeedMoreData
		// 	return
		// }

		// log.Trace("got request:", request)

		if request != nil {

			p.initialParseDone = true

			request.ConnectionInfo = p.sourceInfo
			p.request = request
			p.requestHandlerLogger.SetTarget("", false)
			target = nil

			log.Trace("got request:", request.Short(), "referrer:", request.Headers.Get("Referrer"))

			// try handlers
			level := -1
			var handlerAtLevel HTTPHandler
			for _, handler := range p.handlers {
				log.Tracef("checking if handler %s responds", handler)
				if l := handler.RespondsAtLevel(p.requestHandlerLogger, request); l >= 0 && (level == -1 || l < level) {
					log.Tracef("handler %s responded at level %d", handler, l)
					level = l
					handlerAtLevel = handler
					if l == 0 {
						// responds at top level, no need to further try more handlers
						break
					}
				}
			}

			if level >= 0 && handlerAtLevel != nil {
				// responded
				handler := handlerAtLevel
				p.handler = handler
				log.Debugf("processing request with handler %s responded at level %d", handler, level)
				target, targetID, err = handler.ProcessRequest(p.requestHandlerLogger, request, p.target, p.targetID)
				if err == nil && target != nil {
					if len(targetID) == 0 {
						p.handler = nil
					}
					p.requestHandlerLogger.SetTarget(targetID, false)
					p.responseHandlerLogger.SetTarget(targetID, true)
				}
			}

			p.target = target
			p.targetID = targetID

			if target == nil {
				err = fmt.Errorf("target not detected, aborting")
				return
			}

			// update pipes
			p.forward.SetTarget(target)
			p.forward.Logger().SetTarget(p.targetID, false)
			if p.backward == nil {
				p.backward = NewHTTPPipe(p.log.Derive().SetTarget(p.targetID, true), p.target, p.source)
			} else {
				p.backward.Logger().SetTarget(p.targetID, true)
				p.backward.SetSource(p.target)
			}

			log.Tracef("got request, sending through channel")
			p.requestChan <- request
		}

		if p.target == nil {
			err = fmt.Errorf("target not detected, aborting")
			break
		}

		log.Tracef("about to write request")
		// at least one write is called: write head and all following available data
		err = p.forward.Write()
		if err != nil {
			err = fmt.Errorf("error writing to forward pipe: %w", err)
			break
		}
		log.Tracef("request written, err: %v", err)

		if request == nil {
			break // we do not expect any more data to be written
		}
	}

	return
}

func (p *HTTPProxy) TransferChunkBackward() (err error) {

	log := p.log.WithExtension("-transfer-chunk-backward")
	fmt := log.E

	if p.backward == nil {
		err = fmt.Errorf("backward pipe is not created")
		return
	}

	if p.forward.Upgraded() {
		err = p.backward.Pipe()
		return
	}

	// we do not want to even try to read for response until we have a successful request handler executed,
	// because the request handler will determine the target end and open connection to it, i.e., we may not
	// have an open connection to read response from at this point

	var request *ParsedHTTPRequest
	// first wait for a request, so that target is resolved, only then try to read response
	// only once on initial head parse
	if p.backward.StartHead() || (p.backward.ExpectHead() && p.backwardRequest == nil) {
		// awaiting head
		log.Tracef("waiting for request")
		request = <-p.requestChan
		p.backwardRequest = request
		if request != nil {
			log.Trace("storing backward request:", p.backwardRequest.Short())
		}
		if request == nil {
			log.Debug("got empty request, exiting")
			err = io.EOF
			return
		}
		// log.Trace("got request:", request)
	} else {
		request = p.backwardRequest
		log.Trace("re-stored backward request:", request)
	}

	log.Tracef("reading from target")
	err = p.backward.Read()
	log.Tracef("read from target with err: %v", err)

	/*
		if errors.Is(err, io.EOF) [# && p.forward.Empty() #] {
			log.Trace("EOF", p.forward.input.Len())
			// proceed as normal
			err = fmt.Errorf("EOF: %w", err)
			return
		} else if err != nil {
			err = fmt.Errorf("forward pipe error: read error: %w", err)
			return
		}
	*/
	if err != nil {
		err = fmt.Errorf("backward pipe error: read error: %w", err)
		return
	}

	var response *ParsedHTTPResponse

	for {

		response, err = p.backward.ParseResponse()
		if err != nil {
			err = fmt.Errorf("error parsing backward pipe: %w", err)
			break
		}

		if response != nil {
			log.Trace("got response:", response.Short())
		} else {
			log.Trace("parser returned nil response, err:", err)
		}

		if response != nil {

			p.response = response

			log.Trace("clear backward request storage, current value to be cleared:", p.backwardRequest.Short())
			p.backwardRequest = nil // optional?

			log.Trace("active request for response:", request.Short())
			response.Request = request

			err = p.handler.ProcessResponse(p.responseHandlerLogger, response)
			if err != nil {
				err = fmt.Errorf("error processing backward pipe response: %w", err)
				return
			}

			if p.responsePostProcessor != nil {
				err = p.responsePostProcessor.ProcessResponse(p.responseHandlerLogger, response)
				if err != nil {
					err = fmt.Errorf("error post processing backward pipe response: %w", err)
					return
				}
			}

			if response.StatusCode == 101 {
				if strings.ToLower(response.Headers.Get("Connection")) == "upgrade" {
					// upgraded connection
					p.forward.Upgrade()
					p.backward.Upgrade()
					log.Infof("connection upgraded")
				}
			}
		}

		// at least one write is called: write head and all following available data
		err = p.backward.Write()
		if err != nil {
			err = fmt.Errorf("error writing to backward pipe: %w", err)
			break
		}

		if p.backward.StartHead() && p.response != nil {
			// for retained handler, request and response call ResponseTransferred()
			if p.handler != nil {
				p.handler.ResponseTransferred(p.responseHandlerLogger, p.response.Request, p.response)
			}

			p.response = nil
		}

		// TODO: connection upgraded?
		if p.backward.Upgraded() {
			return
		}

		if response == nil {
			break // we do not expect any more data to be written
		}
	}

	return
}

type HTTPCORSInjector struct {
}

func (h *HTTPCORSInjector) ProcessResponse(logger *ProxyLogger, response *ParsedHTTPResponse) (err error) {

	// log := logger.WithExtension(": http-cors-inject")
	// fmt := log.E

	// CORS
	response.Headers.Set("Access-Control-Request-Headers", "Content-Type")
	response.Headers.Set("Access-Control-Allow-Headers", "Content-Type")
	response.Headers.Set("Access-Control-Allow-Origin", "*")
	response.Headers.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
	response.Headers.Set("Access-Control-Allow-Credentials", "true")

	return
}
