package main

import (
	"encoding/json"
	"fmt"

	// "log"
	"time"
)

/*
Broker has two sets of slots: source slots and target slots
Purpose of the broker is to connect/link source slots with target slots so that
tasks from source slots can be passed to workers at target slots. Each slot has two
sides internal and external. The broker connects/links source and target slot internal sides,
whereas slot external sides represent the broker external API.

Each broker slot has 3 states, slightly different names for source and for target slots.
Source slot has following states:
- Free - job does not have any task associated
- Wait - job awaits for target slot to be linked to and the processing resource to be attached
- Run - job is processing at the worker connected to the linked target slot
Target slot has following states:
- Free - worker (if any) associated with the target slot is not processing any data
- Start - worker is starting up
- Run - worker is processing a task
*/

type BrokerSlotState int

const (
	BrokerSlotStateFree BrokerSlotState = iota
	BrokerSlotStateWait
	BrokerSlotStateRun
	BrokerSlotStateReset
	BrokerSlotStateStarting
)

func (s BrokerSlotState) String() string {
	return [...]string{"Free", "Wait", "Run", "Reset", "Starting"}[s]
}

type BrokerMessageType int

const (
	BrokerMessageStart BrokerMessageType = iota
	BrokerMessageStarted
	BrokerMessageError
	BrokerMessageAcquire
	BrokerMessageAcquired
	BrokerMessageFree
	BrokerMessageRelease
)

func (m BrokerMessageType) String() string {
	return [...]string{"Start", "Started", "Error", "Acquire", "Acquired", "Free", "Release"}[m]
}

type BrokerAcquireMessageData struct {
	SlotType string
	Payload  any
}

type BrokerMessage struct {
	t       BrokerMessageType
	payload any
}

func NewBrokerMessage(t BrokerMessageType, payload any) *BrokerMessage {
	return &BrokerMessage{t, payload}
}

func (m *BrokerMessage) Type() BrokerMessageType {
	return m.t
}

func (m *BrokerMessage) Payload() any {
	return m.payload
}

func (m *BrokerMessage) PayloadString() string {
	return m.payload.(string)
}

func (m *BrokerMessage) PayloadInt() int {
	return m.payload.(int)
}

func (m *BrokerMessage) PayloadBool() bool {
	return m.payload.(bool)
}

func (m *BrokerMessage) AcquirePayload() BrokerAcquireMessageData {
	return m.payload.(BrokerAcquireMessageData)
}

func (m *BrokerMessage) String() string {
	return fmt.Sprintf("BrokerMessage{%s, %+v}", m.t, m.payload)
}

// type BrokerSlotState struct {
// 	state    string
// 	since    int64
// 	image    string
// 	tcpIndex int
// }

type BrokerSlot struct {
	index int // for debugging
	// broker private fields
	input  chan *BrokerMessage // from broker's perspective
	output chan *BrokerMessage // from broker's perspective
	// index  int
	// state  BrokerSlotState
	state    BrokerSlotState
	since    int64
	slotType string // for slot matching: kind, slotMatchKey
	// image    string // left to right: run info
	// remoteAddress string // right to left: instance info
	runInfo any // image for docker containers: image and port, maybe host
	refInfo any // remoteAddress instance info, reference info

	// oppositeIndex int // TODO: oppositeSlot -> *BrokerSlot
	oppositeSlot *BrokerSlot
}

func (s *BrokerSlot) JSON() any {
	oppositeSlotIndex := -1
	if s.oppositeSlot != nil {
		oppositeSlotIndex = s.oppositeSlot.index
	}
	return struct {
		Index             int
		State             string
		Since             int64
		SlotType          string
		RunInfo           any
		RefInfo           any
		OppositeSlotIndex int
		InputQueueLength  int
		OutputQeueLength  int
	}{s.index, s.state.String(), s.since, s.slotType, s.runInfo, s.refInfo, oppositeSlotIndex, len(s.input), len(s.output)}
}

func (s *BrokerSlot) String() string {
	oppositeSlotIndex := -1
	if s.oppositeSlot != nil {
		oppositeSlotIndex = s.oppositeSlot.index
	}
	return fmt.Sprintf("BrokerSlot{%s, since: %d, type: %s, run-info: %#v, ref-info: %#v, op-slot: %d}", s.state, s.since, s.slotType, s.runInfo, s.refInfo, oppositeSlotIndex)
}

