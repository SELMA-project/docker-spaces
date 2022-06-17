package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// based on https://github.com/jpillora/go-tcp-proxy

type RemoteHost struct {
	Endpoint string `json:"endpoint" yaml:"endpoint"`
	Remote   string `json:"remote" yaml:"remote"`
	Replacer string `json:"replacer" yaml:"replacer"`
}

// var logger ColorLogger

func load(filename string) (remoteHosts []RemoteHost, err error) {

	file, err := os.Open(filename)
	if err != nil {
		log.Printf("workers configuration file %s not available: %v", filename, err)
		return
	}
	defer file.Close()

	yaml.NewDecoder(file).Decode(&remoteHosts)

	return
}

func createReplacer(replace string) func([]byte) []byte {
	if replace == "" {
		return nil
	}
	//split by / (TODO: allow slash escapes)
	parts := strings.Split(replace, "~")
	if len(parts) != 2 {
		// logger.Warn("Invalid replace option")
		log.Println("invalid replace option")
		return nil
	}

	re, err := regexp.Compile(string(parts[0]))
	if err != nil {
		// logger.Warn("Invalid replace regex: %s", err)
		log.Printf("invalid replace regex: %s", err)
		return nil
	}

	repl := []byte(parts[1])

	// logger.Info("Replacing %s with %s", re.String(), repl)
	log.Printf("Replacing %s with %s", re.String(), repl)
	return func(input []byte) []byte {
		return re.ReplaceAll(input, repl)
	}
}
func httpParserAndTargetResolver(buff []byte) (conn io.ReadWriteCloser, reverseReplacer ReplacerFunc, err error) {

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
		err = fmt.Errorf("remote connection filed: %v", err)
		return
	}

	// write modified header to the target
	log.Println("sending:", strings.Join(parts, " "))
	_, err = conn.Write([]byte(strings.Join(parts, " ")))
	if err != nil {
		err = fmt.Errorf("write filed: %v", err)
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
		err = fmt.Errorf("write filed: %v", err)
		// p.err("Write failed '%s'\n", err)
		return
	}

	return
}

type DockerContainerParams struct {
	image string
	port  int
	label string
}

type DockerContainerState struct {
	id       string
	last     int64
	port     int
	running  bool
	busy     bool
	starting bool
}

type DockerContainerLauncherResolver struct {
	docker       *Docker // TODO: have multiple docker hosts and a way to choose
	stopping     bool
	mutex        *sync.RWMutex
	startTimeout float64

	runningContainers map[DockerContainerParams]*DockerContainerState
}

func (r *DockerContainerLauncherResolver) Stop() {
	r.stopping = true
}

func (r *DockerContainerLauncherResolver) Cleanup(jobTimeout, stopTimeout, containerTimeout float64) {
	now := time.Now()
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	for params, state := range r.runningContainers {
		if state.starting {
			continue // do not touch containers in starting phase
		}
		last := time.Unix(state.last, 0)
		minutesElapsed := now.Sub(last).Minutes()
		// fmt.Println(minutesElapsed, params, state)
		if state.running && !state.busy && minutesElapsed >= stopTimeout /* 1min */ {

			err := r.stopContainer(state.id)
			if err != nil {
				log.Printf("error stopping container: %v", err)
			}

			state.running = false

		} else if !state.running && !state.busy && minutesElapsed >= containerTimeout {

			err := r.removeContainer(state.id)
			if err != nil {
				log.Printf("error removing container: %v", err)
				continue
			}

			r.mutex.RUnlock()
			r.mutex.Lock()
			delete(r.runningContainers, params)
			r.mutex.Unlock()
			r.mutex.RLock()
		} else if state.running && state.busy && minutesElapsed >= jobTimeout {
			log.Println("killing container")
			err := r.killContainer(state.id)
			if err != nil {
				log.Printf("error killing container: %v", err)
			}

			state.running = false
			state.busy = false
		}
	}

	return
}

