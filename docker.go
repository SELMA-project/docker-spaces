package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"

	// "log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

type DockerAuth struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	Email         string `json:"email"`
	ServerAddress string `json:"serveraddress"` // defaults (by remote engine) to https://registry-1.docker.io/v2/
}

func (a *DockerAuth) Encode() string {

	authJSON, err := json.Marshal(a)
	if err != nil {
		return ""
	}

	authData := make([]byte, base64.StdEncoding.EncodedLen(len(authJSON)))
	base64.StdEncoding.Encode(authData, authJSON)

	return string(authData)
}

type JSONDecoderStream struct {
	decoder *json.Decoder
	closer  io.Closer
}

func NewJSONDecoderStream(r io.Reader) (stream *JSONDecoderStream) {

	stream = &JSONDecoderStream{json.NewDecoder(r), nil}

	if closer, ok := r.(io.Closer); ok {
		stream.closer = closer
	}

	return
}

func (s *JSONDecoderStream) Decode() (data interface{}, err error) {
	err = s.decoder.Decode(&data)
	if err == io.EOF {
		if s.closer != nil {
			s.closer.Close()
		}
		data = nil
		err = nil
	} else if err != nil {
		return
	}
	return
}

func (s *JSONDecoderStream) DecodeNext() (data interface{}, err error) {
	return s.Decode()
}

func (s *JSONDecoderStream) Next() (data interface{}, err error) {
	return s.Decode()
}

