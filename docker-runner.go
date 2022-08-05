package main

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"
)

type DockerRunner struct {
	docker        *Docker
	stop          bool
	containerPort int
	image         string
}

func NewDockerRunner(dockerHost *Docker, containerPort int) *DockerRunner {
	return &DockerRunner{docker: dockerHost, containerPort: containerPort}
}

func (r *DockerRunner) getContainerID() (id string, err error) {

	response, err := r.docker.Get("/containers/json", &url.Values{"all": []string{"false"}}, nil)
	if err != nil {
		return
	}
	defer response.Close()

	if !response.IsJSON {
		err = fmt.Errorf("get container id: docker list containers: invalid response, not a JSON, got: %v", response)
		return
	}

	externalPort := r.containerPort

	if containers, ok := response.JSON.([]any); ok {
		for _, container := range containers {
			if container, ok := container.(map[string]any); ok {
				if ports, ok := container["Ports"]; ok && ports != nil {
					if ports, ok := ports.([]any); ok {
						for _, portConf := range ports {
							if portConf, ok := portConf.(map[string]any); ok {
								if publicPort := portConf["PublicPort"]; publicPort != nil {
									if publicPort, ok := publicPort.(float64); ok {
										if publicPort := int(publicPort); true {
											// log.Trace("docker-runner: get container id:", container["Id"].(string), "port:", publicPort)
											if publicPort == externalPort {
												id = container["Id"].(string)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return
}

func (r *DockerRunner) kill() (err error) {

	id, err := r.getContainerID()
	if err != nil {
		log.Error("docker-runner: kill: error determining running container id:", err)
		err = fmt.Errorf("kill: error getting container id: %w", err)
		return
	}

	if len(id) == 0 {
		return
	}

	log.Info("docker-runner: kill: killing container", id)

	response, err := r.docker.Post("/containers/"+id+"/kill", nil, nil, nil)
	if err != nil {
		log.Error("docker-runner: kill: got error:", err)
		err = fmt.Errorf("kill: error killing container with id = %s: %w", id, err)
		return
	}
	defer response.Close()

	log.Trace("docker-runner: kill: response:", response)

	return
}

func (r *DockerRunner) start(image string, internalPort int) (err error) {

	// check if some container is using our external port, if so - kill it

	err = r.kill()
	if err != nil {
		err = fmt.Errorf("start: error killing previous container: %w", err)
		return
	}

	// start a new container

	var id string

	// pull the image first
	response, err := r.docker.Post("/images/create", &url.Values{"fromImage": []string{image}}, nil, nil)
	if err != nil {
		return
	}
	if response.IsJSON {
		log.Info("docker-runner: start: pulling image", image)
		log.Trace("docker-runner: start: pull response:", response.JSON)
		for {
			var r any
			r, err = response.NextJSON()
			if err != nil {
				response.Close()
				err = fmt.Errorf("start: error reading pull response: %w", err)
				return
			}
			if r == nil {
				break
			}
			log.Trace("docker-runner: start: pull response:", r)
		}
	}
	response.Close()

	externalPort := r.containerPort

	// response, err = r.docker.Post("/containers/create", &url.Values{"name": []string{"api-test"}}, map[string]interface{}{
	response, err = r.docker.Post("/containers/create", nil, nil, map[string]interface{}{
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
			"AutoRemove": true,
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

	log.Trace("docker-runner: start: create container response:", response)

	if response.StatusCode != 201 {
		err = fmt.Errorf("start: unable to create container, docker engine returned %d status code", response.StatusCode)
		return
	}

	if !response.IsJSON {
		err = fmt.Errorf("start: invalid response from docker engine API: response is not JSON, response: %+v", response)
		response.Close()
		return
	}

	resp := response.JSON.(map[string]interface{})

	if _id, present := resp["Id"]; present {
		id = _id.(string)
	} else {
		err = fmt.Errorf("start: invalid response from docker engine API: id field is missing")
		response.Close()
		return
	}

	response.Close()

	response, err = r.docker.Post("/containers/"+id+"/start", nil, nil, nil)
	if err != nil {
		// TODO: auto or manual remove?
		return
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err = fmt.Errorf("start: docker start container returned status: %s", response.Status)
	}

	response.Close()

	return
}

func waitForConnection(address string, sleep, timeout, readTimeout time.Duration) (err error) {

	// check that docker container ir ready to accept tcp connections

	log.Info("docker-runner: wait: waiting for remote connection ")

	buff := make([]byte, 16)

	deadline := time.Now().Add(timeout)

	var conn net.Conn

	for time.Now().Before(deadline) {

		time.Sleep(sleep)

		fmt.Printf(".")

		conn, err = net.Dial("tcp", address)
		if err != nil {
			// remote connection failed, try again
			continue
		}

		conn.SetReadDeadline(time.Now().Add(readTimeout))

		_, err = conn.Read(buff)

		conn.Close()

		if err == io.EOF {
			continue
		}

		if err != nil {
			// we did not send anything, so it may fail
		}

		err = nil

		break
	}

	fmt.Println()

	return
}

func (r *DockerRunner) wait() {

	// TODO: timeouts

	// check that docker container ir ready to accept tcp connections

	remoteAddress := fmt.Sprintf("%s:%d", r.docker.host, r.containerPort)

	log.Info("docker-runner: wait: connecting to remote container")

	// time.Sleep(10 * time.Second)
	// TODO: give up after some time and try count
	for {
		time.Sleep(1 * time.Second)

		log.Trace("docker-runner: wait: trying to connect to remote container")
		cn, er := net.Dial("tcp", remoteAddress)
		if er != nil {
			log.Trace("docker-runner: wait: remote connection failed:", er)
			continue
		}
		// successful connection?

		// x, er := cn.Write([]byte("HELLO"))
		// if er != nil {
		// 	log.Trace("docker-runner: wait: remote connection write failed:", er)
		// 	return
		// }
		// log.Trace("docker-runner: wait: written to remote connection", x, "bytes")
		cn.SetReadDeadline(time.Now().Add(time.Second * 1))

		bf := make([]byte, 1024)
		x, er := cn.Read(bf)
		// fmt.Println(er)
		if er == io.EOF {
			cn.Close()
			continue
		}
		if er != nil {
			// it's ok to fail to read anything - we didn't send anything
			log.Trace("docker-runner: wait: remote connection read failed:", er)
			// return
		}
		log.Trace("docker-runner: wait: read from remote connection", x, "bytes")
		cn.Close()
		break
	}

	return
}

func (r *DockerRunner) Shutdown() (err error) {

	r.stop = true

	// kill running container

	err = r.kill()
	if err != nil {
		err = fmt.Errorf("shutdown: error killing previous container: %v", err)
		return
	}

	return
}

func (r *DockerRunner) Run(slot *BrokerSlot) {

	log.Info("docker-runner: run: starting docker runner")

	remoteAddress := fmt.Sprintf("%s:%d", r.docker.host, r.containerPort)

	// init
	slot.Send(NewBrokerMessage(BrokerMessageFree, remoteAddress)) // refInfo = remoteAddress

	for !r.stop {
		// log.Trace("docker-runner: run: loop")

		message := slot.Read() // TODO: block here
		// TODO: wait or block at slot.Read(), add timeout?

		log.Trace("docker-runner: run: got message:", message)

		switch message.Type() {
		case BrokerMessageStart:
			// imageAndInternalPort := message.PayloadString()

			containerInfo := message.Payload().(*DockerContainerInfo)

			log.Trace("docker-runner: run: start message container info:", containerInfo)

			log.Info("docker-runner: run: starting container")

			err := r.start(containerInfo.image, containerInfo.port)
			if err != nil {
				log.Debug("docker-runner: run: start container error:", err)
				slot.Send(NewBrokerMessage(BrokerMessageError, err.Error()))
				break
			}

			// wait for responding container state
			// r.wait()
			waitForConnection(remoteAddress, 1*time.Second, 5*time.Minute, 1*time.Second)

			// success
			slot.Send(NewBrokerMessage(BrokerMessageStarted, nil)) // parameter?
		}

	}
}

/*
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
*/
