package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"hash/crc32"
	"net"
	"strconv"
	"strings"
)

var log LevelLoggerCompatible = NewCompatibleDefaultLevelLogger()

// https://stackoverflow.com/a/45494246
// Created so that multiple inputs can be accecpted
type stringFlags []string

func (self *stringFlags) String() string {
	return fmt.Sprintf("%v", *self)
}

func (self *stringFlags) Set(value string) error {
	*self = append(*self, strings.TrimSpace(value))
	return nil
}

func main() {
	var port int = 8888
	var secure bool = false
	var keyPath string = "./certs/localhost-key.pem"
	var certPath string = "./certs/localhost.pem"
	var dockerHost string = "unix:///var/run/docker.sock"
	var registryAddress string
	var registryUsername string
	var registryPassword string
	var registryEmail string
	var enableCORS bool
	var startPort int = 9100
	var sourceSlots int = 3
	var targetSlots int = 5
	var sleepMS int = 2000
	var clusterDefs stringFlags
	var enableGPU bool = false
	var releaseTimeout int = 1800
	var stopTimeout int = 0
	var root string

	// <host-start-port>:<target-slot(docker-runner)-count>:<docker-host-url>

	flag.IntVar(&port, "port", port, "port number")
	flag.IntVar(&port, "p", port, "port number")
	flag.BoolVar(&secure, "s", secure, "use TLS")
	flag.BoolVar(&secure, "tls", secure, "use TLS")
	flag.StringVar(&keyPath, "key", keyPath, "private key path")
	flag.StringVar(&certPath, "cert", certPath, "certificate path")
	flag.StringVar(&dockerHost, "docker", dockerHost, "set path to docker engine, e.g., unix:///var/run/docker.sock, ssh://user@remote-host or http://remote-host")
	flag.StringVar(&registryAddress, "registry", registryAddress, "docker registry address (defaults to hub.docker.com registry)")
	flag.StringVar(&registryUsername, "user", registryUsername, "docker registry username")
	flag.StringVar(&registryEmail, "email", registryEmail, "docker registry email")
	flag.StringVar(&registryPassword, "password", registryPassword, "docker registry password")
	flag.BoolVar(&enableCORS, "cors", enableCORS, "enable CORS")
	flag.BoolVar(&enableGPU, "gpu", enableGPU, "enable GPU")
	flag.IntVar(&startPort, "start-port", startPort, "start port of docker containers port range")
	flag.IntVar(&sourceSlots, "source", sourceSlots, "number of source (TCP) slots")
	flag.IntVar(&targetSlots, "target", targetSlots, "number of target (docker) slots")
	flag.IntVar(&sleepMS, "loop-sleep", sleepMS, "broker loop sleep in milliseconds")
	flag.IntVar(&releaseTimeout, "release", releaseTimeout, "container release timeout in seconds")
	flag.IntVar(&stopTimeout, "stop", stopTimeout, "container stop timeout before kill in seconds")
	flag.Var(&clusterDefs, "cluster", "cluster configuration in following format: <host-start-port>:<target-slot(docker-runner)-count>:<docker-host-url>")
	flag.StringVar(&root, "root", root, "redirect root / to specified host")

	flag.Parse()

	var err error

	proxyAddress := fmt.Sprintf(":%d", port) // ":8888"

	var listener net.Listener
	var certificates []tls.Certificate = make([]tls.Certificate, 0, 10)

	if secure {
		var cert tls.Certificate
		cert, err = tls.LoadX509KeyPair(certPath, keyPath)
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
		log.Fatalf("Failed to open local port to listen: %s", err)
	}

	dockerAuth := &DockerAuth{Username: registryUsername, Password: registryPassword, Email: registryEmail, ServerAddress: registryAddress}

	type Cluster struct {
		dockerHost      string
		startPort       int
		targetSlotCount int
	}

	clusters := make([]*Cluster, 0, len(clusterDefs))

	totalTargetSlotCount := 0

	for _, clusterDef := range clusterDefs {

		ps := strings.SplitN(clusterDef, ":", 3)

		var startPort, targetSlotCount int
		var err error

		startPort, err = strconv.Atoi(ps[0])
		if err != nil {
			log.Fatalf("invalid cluster definition: invalid start port: %w", err)
		}

		targetSlotCount, err = strconv.Atoi(ps[1])
		if err != nil {
			log.Fatalf("invalid cluster definition: invalid target slot count: %w", err)
		}

		cluster := &Cluster{dockerHost: ps[2], startPort: startPort, targetSlotCount: targetSlotCount}
		clusters = append(clusters, cluster)

		totalTargetSlotCount += targetSlotCount

		log.Debugf("got cluster configuration: %+v", cluster)
	}

	if len(clusters) == 0 {
		cluster := &Cluster{dockerHost: dockerHost, startPort: startPort, targetSlotCount: targetSlots}
		clusters = append(clusters, cluster)
		totalTargetSlotCount += targetSlots
		log.Debugf("got cluster configuration: %+v", cluster)
	} else {
		targetSlots = totalTargetSlotCount
	}

	broker := NewBroker(sourceSlots, targetSlots, sleepMS, releaseTimeout)

	broker.SourceName = "TCP"
	broker.TargetName = "Docker"

	// nextContainerPort := startPort

	// dockerRunners := make([]*DockerRunner, 0, targetSlots)

	log.Info("GPU:", enableGPU)

	log.Info("creating docker runners")

	for _, cluster := range clusters {

		log.Trace("creating docker runners for cluster:", cluster.dockerHost)

		nextContainerPort := cluster.startPort
		nextGPUDevice := 0

		for count := 0; count < cluster.targetSlotCount; count++ {

			dockerSlot := broker.GetTargetSlot()
			if dockerSlot == nil {
				break
			}

			docker, err := NewDocker(cluster.dockerHost)
			if err != nil {
				log.Fatalf("docker error: %v", err)
				return
			}

			if len(dockerAuth.Username) > 0 || len(dockerAuth.Email) > 0 {
				docker.SetAuth(dockerAuth)
			}

			if enableGPU {
				nextGPUDevice++
			}

			// for remote docker runner: spawned connection as argument must be provided
			dockerRunner := NewDockerRunner(docker, nextContainerPort, nextGPUDevice, stopTimeout, releaseTimeout)
			nextContainerPort++

			// dockerRunners = append(dockerRunners, dockerRunner)

			go dockerRunner.Run(dockerSlot)
		}
	}

	log.Info("docker runners created")

	// make get target slots blocking again, spawn the loop below for real-time docker-runner introduction (remote vs local?)
	/*
		for {
			dockerSlot := broker.GetTargetSlot()
			if dockerSlot == nil {
				break
			}

			docker, err := NewDocker(dockerHost)
			if err != nil {
				log.Fatalf("docker error: %v", err)
				return
			}

			if len(dockerAuth.Username) > 0 || len(dockerAuth.Email) > 0 {
				docker.SetAuth(dockerAuth)
			}

			// for remote docker runner: spawned connection as argument must be provided
			dockerRunner := NewDockerRunner(docker, nextContainerPort)
			nextContainerPort++

			dockerRunners = append(dockerRunners, dockerRunner)

			go dockerRunner.Run(dockerSlot)
		}
	*/

	// start broker
	go broker.Run()

	// dockerResolver := &DockerTargetResolver{}
	//
	// hostResolver := &HostTargetResolver{}
	//
	// brokerResolver := &BrokerTargetResolver{broker}
	//
	// httpResolver := NewHTTPProtocolTargetResolver(dockerResolver, hostResolver, brokerResolver)

	proxyLogger := NewProxyLogger(log) //.SetLevel(WarningLogLevel)

	// httpResolver := NewHTTPPipeResolver(&ContainerHandler{broker}, hostHandler, &DockerHandler{})

	httpProxyConfiguration := NewHTTPProxyConfiguration(
		&HTTPStaticHostHandler{ID: strconv.Itoa(int(crc32.ChecksumIEEE([]byte(proxyAddress))))},
		&HTTPContainerHandler{broker: broker, ID: strconv.Itoa(int(crc32.ChecksumIEEE([]byte(proxyAddress))))},
		&HTTPDockerLocalHandler{},
	)

	connectionCounter := 0

	for {
		conn, err := listener.Accept()
		if err != nil {
			// logger.Warn("Failed to accept connection '%s'", err)
			log.Error("Listener failed to accept connection: %s", err)
			continue
		}

		connectionCounter++

		info := &ProxyConnInfo{TLS: secure, ID: strconv.Itoa(connectionCounter)}

		p := NewDynamicReverseProxy2(proxyLogger.Derive().SetSource(info.ID), httpProxyConfiguration.NewProxy)

		go p.Run(conn, info)
	}
}