func (s *JSONDecoderStream) NextJSON() (result string, err error) {
	var data interface{}
	data, err = s.Next()
	if err != nil {
		return
	}
	if data == nil {
		return "", nil
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	return string(b), nil
}

func (s *JSONDecoderStream) DecodeAll() (all []interface{}, err error) {
	all = make([]interface{}, 0, 1)
	for {
		var data interface{}
		data, err = s.Decode()
		if err != nil {
			return
		}
		if data == nil {
			break
		}
		all = append(all, data)
	}
	return
}

func (s *JSONDecoderStream) String() string {
	data, err := s.DecodeAll()
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%+v", data)
}

type DockerResponse struct {
	Status      string
	StatusCode  int
	Header      http.Header
	ContentType string
	BodyReader  io.ReadCloser
	Body        []byte
	JSON        interface{}
	JSONs       []interface{}
	IsJSON      bool
	decoder     *JSONDecoderStream
}

func NewDockerResponse(r *http.Response) (response *DockerResponse, err error) {

	response = &DockerResponse{}
	response.Status = r.Status
	response.StatusCode = r.StatusCode
	response.Header = r.Header
	response.BodyReader = r.Body
	response.ContentType = r.Header.Get("Content-Type")
	response.IsJSON = response.ContentType == "application/json"

	// NOTE: response (body) must be read till end in case of using HTTP streaming over socket or SSH

	// fmt.Println("RESPONSE CONTENT LENGTH", r.ContentLength, "ENCODING", r.TransferEncoding)
	// in case of stream: r.ContentLength == -1 and r.TransferEncoding == ["chunked"]

	if response.IsJSON {
		response.decoder = NewJSONDecoderStream(r.Body)
		_, err = response.NextJSON()
	}

	return response, err
}

func (r *DockerResponse) Close() {
	// NOTE: because for serial connections (unix sockets, over ssh) all response (body) data must be read before next request,
	// it is not safe to use one docker serial connection from multiple goroutines without any synchronization

	// ensure the body is read entirely
	// fmt.Println("reading all body")
	io.ReadAll(r.BodyReader) // read till end of body
	// fmt.Println("closing body reader")
	r.BodyReader.Close()
	// fmt.Println("body reader done")
}

// typical docker response headers example
// map[Api-Version:[1.41] Date:[Sun, 29 May 2022 04:31:49 GMT] Docker-Experimental:[true] Ostype:[linux] Server:[Docker/20.10.14 (linux)]]
var dockerResponseHideHeaders map[string]bool = map[string]bool{"Api-Version": true, "Date": true, "Docker-Experimental": true, "Ostype": true, "Server": true, "Content-Type": true}

func (r *DockerResponse) String() string {
	headers := http.Header{}
	for name, values := range r.Header {
		if hide, ok := dockerResponseHideHeaders[name]; ok && hide {
			continue
		}
		headers[name] = values
	}

	if r.IsJSON {
		result, err := r.AllJSONs()
		if err != nil {
			return ""
		}
		return fmt.Sprintf("%s H(%v) %v", r.Status, headers, result)
	}
	return fmt.Sprintf("%s H(%v)", r.Status, headers)
}

func (r *DockerResponse) ReadAll() (result []byte, err error) {
	r.Body, err = ioutil.ReadAll(r.BodyReader)
	return r.Body, err
}

func (r *DockerResponse) NextJSON() (result interface{}, err error) {
	// decode directly from BodyReader or from Body
	result, err = r.decoder.Decode()
	if err != nil {
		return nil, err
	}
	if r.JSON == nil {
		r.JSON = result
	}
	if r.JSONs == nil {
		r.JSONs = []interface{}{result}
	} else {
		r.JSONs = append(r.JSONs, result)
	}
	return
}

func (r *DockerResponse) AllJSONs() (result []interface{}, err error) {
	if r.JSONs != nil && len(r.JSONs) > 1 {
		return r.JSONs, nil
	}
	for {
		nextJSON, err := r.NextJSON()
		if err != nil {
			return nil, err
		}
		if nextJSON == nil {
			break
		}
		// if nextJSON != nil && err == nil {
		// 	r.JSONs = append(r.JSONs, nextJSON)
		// } else {
		// 	break
		// }
	}
	return r.JSONs, nil
}

type dockerDialAddr struct {
}

func (a *dockerDialAddr) Network() string {
	return ""
}

func (a *dockerDialAddr) String() string {
	return ""
}

type dockerPipeConn struct {
	stdinPipe  io.WriteCloser
	stdoutPipe io.ReadCloser
}

func (c *dockerPipeConn) Read(b []byte) (n int, err error) {
	return c.stdoutPipe.Read(b)
}

func (c *dockerPipeConn) Write(b []byte) (n int, err error) {
	return c.stdinPipe.Write(b)
}

func (c *dockerPipeConn) Close() error {
	c.stdinPipe.Close()
	c.stdoutPipe.Close()
	return nil
}

func (c *dockerPipeConn) LocalAddr() net.Addr {
	return &dockerDialAddr{}
}

func (c *dockerPipeConn) RemoteAddr() net.Addr {
	return &dockerDialAddr{}
}

func (c *dockerPipeConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *dockerPipeConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *dockerPipeConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type dockerReconnectingPipeConn struct {
	dockerPipeConn
	cmd  *exec.Cmd
	host string
	port string
}

func (c *dockerReconnectingPipeConn) connect() (err error) {

	if c.cmd != nil {
		// if c.cmd.Stderr != nil {
		// 	c.cmd.Stderr.Close()
		// }
		// if c.cmd.Stdin != nil {
		// 	c.cmd.Stdin.Close()
		// }
		// if c.cmd.Stdout != nil {
		// 	c.cmd.Stdout.Close()
		// }
		// log.Printf("SSH DOCKER HOST")
		err := c.cmd.Process.Kill()
		if err != nil {
			log.Printf("docker: connect: SSH remote docker host process kill error: %v", err)
		}
		err = c.cmd.Process.Release()
		if err != nil {
			log.Printf("docker: connect: SSH remote docker host process release error: %v", err)
		}
	}
	c.dockerPipeConn.stdinPipe, c.dockerPipeConn.stdoutPipe, c.cmd, err = connectSSHpipe(c.host, c.port)

	/*
		if c.cmd != nil {
			log.Printf("[0] docker pipe connect, cmd: %+v | %+v", c.cmd.Process, c.cmd.ProcessState)
			log.Printf("[0] docker pipe propccess state: %+v", c.cmd.ProcessState)
			if c.cmd.ProcessState != nil {
				log.Printf("[0] docker pipe process state exited: %+v", c.cmd.ProcessState.Exited())
			}
		}
		if c.cmd == nil || (c.cmd.ProcessState != nil && c.cmd.ProcessState.Exited()) {
			log.Printf("connecting to SSH")
			c.dockerPipeConn.stdinPipe, c.dockerPipeConn.stdoutPipe, c.cmd, err = connectSSHpipe(c.host, c.port)
			log.Printf("[1] docker pipe connect, cmd: %+v | %+v", c.cmd.Process, c.cmd.ProcessState)
			log.Printf("[1] docker pipe propccess state: %+v", c.cmd.ProcessState)
			if c.cmd.ProcessState != nil {
				log.Printf("[1] docker pipe process state exited: %+v", c.cmd.ProcessState.Exited())
			}
			if err != nil {
				return
			}
		}
	*/

	return
}

func NewDockerConnection(host, port string) *dockerReconnectingPipeConn {
	return &dockerReconnectingPipeConn{host: host, port: port}
}

func connectSSHpipe(host string, port string) (stdin io.WriteCloser, stdout io.ReadCloser, cmd *exec.Cmd, err error) {

	args := []string{}
	if len(port) > 0 {
		args = append(args, "-p")
		args = append(args, port)
	}
	args = append(args, host)
	args = append(args, "docker system dial-stdio")

	// TODO: configure ssh binary path
	// TODO: configure remote docker binary path

	cmd = exec.Command("ssh", args...)

	stdin, err = cmd.StdinPipe()
	if err != nil {
		err = fmt.Errorf("SSH pipe stdin: %v", err)
		// log.Printf("SSH pipe stdin pipe error: %v", err)
		return
	}
	stdout, err = cmd.StdoutPipe()
	if err != nil {
		err = fmt.Errorf("SSH pipe stdout: %v", err)
		// log.Printf("SSH pipe stdout pipe error: %v", err)
		return
	}

	err = cmd.Start()
	if err != nil {
		err = fmt.Errorf("SSH pipe: %v", err)
	}

	// // simulate stdin close
	// go func() {
	// 	time.Sleep(time.Minute * 1)
	// 	fmt.Println("CLOSING SSH STDIN")
	// 	stdin.Close()
	// }()
	//
	// // simulate stdout close
	// go func() {
	// 	time.Sleep(time.Minute * 2)
	// 	fmt.Println("CLOSING SSH STDOUT")
	// 	stdout.Close()
	// }()

	return
}

type Docker struct {
	endpoint   string
	client     *http.Client
	host       string
	authHeader string
}

func NewDocker(endpoint string) (docker *Docker, err error) {

	var transport http.RoundTripper

	u, err := url.Parse(endpoint)

	if u.Scheme == "ssh" {
		// over ssh
		username := u.User.Username()
		host := u.Hostname()
		if len(username) > 0 {
			host = username + "@" + host
		}
		port := u.Port()

		// conn := dockerReconnectingPipeConn{}
		// conn.host = host
		// conn.port = port
		conn := NewDockerConnection(host, port)
		// stdin, stdout, _, err := connectSSHpipe(host, port)
		// if err != nil {
		// 	return nil, err
		// }
		//
		// conn := &dockerPipeConn{stdin, stdout}

		transport = &http.Transport{
			MaxConnsPerHost: 1, // this is critical
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				log.Println("docker SSH dial", network, addr)
				conn.connect() // connect/reconnect if connection is lost
				return conn, nil
			},
		}

		// docker.endpoint = "http://ssh"
		endpoint = "http://ssh-" + u.Host

	} else if u.Scheme == "http" {
		// usual stuff
		// docker.endpoint = endpoint
	} else if (u.Scheme == "" && strings.HasSuffix(u.Path, ".sock")) || u.Scheme == "unix" {
		// unix socket
		transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", u.Path)
			},
		}
		// docker.endpoint = "http://unix"
		endpoint = "http://unix"
	}

	docker = &Docker{}

	docker.endpoint = endpoint
	docker.host = u.Hostname()

	docker.client = &http.Client{Transport: transport}

	return
}

