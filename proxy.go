package main

import (
	"bytes"
	"fmt"
	"io"

	// "log"
	"net"
	"time"
)

type ResolvedTarget interface {
	RemoteAddress() string
	HeadData() []byte
	Activity()
	Closed()
}

type ResolvedTargetConnection interface {
	Connect() (io.ReadWriteCloser, error)
}

type TargetResolver interface {
	Resolve([]byte) (ResolvedTarget, error)
}

// type ReplacerFunc func([]byte) []byte
type ReplacerFunc func(io.ReadWriteCloser) io.ReadWriteCloser
type TargetResolverFunc func(buff []byte) (conn io.ReadWriteCloser, reverseReplacer ReplacerFunc, err error)

type DynamicReverseProxy struct {
	sentBytes     uint64
	receivedBytes uint64

	// laddr, raddr  net.Addr
	// laddr, raddr  string

	proxyAddress, targetAddress string

	proxyConn io.ReadWriteCloser
	// proxyConn, targetConn io.ReadWriteCloser
	// lconn, rconn  io.ReadWriteCloser
	erred  bool
	errsig chan bool
	// tlsUnwrapp    bool
	// tlsAddress    string

	// Matcher  func([]byte)
	// Replacer func([]byte) []byte

	// Settings
	// Nagles    bool
	// Log       Logger
	// OutputHex bool

	CORS bool

	// remoteHosts *[]RemoteHost

	// targetResolver TargetResolverFunc
	targetResolvers []TargetResolver
	resolvedTarget  ResolvedTarget
}

// TODO: target address chooser ?

// func NewDynamicReverseProxy(proxyConn net.Conn /* proxyAddress string, */, targetResolver TargetResolverFunc) *DynamicReverseProxy {
func NewDynamicReverseProxy(proxyConn net.Conn /* proxyAddress string, */, targetResolvers ...TargetResolver) *DynamicReverseProxy {
	return &DynamicReverseProxy{
		proxyConn: proxyConn,
		// proxyAddress: proxyAddress,

		// lconn: lconn,
		// laddr: laddr,
		// raddr:  raddr,
		erred:  false,
		errsig: make(chan bool),
		// Log:    NullLogger{},
		targetResolvers: targetResolvers,
	}
}

func (p *DynamicReverseProxy) Start(broker *Broker) {
	defer p.proxyConn.Close()

	//display both ends
	// p.Log.Info("Opened %s >>> %s", p.laddr.String(), p.raddr.String())

	go p.proxySelectTargetAndSetupPipe(p.proxyConn)

	//wait for close...
	<-p.errsig
	// p.Log.Info("Closed (%d bytes sent, %d bytes recieved)", p.sentBytes, p.receivedBytes)

	if p.resolvedTarget != nil {
		p.resolvedTarget.Closed()
	}
}

func (p *DynamicReverseProxy) err(s string, err error) {
	if p.erred {
		return
	}
	if err != io.EOF {
		// p.Log.Warn(s, err)
		log.Errorf("proxy: %s: %v", s, err)
	}
	p.errsig <- true
	p.erred = true
}

func (p *DynamicReverseProxy) pipe(src, dst io.ReadWriter /* , replacer func([]byte) []byte */, notify bool) {
	// islocal := src == p.lconn
	//
	// var dataDirection string
	// if islocal {
	// 	dataDirection = ">>> %d bytes sent%s"
	// } else {
	// 	dataDirection = "<<< %d bytes recieved%s"
	// }
	//
	// var byteFormat string
	// if p.OutputHex {
	// 	byteFormat = "%x"
	// } else {
	// 	byteFormat = "%s"
	// }

	//directional copy (64k buffer)
	buff := make([]byte, 0xffff)
	for {
		n, err := src.Read(buff)
		if err != nil {
			p.err("pipe: read failed", err)
			return
		}
		b := buff[:n]

		if notify && p.resolvedTarget != nil {
			p.resolvedTarget.Activity()
		}

		//execute match
		// if p.Matcher != nil {
		// 	p.Matcher(b)
		// }

		//execute replace
		// if p.Replacer != nil {
		// 	b = p.Replacer(b)
		// }
		// if replacer != nil {
		// 	b = replacer(b)
		// 	// TODO: replacer must modify content-length header in case of HTTP
		// }

		//show output
		// p.Log.Debug(dataDirection, n, "")
		// p.Log.Trace(byteFormat, b)

		//write out result
		n, err = dst.Write(b)
		if err != nil {
			p.err("pipe: write failed", err)
			return
		}
		// if islocal {
		// 	p.sentBytes += uint64(n)
		// } else {
		// 	p.receivedBytes += uint64(n)
		// }
	}
}

