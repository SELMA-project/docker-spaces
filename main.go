package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net"
)

var log LevelLoggerCompatible = NewCompatibleDefaultLevelLogger()

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
	flag.IntVar(&startPort, "start-port", startPort, "start port of docker containers port range")
	flag.IntVar(&sourceSlots, "source", sourceSlots, "number of source (TCP) slots")
	flag.IntVar(&targetSlots, "target", targetSlots, "number of target (docker) slots")

	flag.Parse()

	var err error

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
		log.Fatalf("Failed to open local port to listen: %s", err)
	}

	dockerAuth := &DockerAuth{Username: registryUsername, Password: registryPassword, Email: registryEmail, ServerAddress: registryAddress}

	broker := NewBroker(sourceSlots, targetSlots)

	broker.SourceName = "TCP"
	broker.TargetName = "Docker"

	nextContainerPort := startPort

	dockerRunners := make([]*DockerRunner, 0, targetSlots)

	log.Info("creating docker runners")

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

		dockerRunner := NewDockerRunner(docker, nextContainerPort)
		nextContainerPort++

		dockerRunners = append(dockerRunners, dockerRunner)

		go dockerRunner.Run(dockerSlot)
	}

	// start broker
	go broker.Run()

	resolver := BrokerTargetResolver{broker}

	for {
		conn, err := listener.Accept()
		if err != nil {
			// logger.Warn("Failed to accept connection '%s'", err)
			log.Error("Listener failed to accept connection: %s", err)
			continue
		}

		p := NewDynamicReverseProxy(conn, &resolver)

		p.CORS = enableCORS

		go p.Start(broker)
	}
}