// func newBrokerSlot(index, size int /* size = 3 */) *BrokerSlot {
func newBrokerSlot(size int /* size = 3 */) *BrokerSlot {
	// return &BrokerSlot{input: make(chan *BrokerMessage, size), output: make(chan *BrokerMessage, size) /* state: BrokerSlotState{}, */, index: index}
	return &BrokerSlot{input: make(chan *BrokerMessage, size), output: make(chan *BrokerMessage, size) /* state: BrokerSlotState{}, */}
}

// broker internal API
func (s *BrokerSlot) send(message *BrokerMessage) {
	// if len(s.output) < cap(s.output) {
	if len(s.output) == 0 {
		s.output <- message
	}
}

func (s *BrokerSlot) reset() {
	done := false
	for !done {
		select {
		case <-s.output:
			done = false
		default:
			done = true
		}
	}
	// TODO: reset input channel?
}

// broker internal API
func (s *BrokerSlot) read() *BrokerMessage {
	return <-s.input
}

// broker internal API
func (s *BrokerSlot) empty() bool {
	return len(s.input) == 0
}

// API external to Broker
func (s *BrokerSlot) Send(message *BrokerMessage) {
	s.input <- message
}

// API external to Broker
func (s *BrokerSlot) Read() *BrokerMessage {
	return <-s.output
}

type BrokerMessageJSON struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type BrokerMessageDirection int

const (
	BrokerMessageDirectionSend BrokerMessageDirection = iota
	BrokerMessageDirectionRead
)

func (s BrokerMessageDirection) String() string {
	return [...]string{"broker -->", "broker <--"}[s]
}

type BrokerLogEntry struct {
	Message   BrokerMessageJSON `json:"message"`
	Timestamp int64             `json:"timestamp"`
	Direction string            `json:"direction"`
	Slot      any               `json:"slot"`
}

type Broker struct {
	freeSourceSlots chan *BrokerSlot
	freeTargetSlots chan *BrokerSlot
	// source slots
	// target slots
	sourceSlots []*BrokerSlot
	targetSlots []*BrokerSlot
	SourceName  string
	TargetName  string

	LoopSleep      int // in milliseconds
	ReleaseTimeout int // in seconds

	State []byte

	LastBrokerStart int64

	log []*BrokerLogEntry
}

func (b *Broker) AddLogEntry(message *BrokerMessage, slot *BrokerSlot, origin string, direction BrokerMessageDirection) {
	b.log = append(b.log, &BrokerLogEntry{BrokerMessageJSON{message.t.String(), message.payload}, time.Now().Unix(), direction.String() + " " + origin, slot.JSON()})
}

func (b *Broker) JSON() []byte {
	sourceSlots := make([]any, len(b.sourceSlots))
	targetSlots := make([]any, len(b.targetSlots))
	for i, slot := range b.sourceSlots {
		sourceSlots[i] = slot.JSON()
	}
	for i, slot := range b.targetSlots {
		targetSlots[i] = slot.JSON()
	}
	s := struct {
		Now            int64
		LastLoopStart  int64
		SourceSlots    []any
		TargetSlots    []any
		SourceName     string
		TargetName     string
		LoopSleep      int
		ReleaseTimeout int
	}{
		time.Now().Unix(),
		b.LastBrokerStart,
		sourceSlots,
		targetSlots,
		b.SourceName,
		b.TargetName,
		b.LoopSleep,
		b.ReleaseTimeout,
	}
	data, err := json.MarshalIndent(s, "  ", "  ")
	if err != nil {
		log.Error("unable to marshal broker to JSON: %v", err)
	}
	return data
}

func (b *Broker) HistoryJSON() []byte {
	data, err := json.MarshalIndent(b.log, "  ", "  ")
	if err != nil {
		log.Error("unable to marshal broker history to JSON: %v", err)
	}
	return data
}