func (d *Docker) SetAuth(auth *DockerAuth) {
	if auth == nil {
		d.authHeader = ""
	} else {
		d.authHeader = auth.Encode()
	}
	return
}

func (d *Docker) callRaw(method string, api string, headers http.Header, contentType string, body io.Reader) (response *http.Response /* , data io.ReadCloser */, err error) {
	url := api
	if len(d.endpoint) > 0 {
		url = d.endpoint + api
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		return
	}
	if headers != nil {
		for key, values := range headers {
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}
	}
	if len(d.authHeader) > 0 {
		request.Header.Add("X-Registry-Auth", d.authHeader)
	}
	if len(contentType) > 0 {
		request.Header.Add("Content-Type", contentType)
	}
	response, err = d.client.Do(request)
	// if err != nil {
	// 	return
	// }
	// if response.StatusCode == 200 {
	// 	return response.Body, nil
	// }
	// fmt.Println(response.Status)
	// return response.Body, err
	return
}

func (d *Docker) Call(method string, api string, headers http.Header, body interface{}) (response *DockerResponse, err error) {
	var sendBody io.Reader
	var contentType string
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		contentType = "application/json"
		sendBody = bytes.NewBuffer(data)
	} else {
		sendBody = http.NoBody
	}
	r, err := d.callRaw(method, api, headers, contentType, sendBody)
	if err != nil {
		return nil, err
	}
	response, err = NewDockerResponse(r)
	// if r.Header.Get("Content-Type") == "application/json" {
	// 	return NewJSONDecoderStream(r.Body), nil
	/*
		decoder := json.NewDecoder(r.Body)
		// err = decoder.Decode(&result)
		for {
			err = decoder.Decode(&result)
			fmt.Println(">>>", result)
			if err != nil {
				if err == io.EOF {
					err = nil
				}
				break
			}
		}
	*/
	// data, err := ioutil.ReadAll(r.Body)
	// if err != nil {
	// 	return nil, err
	// }
	// err = json.Unmarshal(data, &result)
	// fmt.Println(r.Header)
	// fmt.Println(string(data))
	// return result, err
	// }
	// result = r.Body
	return
}

