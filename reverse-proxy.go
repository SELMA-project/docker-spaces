package main

import (
	"errors"
	"io"
)

type ProxyConnInfo struct {
	TLS bool
	ID  string
}

type ReverseProxy interface {
	Close() error // closes connection to target, source connection Close() is already deferred
	TransferChunkForward() error
	TransferChunkBackward() error
}

type ReverseProxyFactory func(*ProxyLogger, io.ReadWriter, *ProxyConnInfo) ReverseProxy

// var ErrProxyNeedMoreData = errors.New("proxy: more data is needed")

type DynamicReverseProxy2 struct {
	proxyFactories []ReverseProxyFactory
	errors         chan error
	log            *ProxyLogger
}

func NewDynamicReverseProxy2(logger *ProxyLogger, proxyFactories ...ReverseProxyFactory) *DynamicReverseProxy2 {
	return &DynamicReverseProxy2{proxyFactories, make(chan error), logger.Derive().SetPrefix("dynamic-reverse-proxy")}
}

func (p *DynamicReverseProxy2) pipe(transferChunk func() error) (err error) {
	for {
		err = transferChunk()
		if err != nil {
			// if !errors.Is(err, ErrProxyNeedMoreData) {
			// 	break
			// }
			break
		}
	}
	p.errors <- err
	return
}

func (p *DynamicReverseProxy2) Run(source io.ReadWriteCloser, sourceInfo *ProxyConnInfo) (err error) {

	log := p.log.WithExtension("-run")
	fmt := log.E

	log.Info("new connection")

	defer source.Close()

	var proxy ReverseProxy

	input := NewPeekableReadWriter(source)

	// tryAgainProxies := make([]ReverseProxy, 0, len(p.proxyFactories))

	for _, proxyFactory := range p.proxyFactories {

		proxy = proxyFactory(p.log, input, sourceInfo)

		err := proxy.TransferChunkForward()

		// if errors.Is(err, ErrProxyNeedMoreData) {
		// 	log.Debugf("more data is needed for proxy '%s'", proxy)
		// 	tryAgainProxies = append(tryAgainProxies, proxy)
		// 	continue
		// }

		if err == nil {
			// tryAgainProxies = nil
			break
		}

		log.Tracef("proxy '%s' responded with error: %v", proxy, err)

		// reset
		proxy = nil
		err = input.Reset()
		if err != nil {
			err = fmt.Errorf("proxy detection error: %w", err)
			return err
		}
	}

	/*
		for tryAgainProxies != nil && len(tryAgainProxies) > 0 {

			currentProxyList := tryAgainProxies
			tryAgainProxies = make([]ReverseProxy, 0, len(currentProxyList))

			for _, proxy = range currentProxyList {

				err := proxy.TransferChunkForward()

				if errors.Is(err, ErrProxyNeedMoreData) {
					log.Debugf("still more data is needed for proxy '%s'", proxy)
					tryAgainProxies = append(tryAgainProxies, proxy)
					continue
				}

				if err == nil {
					tryAgainProxies = nil
					break
				}

				log.Tracef("proxy '%s' responded with error: %v", proxy, err)

				// reset
				proxy = nil
				err = input.Reset()
				if err != nil {
					err = fmt.Errorf("proxy detection error: %w", err)
					return err
				}
			}
		}
	*/

	if proxy == nil {
		err = fmt.Errorf("no proxy to handle incomming connection")
		return
	}

	log.Info("responded proxy:", proxy)

	defer proxy.Close()

	input.StopPeeking()

	// https://stackoverflow.com/a/39425375
	// There is a protocol(HTTP) on how the messages are exchanged. HTTP dictates that responses must arrive in the order they were requested. So it goes like-
	// Request;Response;Request;Response;Request;Response;...

	go p.pipe(proxy.TransferChunkForward)
	go p.pipe(proxy.TransferChunkBackward)

	err = <-p.errors

	if errors.Is(err, io.EOF) {
		err = nil
	} else {
		err = fmt.Errorf("pipe error: %w", err)
	}

	if err == nil {
		log.Info("connection closed")
	} else {
		log.Errorf("%v", err)
	}

	return
}