func NewBroker(sourceSlotCount, targetSlotCount, sleepMS, releaseTimeout int) *Broker {
	freeSourceSlots := make(chan *BrokerSlot, sourceSlotCount)
	freeTargetSlots := make(chan *BrokerSlot, targetSlotCount)
	sourceSlots := make([]*BrokerSlot, sourceSlotCount)
	targetSlots := make([]*BrokerSlot, targetSlotCount)
	for i := 0; i < sourceSlotCount; i++ {
		slot := newBrokerSlot(3)
		slot.index = i
		sourceSlots[i] = slot
		freeSourceSlots <- slot
		slot.state = BrokerSlotStateFree // GB: lai ciklos var atskirt TCPslotus, kas netiek lietoti
	}
	for i := 0; i < targetSlotCount; i++ {
		slot := newBrokerSlot(3)
		slot.index = i
		targetSlots[i] = slot
		freeTargetSlots <- slot
	}
	if sleepMS == 0 {
		sleepMS = 1
	}
	if releaseTimeout == 0 {
		releaseTimeout = 1800
	}
	return &Broker{freeSourceSlots: freeSourceSlots, freeTargetSlots: freeTargetSlots, sourceSlots: sourceSlots, targetSlots: targetSlots, SourceName: "Source", TargetName: "Target",
		LoopSleep: sleepMS, ReleaseTimeout: releaseTimeout}
}

// TCP side API
func (b *Broker) GetSourceSlot() *BrokerSlot {
	slot := <-b.freeSourceSlots
	// slot.state = BrokerSlotWait
	slot.reset()
	return slot
}

// Docker side API
func (b *Broker) GetTargetSlot() *BrokerSlot {
	// non-blocking
	if len(b.freeTargetSlots) == 0 {
		return nil
	}
	slot := <-b.freeTargetSlots
	return slot
}

func yType(slotType string) bool {
	if len(slotType) > 2 && slotType[0] == 'y' && slotType[1] == ':' {
		return true
	}
	return false
}

