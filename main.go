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
	id      string
	last    int64
	port    int
	running bool
}

type DockerContainerLauncherResolver struct {
	docker   *Docker // TODO: have multiple docker hosts and a way to choose
	stopping bool
	mutex    *sync.RWMutex

	runningContainers map[DockerContainerParams]*DockerContainerState
}

func (r *DockerContainerLauncherResolver) Stop() {
	r.stopping = true
}

func (r *DockerContainerLauncherResolver) Cleanup(minutes float64) {
	now := time.Now()
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	for params, state := range r.runningContainers {
		last := time.Unix(state.last, 0)
		minutesElapsed := now.Sub(last).Minutes()
		fmt.Println(minutesElapsed, params, state)
		if state.running && minutesElapsed >= minutes /* 1min */ {

			err := r.stopContainer(state.id)
			if err != nil {
				log.Printf("error stopping container: %v", err)
			}

			state.running = false

		} else if !state.running && minutesElapsed >= 60 {

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
		}
	}
}

func (r *DockerContainerLauncherResolver) PeriodicCleanup(minutes float64) {
	if minutes == 0 {
		// for periodic cleanup time interval must be nonzero
		minutes = 1 // defaults to 1 min
	}
	for {
		r.Cleanup(minutes)
		if r.stopping {
			break
		}
		time.Sleep(1 * time.Minute) // sleep 1 min
		if r.stopping {
			break
		}
	}
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

func (r *DockerContainerLauncherResolver) dockerRun(image string, internalPort int, label string) (address string, activityNotifier func(), err error) {

	if r.runningContainers == nil {
		r.runningContainers = map[DockerContainerParams]*DockerContainerState{}
	}

	params := DockerContainerParams{image, internalPort, label}

	var id string
	var externalPort int

	r.mutex.RLock()
	// TODO: how to prevent cleanup during this? expand mutex?
	if state, ok := r.runningContainers[params]; ok {
		if state.running /* && state.last > 0 */ {
			r.mutex.RUnlock()
			// refresh last run time
			state.last = time.Now().Unix()
			address = fmt.Sprintf("%s:%d", r.docker.host, state.port)
			activityNotifier = func() {
				fmt.Println("got activity 3")
				state.last = time.Now().Unix()
			}
			return
		} else /* if !state.running */ {
			id = state.id
			externalPort = state.port
		}
	}
	r.mutex.RUnlock()

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

	r.mutex.RLock()
	if state, ok := r.runningContainers[params]; ok {
		r.mutex.RUnlock()
		// refresh last run time
		state.last = time.Now().Unix()
		state.port = externalPort
		state.running = true
		activityNotifier = func() {
			fmt.Println("got activity 1")
			state.last = time.Now().Unix()
		}
		return
	} else {
		r.mutex.RUnlock()
		r.mutex.Lock()
		state := &DockerContainerState{id: id, last: time.Now().Unix(), port: externalPort, running: true}
		activityNotifier = func() {
			fmt.Println("got activity 2")
			state.last = time.Now().Unix()
		}
		r.runningContainers[params] = state
		r.mutex.Unlock()
	}

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

	log.Println("Got HTTP method:", parts[0])
	log.Println("Got HTTP path:", parts[1])
	log.Println("Got HTTP version:", parts[2])

	// TODO: choose target here

	remoteAddress := ""

	requestPath := parts[1]

	var activityNotifier func()

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

		remoteAddress, activityNotifier, err = r.dockerRun(image, port, label)
		if err != nil {
			log.Printf("docker run error: %v", err)
			return
		}

		fmt.Println("remote address:", remoteAddress)

		// reverseReplacer = createReplacer("/api/tts~/" + pathParts[1] + "/api/tts")
		// reverseReplacer = createReplacer("/api/tts~/tts/api/tts")

		parts[1] = "/" + strings.Join(pathParts[2:], "/")

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
	if r.notifier != nil {
		r.notifier()
	}
	return r.inner.Read(buff)
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

	flag.IntVar(&port, "port", port, "port number")
	flag.IntVar(&port, "p", port, "port number")
	flag.BoolVar(&secure, "s", secure, "use TLS")
	flag.BoolVar(&secure, "tls", secure, "use TLS")
	flag.StringVar(&keyPath, "key", keyPath, "private key path")
	flag.StringVar(&certPath, "cert", certPath, "certificate path")
	flag.StringVar(&dockerHost, "docker", dockerHost, "set path to docker engine, e.g., unix:///var/run/docker.sock, ssh://user@remote-host or http://remote-host")

	flag.Parse()

	var err error

	rand.Seed(time.Now().UnixNano())

	docker, err := NewDocker(dockerHost)
	if err != nil {
		log.Fatalf("docker error: %v", err)
		return
	}
	resolver := DockerContainerLauncherResolver{docker: docker, mutex: &sync.RWMutex{}}
	go resolver.PeriodicCleanup(0)

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