/*
// func parseProtocolAndWriteToTargetConn(buff []byte) (conn io.ReadWriteCloser, reverseReplacer func([]byte) []byte, err error) {
func parseProtocolAndWriteToTargetConn(buff []byte) (conn io.ReadWriteCloser, reverseReplacer ReplacerFunc, err error) {

	requestLineLength := bytes.Index(buff, []byte("\r\n"))
	if requestLineLength == -1 {
		return
	}

	// minimum string for HTTP (length 12): GET / HTTP/2
	if requestLineLength < 12 {
		err = fmt.Errorf("input packet too short for HTTP request (%d), got string: '%s'", requestLineLength, string(buff[:requestLineLength]))
		return
	}

	parts := strings.Split(string(buff[:requestLineLength]), " ")

	if len(parts) != 3 {
		err = fmt.Errorf("input packet does not look like HTTP request, has %d parts instead of required 3 parts", len(parts))
		return
	}

	_, _, httpOK := http.ParseHTTPVersion(parts[2])
	if !httpOK {
		err = fmt.Errorf("input packet does not contain valid HTTP request, invalid version string")
		return
	}

	log.Println("Got HTTP method:", parts[0])
	log.Println("Got HTTP path:", parts[1])
	log.Println("Got HTTP version:", parts[2])

	// TODO: choose target here

	remoteAddress := ""

	// *p.remoteHosts
	for _, remoteHost := range remoteHosts {
		if strings.HasPrefix(parts[1], remoteHost.Endpoint) {
			parts[1] = strings.Replace(parts[1], remoteHost.Endpoint, "", 1) // replace URL
			remoteAddress = remoteHost.Remote
			log.Println("found remote host configuration", remoteHost)
			if len(remoteHost.Replacer) > 0 {
				// reverseReplacer = createReplacer(remoteHost.Replacer)
			}
			break
		}
	}

	// parts[1]
	if len(remoteAddress) == 0 {
		err = fmt.Errorf("target not found")
		// log.Printf("target not found")
		// p.err("target not found", nil)
		return
	}

	// var err error
	//connect to remote
	// if p.tlsUnwrapp {
	// 	p.rconn, err = tls.Dial("tcp", remoteAddress, nil)
	// } else {
	// 	p.raddr, err = net.ResolveTCPAddr("tcp", remoteAddress)
	// 	if err != nil {
	// 		p.err("Failed to resolve remote address", err)
	// 	}
	// 	p.rconn, err = net.DialTCP("tcp", nil, p.raddr)
	// }
	conn, err = net.Dial("tcp", remoteAddress)
	if err != nil {
		// p.Log.Warn("Remote connection failed: %s", err)
		// log.Printf("Remote connection failed: %s", err)
		err = fmt.Errorf("remote connection failed: %v", err)
		return
	}

	// write modified header to the target
	log.Println("sending:", strings.Join(parts, " "))
	_, err = conn.Write([]byte(strings.Join(parts, " ")))
	if err != nil {
		err = fmt.Errorf("write failed: %v", err)
		// p.err("Write failed '%s'\n", err)
		return
	}

	// rest of the buffer after request line (note: includes \r\n)
	// buff = buff[requestLineLength:]

	// if p.Replacer != nil {
	// 	b = p.Replacer(b)
	// }

	// write rest of the buffer to the target
	_, err = conn.Write(buff[requestLineLength:])
	if err != nil {
		err = fmt.Errorf("write failed: %v", err)
		// p.err("Write failed '%s'\n", err)
		return
	}

	return
}
*/