func (d *Docker) Call2(method string, api string, body interface{}) (result interface{}, err error) {
	var sendBody io.Reader
	var contentType string
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		contentType = "application/json"
		sendBody = bytes.NewBuffer(data)
	} else {
		sendBody = http.NoBody
	}
	r, err := d.callRaw(method, api, nil, contentType, sendBody)
	if err != nil {
		return nil, err
	}
	if r.Header.Get("Content-Type") == "application/json" {
		return NewJSONDecoderStream(r.Body), nil
		/*
			decoder := json.NewDecoder(r.Body)
			// err = decoder.Decode(&result)
			for {
				err = decoder.Decode(&result)
				fmt.Println(">>>", result)
				if err != nil {
					if err == io.EOF {
						err = nil
					}
					break
				}
			}
		*/
		// data, err := ioutil.ReadAll(r.Body)
		// if err != nil {
		// 	return nil, err
		// }
		// err = json.Unmarshal(data, &result)
		// fmt.Println(r.Header)
		// fmt.Println(string(data))
		// return result, err
	}
	result = r.Body
	return
}

func (d *Docker) Get(api string, query *url.Values, headers http.Header) (result *DockerResponse, err error) {
	qs := ""
	if query != nil {
		qs = query.Encode()
		if len(qs) > 0 {
			qs = "?" + qs
		}
	}
	return d.Call("GET", api+qs, headers, nil)
}

func (d *Docker) Head(api string, query *url.Values, headers http.Header) (result *DockerResponse, err error) {
	qs := ""
	if query != nil {
		qs = query.Encode()
		if len(qs) > 0 {
			qs = "?" + qs
		}
	}
	return d.Call("HEAD", api+qs, headers, nil)
	// A response header X-Docker-Container-Path-Stat is returned, containing a base64 - encoded JSON object with some filesystem header information about the path.
}

func (d *Docker) Post(api string, query *url.Values, headers http.Header, body interface{}) (result *DockerResponse, err error) {
	qs := ""
	if query != nil {
		qs = query.Encode()
		if len(qs) > 0 {
			qs = "?" + qs
		}
	}
	return d.Call("POST", api+qs, headers, body)
}

func (d *Docker) Put(api string, query *url.Values, headers http.Header, body interface{}) (result *DockerResponse, err error) {
	qs := ""
	if query != nil {
		qs = query.Encode()
		if len(qs) > 0 {
			qs = "?" + qs
		}
	}
	return d.Call("PUT", api+qs, headers, body)
}

func (d *Docker) Delete(api string, query *url.Values, headers http.Header) (result *DockerResponse, err error) {
	qs := ""
	if query != nil {
		qs = query.Encode()
		if len(qs) > 0 {
			qs = "?" + qs
		}
	}
	return d.Call("DELETE", api+qs, headers, nil)
}