func (r *DockerContainerLauncherResolver) PeriodicCleanup(jobTimeout, stopTimeout, containerTimeout, sleep float64) {
	if stopTimeout == 0 {
		// for periodic cleanup time interval must be nonzero
		stopTimeout = 1 // defaults to 1 min
	}
	if containerTimeout == 0 {
		containerTimeout = 60 // defaults to 60 min
	}
	if sleep == 0 {
		sleep = 1
	}
	for {
		r.Cleanup(jobTimeout, stopTimeout, containerTimeout)
		if r.stopping {
			break
		}
		time.Sleep(time.Duration(sleep*60) * time.Second) // sleep 1 min
		if r.stopping {
			break
		}
	}
}

func (r *DockerContainerLauncherResolver) killContainer(id string) error {
	fmt.Println("killing container", id)
	response, err := r.docker.Post("/containers/"+id+"/kill", nil, nil)
	if err != nil {
		return err
		// log.Fatal("got err", err)
	}
	fmt.Println("KILL CONTAINER RESPONSE:", response)
	return nil
}

func (r *DockerContainerLauncherResolver) stopContainer(id string) error {
	fmt.Println("stopping container", id)
	response, err := r.docker.Post("/containers/"+id+"/stop", nil, nil)
	if err != nil {
		return err
		// log.Fatal("got err", err)
	}
	fmt.Println("STOP CONTAINER RESPONSE:", response)
	return nil
}

func (r *DockerContainerLauncherResolver) removeContainer(id string) error {
	fmt.Println("removing container", id)
	response, err := r.docker.Delete("/containers/"+id, nil)
	if err != nil {
		return err
		// log.Fatal("got err", err)
	}
	fmt.Println("REMOVE CONTAINER RESPONSE:", response)
	return nil
}

func (r *DockerContainerLauncherResolver) dockerRun(image string, internalPort int, label string) (address string, activityNotifier func(), state *DockerContainerState, err error) {

	if r.runningContainers == nil {
		r.runningContainers = map[DockerContainerParams]*DockerContainerState{}
	}

	params := DockerContainerParams{image, internalPort, label}

	var id string
	var externalPort int

	// var state *DockerContainerState

	r.mutex.RLock()
	state, present := r.runningContainers[params]
	if present {
		if state.running || state.starting /* && state.last > 0 */ {
			r.mutex.RUnlock()
			// refresh last run time
			state.last = time.Now().Unix()
			state.busy = true
			if state.running {
				address = fmt.Sprintf("%s:%d", r.docker.host, state.port)
			}
			activityNotifier = func() {
				fmt.Println("got activity 3")
				state.last = time.Now().Unix()
				state.busy = false // TODO: only when EOF or Close()
			}
			return
		} else /* if !state.running */ {
			id = state.id
			externalPort = state.port
		}
		state.starting = true
	} else {
		// state for this container configuration does not exist yet, add it now so that concurrent run calls for this configuration wait for this run to complete
		r.mutex.RUnlock()
		r.mutex.Lock()
		state = &DockerContainerState{id: id, last: time.Now().Unix(), port: externalPort, running: false, starting: true, busy: false}
		r.runningContainers[params] = state
		r.mutex.Unlock()
		r.mutex.RLock()
	}
	r.mutex.RUnlock()

	activityNotifier = func() {
		fmt.Println("got activity")
		state.last = time.Now().Unix()
		state.busy = false // TODO: only when EOF or Close()
	}

	response, err := r.docker.Post("/images/create", &url.Values{"fromImage": []string{image}}, nil)
	if err != nil {
		return
	}
	if response.IsJSON {
		fmt.Println("PULL IMAGE RESPONSE:", response.JSON)
		for {
			var r any
			r, err = response.NextJSON()
			if err != nil {
				return
			}
			if r == nil {
				break
			}
			fmt.Println("PULL IMAGE RESPONSE:", r)
		}
	}

	if len(id) == 0 {

		externalPort = 1024 + rand.Intn(math.MaxUint16-1024)

		fmt.Println("internal port", internalPort, "externalPort", externalPort)

		// response, err = r.docker.Post("/containers/create", &url.Values{"name": []string{"api-test"}}, map[string]interface{}{
		response, err = r.docker.Post("/containers/create", nil, map[string]interface{}{
			"Hostname":     "",
			"Domainname":   "",
			"User":         "",
			"AttachStdin":  false,
			"AttachStdout": false,
			"AttachStderr": false,
			"Tty":          false,
			"OpenStdin":    false,
			"StdinOnce":    false,
			// "Env": []string{
			// 	"MYSQL_ALLOW_EMPTY_PASSWORD=yes",
			// 	"MYSQL_ROOT_PASSWORD=123123",
			// },
			"Image": image,
			"HostConfig": map[string]interface{}{
				"PortBindings": map[string]interface{}{
					fmt.Sprintf("%d/tcp", internalPort): []interface{}{map[string]string{"HostPort": strconv.Itoa(externalPort)}},
				},
			},
			"ExposedPorts": map[string]interface{}{
				fmt.Sprintf("%d/tcp", internalPort): map[string]string{},
			},
		})
		if err != nil {
			return
		}
		fmt.Println("CREATE CONTAINER RESPONSE:", response)
		if !response.IsJSON {
			err = fmt.Errorf("invalid response from dcoker create container API")
			return
		}

		resp := response.JSON.(map[string]interface{})
		id = resp["Id"].(string)
	}

	fmt.Println("starting container")
	response, err = r.docker.Post("/containers/"+id+"/start", nil, nil)
	if err != nil {
		return
	}

	// update state
	// TODO: lock mutex properly
	state.id = id
	state.last = time.Now().Unix()
	state.port = externalPort
	state.starting = false
	state.running = true
	state.busy = true

	address = fmt.Sprintf("%s:%d", r.docker.host, externalPort)

	return
}