func (b *Broker) Run() {

	if b.LoopSleep == 0 {
		b.LoopSleep = 1
	}

	for {

		now := time.Now().Unix()

		b.LastBrokerStart = now

		for _, sourceSlot := range b.sourceSlots {
			if sourceSlot.empty() /* || sourceSlot.state == BrokerSlotStateFree */ { // GB ignore neizmantotos TCP slotus
				continue
			}

			message := sourceSlot.read()
			log.Tracef("broker: got %s message: %s", b.SourceName, message)
			b.AddLogEntry(message, sourceSlot, b.SourceName, BrokerMessageDirectionRead)

			switch message.Type() {
			case BrokerMessageRelease:
				if sourceSlot.state != BrokerSlotStateRun || yType(sourceSlot.slotType) { // GB: yType testu vajag uzprogrammet
					sourceSlot.state = BrokerSlotStateFree // GB: lai ciklos var atskirt TCPslotus, kas netiek lietoti
					b.freeSourceSlots <- sourceSlot
					break
				}
				// targetSlot := b.targetSlots[sourceSlot.oppositeIndex]
				targetSlot := sourceSlot.oppositeSlot
				targetSlot.state = BrokerSlotStateFree
				targetSlot.since = now
				kill := message.PayloadBool()
				if kill {
					log.Debug("broker: killing container because of TCP release with kill setting")
					// targetSlot.image = "" // nil
					targetSlot.slotType = ""
					targetSlot.state = BrokerSlotStateFree
					targetSlot.since = 3
					// set docker type to none ?
					// kill docker
				}
				sourceSlot.state = BrokerSlotStateFree // GB: lai ciklos var atskirt TCPslotus, kas netiek lietoti
				b.freeSourceSlots <- sourceSlot
			case BrokerMessageAcquire:
				// type acq struct {
				// 	image string
				// 	port  int
				// }
				// payload := message.Payload().(acq)
				// sourceSlot.image = payload.image
				// sourceSlot.image = message.PayloadString() // image + port = repository:tag:port
				data := message.AcquirePayload()
				sourceSlot.slotType = data.SlotType
				sourceSlot.runInfo = data.Payload
				sourceSlot.since = now
				sourceSlot.state = BrokerSlotStateWait
				sourceSlot.oppositeSlot = nil

				// TODO: check for empty image string
				// TODO: what to do if empty image is passed
				// if len(sourceSlot.image) == 0 {
				if len(sourceSlot.slotType) == 0 {
					// TODO: fail
					log.Fatalf("broker: empty slot type from %s", b.SourceName)
				}

				// jāatrod brīvs dokeris
				// sūta start, lai piestartē docker container
				/*
					for _, targetSlot := range b.targetSlots {
						// if targetSlot.state == BrokerSlotStateFree && targetSlot.image == sourceSlot.image {
						if targetSlot.state == BrokerSlotStateFree && targetSlot.slotType == sourceSlot.slotType {
							// brīvs konteineris
							targetSlot.state = BrokerSlotStateRun
							// targetSlot.oppositeIndex = sourceSlot.index
							// sourceSlot.oppositeIndex = targetSlot.index
							// targetSlot.oppositeSlot = sourceSlot
							sourceSlot.oppositeSlot = targetSlot
							sourceSlot.state = BrokerSlotStateRun

							sourceSlot.send(NewBrokerMessage(BrokerMessageAcquired, targetSlot.refInfo))
						}
					}
				*/

				// if sourceSlot.state == BrokerSlotStateWait {
				// 	// neizdevās atrast konteineri
				// 	// gaida, kamēr docker gals piestartē
				// }
			}
		}

		// now = time.Now().Unix()

		for _, targetSlot := range b.targetSlots {
			if targetSlot.empty() {
				continue
			}

			message := targetSlot.read()
			log.Tracef("broker: got %s message: %s", b.TargetName, message)
			b.AddLogEntry(message, targetSlot, b.TargetName, BrokerMessageDirectionRead)

			switch message.Type() {
			case BrokerMessageStarted:
				targetSlot.state = BrokerSlotStateFree
				// targetSlot.image = message.PayloadString()
				// targetSlot.slotType = message.PayloadString()
				targetSlot.since = now
			case BrokerMessageFree:
				targetSlot.state = BrokerSlotStateFree
				// targetSlot.image = ""
				targetSlot.slotType = ""
				// targetSlot.remoteAddress = message.PayloadString()
				targetSlot.refInfo = message.Payload()
				targetSlot.since = now
			case BrokerMessageError:

				for _, sourceSlot := range b.sourceSlots {
					if sourceSlot.state == BrokerSlotStateWait && sourceSlot.slotType == targetSlot.slotType {
						// sourceSlot.state = BrokerSlotStateFree
						// b.freeSourceSlots <- sourceSlot
						// sourceSlot.send(NewBrokerMessage(BrokerMessageError, message.PayloadString()))

						// m := NewBrokerMessage(BrokerMessageError, message.PayloadString())
						// sourceSlot.send(m)
						// b.AddLogEntry(m, sourceSlot, b.SourceName, BrokerMessageDirectionSend)
					}

				}

				targetSlot.state = BrokerSlotStateFree
				targetSlot.slotType = ""
				targetSlot.since = 5 // now

				// A: no such image in DockerHub --> signal ERROR to TCPopposite and goto FREE
				// B: no resources on the host to start a new container --> kill old & set FREE(NIL)+NOW and keep trying in 2 sec
			}
		}

		// ------------

		for _, sourceSlot := range b.sourceSlots {
			if sourceSlot.state != BrokerSlotStateWait {
				continue
			}

			if yType(sourceSlot.slotType) {

				for _, targetSlot := range b.targetSlots {
					// if targetSlot.state == BrokerSlotStateFree && targetSlot.image == sourceSlot.image {
					if (targetSlot.state == BrokerSlotStateFree || targetSlot.state == BrokerSlotStateRun) && targetSlot.slotType == sourceSlot.slotType {
						// brīvs konteineris
						targetSlot.state = BrokerSlotStateRun
						targetSlot.since = now
						// targetSlot.oppositeIndex = sourceSlot.index
						// sourceSlot.oppositeIndex = targetSlot.index
						// targetSlot.oppositeSlot = sourceSlot
						sourceSlot.oppositeSlot = targetSlot
						sourceSlot.state = BrokerSlotStateRun
						sourceSlot.since = now // GB

						// sourceSlot.send(NewBrokerMessage(BrokerMessageAcquired, targetSlot.refInfo))
						m := NewBrokerMessage(BrokerMessageAcquired, targetSlot.refInfo)
						sourceSlot.send(m)
						b.AddLogEntry(m, sourceSlot, b.SourceName, BrokerMessageDirectionSend)
					}
				}

			} else {
				for _, targetSlot := range b.targetSlots {
					// if targetSlot.state == BrokerSlotStateFree && targetSlot.image == sourceSlot.image {
					if targetSlot.state == BrokerSlotStateFree && targetSlot.slotType == sourceSlot.slotType {
						// brīvs konteineris
						targetSlot.state = BrokerSlotStateRun
						targetSlot.since = now
						// targetSlot.oppositeIndex = sourceSlot.index
						// sourceSlot.oppositeIndex = targetSlot.index
						// targetSlot.oppositeSlot = sourceSlot
						sourceSlot.oppositeSlot = targetSlot
						sourceSlot.state = BrokerSlotStateRun
						sourceSlot.since = now // GB

						// sourceSlot.send(NewBrokerMessage(BrokerMessageAcquired, targetSlot.refInfo))
						m := NewBrokerMessage(BrokerMessageAcquired, targetSlot.refInfo)
						sourceSlot.send(m)
						b.AddLogEntry(m, sourceSlot, b.SourceName, BrokerMessageDirectionSend)
					}
				}
			}
		}

		for {

			waitSlotTypes := make(map[string]int)     // cqueues
			runningSlotTypes := make(map[string]int)  // bqueues
			waitOnlySlotTypes := make(map[string]int) // aqueues

			for _, slot := range b.sourceSlots {
				if slot.state == BrokerSlotStateWait && slot.oppositeSlot == nil {
					waitSlotTypes[slot.slotType]++
				}
			}

			for _, slot := range b.targetSlots {
				// if slot.state == BrokerSlotStateRun {
				runningSlotTypes[slot.slotType]++
				// }
			}

			// waitOnlySlotTypes = waitSlotTypes - runningSlotTypes

			for slotType, count := range waitSlotTypes {
				if _, present := runningSlotTypes[slotType]; !present {
					waitOnlySlotTypes[slotType] = count
				}
			}

			var oldest *BrokerSlot

			for _, slot := range b.targetSlots {
				if slot.state == BrokerSlotStateFree {
					if oldest == nil || oldest.since > slot.since {
						oldest = slot
					}
				}
			}

			if len(waitOnlySlotTypes) == 0 {
				break
			}

			done := true

			if oldest != nil {

				var mostRequestedSlotType string
				var waitCount int

				for slotType, count := range waitOnlySlotTypes {
					if count > waitCount {
						mostRequestedSlotType = slotType
						waitCount = count
					}
				}

				var waitingSlot *BrokerSlot

				for _, slot := range b.sourceSlots {
					if slot.state == BrokerSlotStateWait && slot.slotType == mostRequestedSlotType {
						oldest.oppositeSlot = slot
						waitingSlot = slot
						break
					}
				}

				// send start only after connecting the slots!

				if waitingSlot != nil {

					oldest.state = BrokerSlotStateStarting
					oldest.slotType = waitingSlot.slotType
					oldest.since = now
					waitingSlot.oppositeSlot = oldest

					// oldest.send(NewBrokerMessage(BrokerMessageStart, waitingSlot.runInfo))
					m := NewBrokerMessage(BrokerMessageStart, waitingSlot.runInfo)
					oldest.send(m)
					b.AddLogEntry(m, oldest, b.TargetName, BrokerMessageDirectionSend)

					// if port number is 3, then respond with error so that client connection gets terminated
					if containerInfo, ok := waitingSlot.runInfo.(*DockerContainerInfo); ok && containerInfo.port == 3 {
						waitingSlot.state = BrokerSlotStateFree
						b.freeSourceSlots <- waitingSlot
						// waitingSlot.send(NewBrokerMessage(BrokerMessageError, "closing port 3 for RabbitMQ"))
						m := NewBrokerMessage(BrokerMessageError, "closing port 3 for RabbitMQ")
						waitingSlot.send(m)
						b.AddLogEntry(m, waitingSlot, b.SourceName, BrokerMessageDirectionSend)
					}

					done = false
				}
			}

			if done {
				break
			}
		}

		// GB piestarte vairakus konteinerus vienam x-type, ja ir tada iespeja
		for {
			done := true
			// Find the oldest unserved TCP slot
			var oldestTCP *BrokerSlot
			oldestTCPtime := now
			for _, slot := range b.sourceSlots {
				if slot.state == BrokerSlotStateWait && slot.oppositeSlot == nil && slot.since < oldestTCPtime && yType(slot.slotType) == false {
					oldestTCP = slot
					oldestTCPtime = slot.since
				}
			}

			// Find the oldest free DockerRunner slot
			var oldestRUNNER *BrokerSlot
			oldestRUNNERtime := now
			for _, slot := range b.targetSlots {
				if slot.state == BrokerSlotStateFree && slot.since < oldestRUNNERtime {
					oldestRUNNER = slot
					oldestRUNNERtime = slot.since
				}
			}

			if oldestTCP != nil && oldestRUNNER != nil && oldestRUNNERtime < (now-4*int64(b.LoopSleep)/1000) && oldestTCPtime < (now-4*int64(b.LoopSleep)/1000) {
				log.Debug("broker: starting another copy of container")
				oldestRUNNER.state = BrokerSlotStateStarting
				oldestRUNNER.slotType = oldestTCP.slotType
				oldestRUNNER.since = now
				oldestTCP.oppositeSlot = oldestRUNNER

				// oldestRUNNER.send(NewBrokerMessage(BrokerMessageStart, oldestTCP.runInfo))
				m := NewBrokerMessage(BrokerMessageStart, oldestTCP.runInfo)
				oldestRUNNER.send(m)
				b.AddLogEntry(m, oldestRUNNER, b.TargetName, BrokerMessageDirectionSend)

				// if port number is 3, then respond with error so that client connection gets terminated
				if containerInfo, ok := oldestTCP.runInfo.(*DockerContainerInfo); ok && containerInfo.port == 3 {
					oldestTCP.state = BrokerSlotStateFree
					b.freeSourceSlots <- oldestTCP
					// oldestTCP.send(NewBrokerMessage(BrokerMessageError, "closing port 3 for RabbitMQ"))
					m := NewBrokerMessage(BrokerMessageError, "closing port 3 for RabbitMQ")
					oldestTCP.send(m)
					b.AddLogEntry(m, oldestTCP, b.SourceName, BrokerMessageDirectionSend)
				}

				done = false
			}

			if done {
				break
			}
		}

		// GB: padara FREE y-tipa konteinerus, kurus vairs neviens nelieto - lai tos velak var kill/stop
		for _, slotD := range b.targetSlots {
			if yType(slotD.slotType) && slotD.state == BrokerSlotStateRun {
				unused := true
				for _, slotT := range b.sourceSlots {
					if slotT.slotType == slotD.slotType && slotT.state == BrokerSlotStateRun {
						unused = false
					}
				}
				if unused {
					slotD.state = BrokerSlotStateFree
					slotD.since = now
				}
			}
		}

		// GB: kill/stop visus konteinerus, kuri 30min nevar piestarteties vai nostradat
		for _, slotD := range b.targetSlots {
			if slotD.since < (now-int64(b.ReleaseTimeout)) && (slotD.state == BrokerSlotStateRun /* || slotD.state == BrokerSlotStateStarting */) {
				log.Debug("broker: releasing target slot")
				slotD.state = BrokerSlotStateFree
				slotD.slotType = ""
				slotD.since = 7
			}
		}

		for _, slot := range b.sourceSlots {
			if slot.state != BrokerSlotStateWait {
				continue
			}

			// source slot waiting
			if slot.since < (now - int64(b.ReleaseTimeout)) {
				slot.state = BrokerSlotStateFree
				b.freeSourceSlots <- slot
				m := NewBrokerMessage(BrokerMessageError, "wait timeout")
				slot.send(m)
				b.AddLogEntry(m, slot, b.SourceName, BrokerMessageDirectionSend)
			}

		}

		// TODO: piestartē vairākus dokerus ar vienu tipu (ja vienā rindā nāk iekšā daudz uzdevumi ar vienu tipu), ja ir palaisti dokeri ar visiem tipiem
		// Nosacījums: eksiste FREE dokeris ar SINCE > 6 sekundem &&& eksiste TCP WAIT bez opositeIndex (nem ar vecako SINCE); atkarto kamer tadi ir

		// log.Trance("Broker: wait 2 sec")
		time.Sleep(time.Duration(b.LoopSleep) * time.Millisecond)
	}
}
