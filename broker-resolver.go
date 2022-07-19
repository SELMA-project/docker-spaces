package main

import (
	"fmt"
	// "log"
	"net/url"
	"strconv"
	"strings"
)

// proxy target resolver via broker

type BrokerTarget struct {
	remoteAddress string
	data          []byte
	activity      func()
	closed        func()
}

func (t *BrokerTarget) RemoteAddress() string {
	return t.remoteAddress
}

func (t *BrokerTarget) HeadData() []byte {
	return t.data
}

func (t *BrokerTarget) Activity() {
	if t.activity != nil {
		t.activity()
	}
	return
}

func (t *BrokerTarget) Closed() {
	if t.closed != nil {
		t.closed()
	}
	return
}

type BrokerTargetResolver struct {
	broker *Broker
}

type DockerContainerInfo struct {
	image string
	port  int
}

func (r *BrokerTargetResolver) parseURLPath(path string) (pathRewrite string, yType bool, info *DockerContainerInfo, err error) {

	// example:
	// http://194.8.1.235:8888/x-selmaproject-tts-777-5002/
	// selmaproject/tts:777 with external port 8765

	pathRewrite = path // defaults to same path, alternative default: return empty string if no rewrite happens

	if strings.HasPrefix(path, "/y:") {
		yType = true
	}

	if !yType && !strings.HasPrefix(path, "/x:") {
		// err = fmt.Errorf("parse-url-path: invalid dynamic run path") // TODO: how to call this
		return
	}

	pathParts := strings.SplitN(path, "/", 3)

	// assert pathParts[0] == "" // not absolute path

	ps := strings.Split(pathParts[1], ":")

	// /x:registry:repo:tag:port/...
	if len(ps) < 5 {
		err = fmt.Errorf("parse-url-path: invalid dynamic run request")
		return
	}

	port, err := strconv.Atoi(ps[4])
	if err != nil {
		err = fmt.Errorf("parse-url-path: invalid dynamic run path: invalid internal port %s: %v", ps[4], err)
		return
	}

	image := fmt.Sprintf("%s/%s:%s", ps[1], ps[2], ps[3])

	info = &DockerContainerInfo{image, port}

	pathRewrite = "/" + pathParts[2]

	return
}

func (r *BrokerTargetResolver) Resolve(buff []byte) (target ResolvedTarget, err error) {

	req, err := ParseHTTPRequest(buff)
	if req == nil {
		return
	}

	log.Debug("broker-resolver: got HTTP request:", req.Method, req.Path, req.Version)
	// log.Debug("broker-resolver: got HTTP Headers", req.Headers)

	var containerInfo *DockerContainerInfo

	// if strings.HasPrefix(req.Path, "/x:") {
	// 	req.Path, containerInfo, err = r.parseURLPath(req.Path)
	// } else if ref != nil && (len(ref.Host) == 0 || ref.Host == req.Headers.Get("Host")) && strings.HasPrefix(ref.Path, "/x:") {
	// 	req.Path, containerInfo, err = r.parseURLPath(ref.Path)
	// }

	yType := false

	req.Path, yType, containerInfo, err = r.parseURLPath(req.Path)
	if err != nil {
		err = fmt.Errorf("broker-resolver: invalid dynamic run path: %w", err)
		return
	}

	if containerInfo == nil {

		// try with referer header

		var ref *url.URL

		referer := req.Headers.Get("Referer")
		if len(referer) > 0 {
			ref, _ = url.Parse(referer)
		}

		if ref != nil && (len(ref.Host) == 0 || ref.Host == req.Headers.Get("Host")) {
			_, yType, containerInfo, err = r.parseURLPath(ref.Path)
			if err != nil {
				err = fmt.Errorf("broker-resolver: invalid dynamic run path: %w", err)
				return
			}
		}
	}

	if containerInfo == nil {
		// not a dynamic run path
		return
	}

	typePrefix := "x:"
	if yType {
		typePrefix = "y:"
	}

	slotType := typePrefix + containerInfo.image
	log.Trace("broker-resolver: got slot type:", slotType)

	slot := r.broker.GetSourceSlot()
	log.Tracef("broker-resolver: got free slot: %+v", slot)

	// slot.Send(NewBrokerMessage(BrokerMessageAcquire, fmt.Sprint("%s:%d", image, port))) // acquire
	slot.Send(NewBrokerMessage(BrokerMessageAcquire, BrokerAcquireMessageData{slotType, containerInfo})) // acquire
	message := slot.Read()

	log.Debug("broker-resolver: TCP end got message:", message)

	var remoteAddress string

	switch message.Type() {
	case BrokerMessageAcquired:
		remoteAddress = message.PayloadString()
	case BrokerMessageError:
		err = fmt.Errorf("borker-resolver: broker error: %v", message.PayloadString())
		return
	default:
		err = fmt.Errorf("broker-resolver: unknown message from broker: %v", message)
		return
	}

	activityNotifier := func() {
		slot.Send(NewBrokerMessage(BrokerMessageRelease, false))
	}

	if len(remoteAddress) == 0 {
		err = fmt.Errorf("broker-resolver: target not found")
		return
	}

	target = &BrokerTarget{remoteAddress: remoteAddress, data: req.Data(true, false, false), activity: nil, closed: activityNotifier}

	return
}