func (r *DockerContainerLauncherResolver) resolver(buff []byte) (conn io.ReadWriteCloser, reverseReplacer ReplacerFunc, err error) {

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

	// search for end of headers position
	endOfHeaders := bytes.Index(buff, []byte("\r\n\r\n"))
	if endOfHeaders == -1 {
		return
	}

	// read all headers and parse cookies
	headerLines := strings.Split(string(buff), "\r\n")
	headerLines = headerLines[1:] // exclude request line

	headers := http.Header{}

	// parse headers
	for _, line := range headerLines {
		if len(line) == 0 {
			continue
		}
		kv := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		headers.Add(key, value)

		// fmt.Println(key, ":", value)
	}

	log.Println("Got HTTP method:", parts[0])
	log.Println("Got HTTP path:", parts[1])
	log.Println("Got HTTP version:", parts[2])
	log.Println("Headers", headers)

	// TODO: choose target here

	remoteAddress := ""

	requestPath := parts[1]

	var ref *url.URL

	referer := headers.Get("Referer")
	if len(referer) > 0 {
		ref, _ = url.Parse(referer)
	}

	var activityNotifier func()
	var state *DockerContainerState

	// http://194.8.1.235:8888/x-selmaproject-tts-777-5002/
	// selmaproject/tts:777 with external port 8765
	if strings.HasPrefix(requestPath, "/x:") {

		pathParts := strings.Split(requestPath, "/")
		fmt.Println(pathParts)
		ps := strings.Split(pathParts[1], ":")

		if len(ps) < 5 {
			err = fmt.Errorf("invalid dynamic image request")
			return
		}

		label := ""
		image := fmt.Sprintf("%s/%s:%s", ps[1], ps[2], ps[3])
		var port int
		port, err = strconv.Atoi(ps[4])
		if err != nil {
			err = fmt.Errorf("invalid internal port: %v", err)
			return
		}

		if len(ps) > 5 {
			label = ps[5]
		}

		remoteAddress, activityNotifier, state, err = r.dockerRun(image, port, label)
		if err != nil {
			log.Printf("docker run error: %v", err)
			return
		}

		fmt.Println("remote address:", remoteAddress)

		for {
			time.Sleep(1 * time.Second)
			if state.starting {
				fmt.Println("waiting for container to start")
				continue
			}
			if len(remoteAddress) == 0 {
				remoteAddress = fmt.Sprintf("%s:%d", r.docker.host, state.port) // dirty hack
			}
			fmt.Println("trying")
			cn, er := net.Dial("tcp", remoteAddress)
			if er != nil {
				fmt.Println("!!! remote conn filed", er)
				break
				// return
			}

			// x, er := cn.Write([]byte("HELLO"))
			// if er != nil {
			// 	fmt.Println("!!!!! remote conn write filed", er)
			// 	return
			// }
			// fmt.Println("written", x)
			cn.SetReadDeadline(time.Now().Add(time.Second * 1))

			bf := make([]byte, 1024)
			x, er := cn.Read(bf)
			// fmt.Println(er)
			if er == io.EOF {
				cn.Close()
				continue
			}
			if er != nil {
				fmt.Println("remote conn read filed", er)
				// return
			}
			fmt.Println("read", x)
			cn.Close()
			break
		}

		// reverseReplacer = createReplacer("/api/tts~/" + pathParts[1] + "/api/tts")
		// reverseReplacer = createReplacer("/api/tts~/tts/api/tts")

		parts[1] = "/" + strings.Join(pathParts[2:], "/")

	} else if ref != nil && (len(ref.Host) == 0 || ref.Host == headers.Get("Host")) && strings.HasPrefix(ref.Path, "/x:") {

		fmt.Println("referer mode")

		pathParts := strings.Split(ref.Path, "/")
		fmt.Println(pathParts)
		ps := strings.Split(pathParts[1], ":")

		if len(ps) < 5 {
			err = fmt.Errorf("invalid dynamic image request")
			return
		}

		label := ""
		image := fmt.Sprintf("%s/%s:%s", ps[1], ps[2], ps[3])
		var port int
		port, err = strconv.Atoi(ps[4])
		if err != nil {
			err = fmt.Errorf("invalid internal port: %v", err)
			return
		}

		if len(ps) > 5 {
			label = ps[5]
		}

		remoteAddress, activityNotifier, state, err = r.dockerRun(image, port, label)
		if err != nil {
			log.Printf("docker run error: %v", err)
			return
		}

		fmt.Println("remote address:", remoteAddress)

		for {
			time.Sleep(1 * time.Second)
			if state.starting {
				fmt.Println("waiting for container to start")
				continue
			}
			if len(remoteAddress) == 0 {
				remoteAddress = fmt.Sprintf("%s:%d", r.docker.host, state.port) // dirty hack
			}
			fmt.Println("trying")
			cn, er := net.Dial("tcp", remoteAddress)
			if er != nil {
				fmt.Println("!!! remote conn filed", er)
				break
				// return
			}

			// x, er := cn.Write([]byte("HELLO"))
			// if er != nil {
			// 	fmt.Println("!!!!! remote conn write filed", er)
			// 	return
			// }
			// fmt.Println("written", x)
			cn.SetReadDeadline(time.Now().Add(time.Second * 1))

			bf := make([]byte, 1024)
			x, er := cn.Read(bf)
			// fmt.Println(er)
			if er == io.EOF {
				cn.Close()
				continue
			}
			if er != nil {
				fmt.Println("remote conn read filed", er)
				// return
			}
			fmt.Println("read", x)
			cn.Close()
			break
		}

		// TODO: update referrer ?
		// parts[1] = "/" + strings.Join(pathParts[2:], "/")

	} else {
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
		err = fmt.Errorf("remote connection filed: %v", err)
		return
	}

	// write modified header to the target
	log.Println("sending:", strings.Join(parts, " "))
	_, err = conn.Write([]byte(strings.Join(parts, " ")))
	if err != nil {
		err = fmt.Errorf("write filed: %v", err)
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
		err = fmt.Errorf("write filed: %v", err)
		// p.err("Write failed '%s'\n", err)
		return
	}

	// TODO: wrap conn to notify on read activity
	if activityNotifier != nil {
		conn = &ReadNotifier{conn, activityNotifier}
	}

	return
}