func (p *DynamicReverseProxy) proxySelectTargetAndSetupPipe(src io.ReadWriter) {

	var err error
	var targetConn io.ReadWriteCloser
	// var reverseReplacer ReplacerFunc
	// var reverseReplacer func([]byte) []byte

	var buff bytes.Buffer

	hbuff := make([]byte, 0xfff)

	for {
		N, err := src.Read(hbuff)
		if err != nil {
			p.err("select-target: incomming read failed", err)
			return
		}

		// does this ever happen?
		if N == 0 {
			log.Warn("proxy: select-target: incomming read returned empty buffer")
			time.Sleep(100 * time.Millisecond)
			continue
		}

		n, err := buff.Write(hbuff[:N])
		if err != nil {
			p.err("select-target: incomming head buffer write failed", err)
			return
		}
		if n < N {
			p.err("select-target: incomplete write to incomming head buffer", nil)
			// p.err("protocol head buffer too small or invalid protocol", nil)
			return
		}

		for _, targetResolver := range p.targetResolvers {

			p.resolvedTarget, err = targetResolver.Resolve(buff.Bytes())

			// targetConn, reverseReplacer, err = parseProtocolAndWriteToTargetConn(buff.Bytes())
			// targetConn, reverseReplacer, err = p.targetResolver(buff.Bytes())
			if err != nil {
				continue
				// p.err("select-target: target resolver error", err)
				// return
			}
			if p.resolvedTarget != nil {
				log.Trace("proxy: resolved target address:", p.resolvedTarget.RemoteAddress())
				break
			}
		}

		if p.resolvedTarget != nil && err == nil {
			break
		} else if err != nil {
			p.err("select-target: unable to resolve target, last error", err)
			return
		}
		// if targetConn != nil {
		// 	break
		// }
	}

	/*
		slept := 0 // how much slept in ms
		//directional copy (64k buffer)
		buff := make([]byte, 0xffff) // TODO: configure length of protocol detection buffer
		N := 0

		hbuff := make([]byte, 0xfff)
		for {
			n, err := src.Read(hbuff)
			if err != nil {
				p.err("Read failed", err)
				return
			}

			if n == 0 {
				if slept >= 10000 {
					// timeout after 10s
					p.err("timed out", nil)
					return
				}
				// TODO: wait for more? sleep?
				time.Sleep(100 * time.Millisecond)
				slept += 100
				continue
				// p.err("no data", nil)
				// return
			}

			// b := hbuff[:n]
			// log.Printf("got %d bytes", n)

			n = copy(buff[N:], hbuff[:n])
			if n == 0 {
				// buffer full
				p.err("protocol head buffer too small or invalid protocol", nil)
				return
			}
			N += n

			// try this great example for proof of concept:
			// s := []int{0, 1, 2}
			// n := copy(s[1:], []int{4, 5, 6, 7})
			// fmt.Println("result", s, n)
			// // output: result [0 4 5] 2

			targetConn, reverseReplacer, err = parseProtocolAndWriteToTargetConn(buff[:N])
			if err != nil {
				p.err("error detecting protocol and target address", err)
				return
			}
			if targetConn != nil {
				break
			}
		}
	*/

	remoteAddress := p.resolvedTarget.RemoteAddress()

	log.Info("connecting to remote address", remoteAddress)

	if connectTarget, ok := p.resolvedTarget.(ResolvedTargetConnection); ok {
		targetConn, err = connectTarget.Connect()
		if err != nil {
			p.err(fmt.Sprintf("select-target: remote connection to %s failed", remoteAddress), err)
			return
		}

		defer targetConn.Close()
	} else {
		log.Trace("proxy: connecting to target:", remoteAddress)
		targetConn, err = net.Dial("tcp", remoteAddress)
		if err != nil {
			p.err(fmt.Sprintf("select-target: remote connection to %s failed", remoteAddress), err)
			return
		}

		defer targetConn.Close()
	}

	headData := p.resolvedTarget.HeadData()

	n, err := targetConn.Write(headData)
	if err != nil {
		p.err("select-target: remote connection head data write failed", err)
		return
	}

	if n < len(headData) {
		p.err("select-target: remote connection head data write incomplete", nil)
		return
	}

	// if reverseReplacer != nil {
	// 	targetConn = reverseReplacer(targetConn)
	// }

	if p.CORS {
		targetConn = NewHTTPCORSInject(targetConn)
	}

	// pipe in reverse direction in a separate goroutine
	go p.pipe(targetConn, p.proxyConn /*, reverseReplacer*/, p.resolvedTarget != nil)

	p.pipe(p.proxyConn, targetConn /*, nil*/, false)
}