type ReadNotifier struct {
	inner    io.ReadWriteCloser
	notifier func()
}

func (r *ReadNotifier) Read(buff []byte) (n int, err error) {
	n, err = r.inner.Read(buff)
	if r.notifier != nil && n > 0 && err == nil {
		r.notifier()
	}
	return
}

func (r *ReadNotifier) Write(buff []byte) (n int, err error) {
	return r.inner.Write(buff)
}

func (r *ReadNotifier) Close() error {
	return r.inner.Close()
}

var remoteHosts []RemoteHost

func main() {

	var port int = 8888
	var secure bool = false
	var keyPath string = "./certs/localhost-key.pem"
	var certPath string = "./certs/localhost.pem"
	var dockerHost string = "unix:///var/run/docker.sock"
	var startTimeout float64 = 5
	var stopTimeout float64 = 1
	var containerTimeout float64 = 60
	var sleepTimeout float64 = 1
	var jobTimeout float64 = 2

	flag.IntVar(&port, "port", port, "port number")
	flag.IntVar(&port, "p", port, "port number")
	flag.BoolVar(&secure, "s", secure, "use TLS")
	flag.BoolVar(&secure, "tls", secure, "use TLS")
	flag.StringVar(&keyPath, "key", keyPath, "private key path")
	flag.StringVar(&certPath, "cert", certPath, "certificate path")
	flag.StringVar(&dockerHost, "docker", dockerHost, "set path to docker engine, e.g., unix:///var/run/docker.sock, ssh://user@remote-host or http://remote-host")
	// flag.Float64Var(&startTimeout, "start", startTimeout, "start delay")
	flag.Float64Var(&jobTimeout, "timeout", jobTimeout, "job timeout")
	flag.Float64Var(&stopTimeout, "stop", stopTimeout, "container stop timeout")
	flag.Float64Var(&containerTimeout, "remove", containerTimeout, "container remove timeout")
	flag.Float64Var(&sleepTimeout, "sleep", sleepTimeout, "sleep timeout")

	flag.Parse()

	var err error

	rand.Seed(time.Now().UnixNano())

	docker, err := NewDocker(dockerHost)
	if err != nil {
		log.Fatalf("docker error: %v", err)
		return
	}
	resolver := DockerContainerLauncherResolver{docker: docker, mutex: &sync.RWMutex{}, startTimeout: startTimeout}
	go resolver.PeriodicCleanup(jobTimeout, stopTimeout, containerTimeout, sleepTimeout)

	remoteHosts, err = load("proxy.yaml")
	log.Println("got remote hosts", remoteHosts, err)

	connid := uint64(0)

	proxyAddress := fmt.Sprintf(":%d", port) // ":8888"

	var listener net.Listener
	var certificates []tls.Certificate = make([]tls.Certificate, 0, 10)

	if secure {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			log.Fatalf("failed to load certificate: %s", err)
		}
		certificates = append(certificates, cert)

		config := &tls.Config{
			InsecureSkipVerify: true,
			// Certificates:       []tls.Certificate{certificate},
			Certificates: certificates,
			// NextProtos:   []string{"http/1.1"},
		}
		listener, err = tls.Listen("tcp", proxyAddress, config)
	} else {
		listener, err = net.Listen("tcp", proxyAddress)
	}
	if err != nil {
		// logger.Warn("Failed to open local port to listen: %s", err)
		log.Fatalf("Failed to open local port to listen: %s", err)
		// os.Exit(1)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			// logger.Warn("Failed to accept connection '%s'", err)
			log.Printf("Failed to accept connection '%s'", err)
			continue
		}
		connid++

		var p *DynamicReverseProxy
		// if *unwrapTLS {
		// 	logger.Info("Unwrapping TLS")
		// 	p = proxy.NewTLSUnwrapped(conn, laddr, raddr, *remoteAddr)
		// } else {
		// 	p = proxy.New(conn, laddr, raddr)
		// }
		// p = NewDynamicReverseProxy(conn, proxyAddress)
		p = NewDynamicReverseProxy(conn, resolver.resolver)

		// p.Matcher = matcher
		// p.Replacer = replacer

		// p.Nagles = false
		// p.OutputHex = true
		// p.Log = ColorLogger{
		// 	Verbose:     false,
		// 	VeryVerbose: false,
		// 	Prefix:      fmt.Sprintf("Connection #%03d ", connid),
		// 	Color:       false,
		// }
		// p.remoteHosts = &remoteHosts

		go p.Start()
	}
}
