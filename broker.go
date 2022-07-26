package main

import (
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

// func newBrokerSlot(index, size int /* size = 3 */) *BrokerSlot {
func newBrokerSlot(size int /* size = 3 */) *BrokerSlot {
	// return &BrokerSlot{input: make(chan *BrokerMessage, size), output: make(chan *BrokerMessage, size) /* state: BrokerSlotState{}, */, index: index}
	return &BrokerSlot{input: make(chan *BrokerMessage, size), output: make(chan *BrokerMessage, size) /* state: BrokerSlotState{}, */}
}

// broker internal API
func (s *BrokerSlot) send(message *BrokerMessage) {
	s.output <- message
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

type Broker struct {
	freeSourceSlots chan *BrokerSlot
	freeTargetSlots chan *BrokerSlot
	// source slots
	// target slots
	tcpSlots    []*BrokerSlot
	dockerSlots []*BrokerSlot
	SourceName  string
	TargetName  string

	LoopSleep int // in milliseconds
}

func NewBroker(sourceSlotCount, targetSlotCount, sleepMS int) *Broker {
	freeSourceSlots := make(chan *BrokerSlot, sourceSlotCount)
	freeTargetSlots := make(chan *BrokerSlot, targetSlotCount)
	sourceSlots := make([]*BrokerSlot, sourceSlotCount)
	targetSlots := make([]*BrokerSlot, targetSlotCount)
	for i := 0; i < sourceSlotCount; i++ {
		slot := newBrokerSlot(3)
		sourceSlots[i] = slot
		freeSourceSlots <- slot
		slot.state = BrokerSlotStateFree // GB: lai ciklos var atskirt TCPslotus, kas netiek lietoti
	}
	for i := 0; i < targetSlotCount; i++ {
		slot := newBrokerSlot(3)
		targetSlots[i] = slot
		freeTargetSlots <- slot
	}
	if sleepMS == 0 {
		sleepMS = 1
	}
	return &Broker{freeSourceSlots: freeSourceSlots, freeTargetSlots: freeTargetSlots, tcpSlots: sourceSlots, dockerSlots: targetSlots, SourceName: "Source", TargetName: "Target", LoopSleep: sleepMS}
}

// TCP side API
func (b *Broker) GetSourceSlot() *BrokerSlot {
	slot := <-b.freeSourceSlots
	// slot.state = BrokerSlotWait
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

		for _, tcpSlot := range b.tcpSlots {
			if tcpSlot.empty() /* || tcpSlot.state == BrokerSlotStateFree */ { // GB ignore neizmantotos TCP slotus
				continue
			}

			message := tcpSlot.read()
			log.Tracef("broker: got %s message: %s", b.SourceName, message)

			switch message.Type() {
			case BrokerMessageRelease:
				if tcpSlot.state != BrokerSlotStateRun || yType(tcpSlot.slotType) { // GB: yType testu vajag uzprogrammet
					b.freeSourceSlots <- tcpSlot
					tcpSlot.state = BrokerSlotStateFree // GB: lai ciklos var atskirt TCPslotus, kas netiek lietoti
					break
				}
				// dockerSlot := b.dockerSlots[tcpSlot.oppositeIndex]
				dockerSlot := tcpSlot.oppositeSlot
				dockerSlot.state = BrokerSlotStateFree
				dockerSlot.since = now
				kill := message.PayloadBool()
				if kill {
					// dockerSlot.image = "" // nil
					dockerSlot.slotType = ""
					dockerSlot.state = BrokerSlotStateFree
					dockerSlot.since = 0
					// set docker type to none ?
					// kill docker
				}
				b.freeSourceSlots <- tcpSlot
			case BrokerMessageAcquire:
				// type acq struct {
				// 	image string
				// 	port  int
				// }
				// payload := message.Payload().(acq)
				// tcpSlot.image = payload.image
				// tcpSlot.image = message.PayloadString() // image + port = repository:tag:port
				data := message.AcquirePayload()
				tcpSlot.slotType = data.SlotType
				tcpSlot.runInfo = data.Payload
				tcpSlot.since = now
				tcpSlot.state = BrokerSlotStateWait
				tcpSlot.oppositeSlot = nil

				// TODO: check for empty image string
				// TODO: what to do if empty image is passed
				// if len(tcpSlot.image) == 0 {
				if len(tcpSlot.slotType) == 0 {
					// TODO: fail
					log.Fatalf("broker: empty slot type from %s", b.SourceName)
				}

				// jāatrod brīvs dokeris
				// sūta start, lai piestartē docker container
				/*
					for _, dockerSlot := range b.dockerSlots {
						// if dockerSlot.state == BrokerSlotStateFree && dockerSlot.image == tcpSlot.image {
						if dockerSlot.state == BrokerSlotStateFree && dockerSlot.slotType == tcpSlot.slotType {
							// brīvs konteineris
							dockerSlot.state = BrokerSlotStateRun
							// dockerSlot.oppositeIndex = tcpSlot.index
							// tcpSlot.oppositeIndex = dockerSlot.index
							// dockerSlot.oppositeSlot = tcpSlot
							tcpSlot.oppositeSlot = dockerSlot
							tcpSlot.state = BrokerSlotStateRun

							tcpSlot.send(NewBrokerMessage(BrokerMessageAcquired, dockerSlot.refInfo))
						}
					}
				*/

				// if tcpSlot.state == BrokerSlotStateWait {
				// 	// neizdevās atrast konteineri
				// 	// gaida, kamēr docker gals piestartē
				// }
			}
		}

		// now = time.Now().Unix()

		for _, dockerSlot := range b.dockerSlots {
			if dockerSlot.empty() {
				continue
			}

			message := dockerSlot.read()
			log.Tracef("broker: got %s message: %s", b.TargetName, message)

			switch message.Type() {
			case BrokerMessageStarted:
				dockerSlot.state = BrokerSlotStateFree
				// dockerSlot.image = message.PayloadString()
				// dockerSlot.slotType = message.PayloadString()
				dockerSlot.since = now
			case BrokerMessageFree:
				dockerSlot.state = BrokerSlotStateFree
				// dockerSlot.image = ""
				dockerSlot.slotType = ""
				// dockerSlot.remoteAddress = message.PayloadString()
				dockerSlot.refInfo = message.Payload()
				dockerSlot.since = now
			case BrokerMessageError:

				for _, tcpSlot := range b.tcpSlots {
					if tcpSlot.state == BrokerSlotStateWait && tcpSlot.slotType == dockerSlot.slotType {
						b.freeSourceSlots <- tcpSlot
						tcpSlot.state = BrokerSlotStateFree
						tcpSlot.send(NewBrokerMessage(BrokerMessageError, "Image does not exist"))
					}

				}

				dockerSlot.state = BrokerSlotStateFree
				dockerSlot.slotType = ""
				dockerSlot.since = 0 // now

				// A: no such image in DockerHub --> signal ERROR to TCPopposite and goto FREE
				// B: no resources on the host to start a new container --> kill old & set FREE(NIL)+NOW and keep trying in 2 sec
			}
		}

		// ------------

		for _, tcpSlot := range b.tcpSlots {
			if tcpSlot.state != BrokerSlotStateWait {
				continue
			}

			if yType(tcpSlot.slotType) {

				for _, dockerSlot := range b.dockerSlots {
					// if dockerSlot.state == BrokerSlotStateFree && dockerSlot.image == tcpSlot.image {
					if (dockerSlot.state == BrokerSlotStateFree || dockerSlot.state == BrokerSlotStateRun) && dockerSlot.slotType == tcpSlot.slotType {
						// brīvs konteineris
						dockerSlot.state = BrokerSlotStateRun
						// dockerSlot.oppositeIndex = tcpSlot.index
						// tcpSlot.oppositeIndex = dockerSlot.index
						// dockerSlot.oppositeSlot = tcpSlot
						tcpSlot.oppositeSlot = dockerSlot
						tcpSlot.state = BrokerSlotStateRun
						tcpSlot.since = now // GB

						tcpSlot.send(NewBrokerMessage(BrokerMessageAcquired, dockerSlot.refInfo))
					}
				}

			} else {
				for _, dockerSlot := range b.dockerSlots {
					// if dockerSlot.state == BrokerSlotStateFree && dockerSlot.image == tcpSlot.image {
					if dockerSlot.state == BrokerSlotStateFree && dockerSlot.slotType == tcpSlot.slotType {
						// brīvs konteineris
						dockerSlot.state = BrokerSlotStateRun
						// dockerSlot.oppositeIndex = tcpSlot.index
						// tcpSlot.oppositeIndex = dockerSlot.index
						// dockerSlot.oppositeSlot = tcpSlot
						tcpSlot.oppositeSlot = dockerSlot
						tcpSlot.state = BrokerSlotStateRun
						tcpSlot.since = now // GB

						tcpSlot.send(NewBrokerMessage(BrokerMessageAcquired, dockerSlot.refInfo))
					}
				}
			}
		}

		for {

			waitSlotTypes := make(map[string]int)     // cqueues
			runningSlotTypes := make(map[string]int)  // bqueues
			waitOnlySlotTypes := make(map[string]int) // aqueues

			for _, slot := range b.tcpSlots {
				if slot.state == BrokerSlotStateWait && slot.oppositeSlot == nil {
					waitSlotTypes[slot.slotType]++
				}
			}

			for _, slot := range b.dockerSlots {
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

			for _, slot := range b.dockerSlots {
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

				for _, slot := range b.tcpSlots {
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

					oldest.send(NewBrokerMessage(BrokerMessageStart, waitingSlot.runInfo))

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
			for _, slot := range b.tcpSlots {
				if slot.state == BrokerSlotStateWait && slot.oppositeSlot == nil && slot.since < oldestTCPtime && yType(slot.slotType) == false {
					oldestTCP = slot
					oldestTCPtime = slot.since
				}
			}

			// Find the oldest free DockerRunner slot
			var oldestRUNNER *BrokerSlot
			oldestRUNNERtime := now
			for _, slot := range b.dockerSlots {
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
				oldestRUNNER.send(NewBrokerMessage(BrokerMessageStart, oldestTCP.runInfo))
				done = false
			}

			if done {
				break
			}
		}

		// GB: padara FREE y-tipa konteinerus, kurus vairs neviens nelieto - lai tos velak var kill/stop
		for _, slotD := range b.dockerSlots {
			if yType(slotD.slotType) && slotD.state == BrokerSlotStateRun {
				unused := true
				for _, slotT := range b.tcpSlots {
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
		for _, slotD := range b.dockerSlots {
			if slotD.since < (now-1800) && (slotD.state == BrokerSlotStateRun || slotD.state == BrokerSlotStateStarting) {
				slotD.state = BrokerSlotStateFree
				slotD.slotType = ""
				slotD.since = 0
			}
		}

		// TODO: piestartē vairākus dokerus ar vienu tipu (ja vienā rindā nāk iekšā daudz uzdevumi ar vienu tipu), ja ir palaisti dokeri ar visiem tipiem
		// Nosacījums: eksiste FREE dokeris ar SINCE > 6 sekundem &&& eksiste TCP WAIT bez opositeIndex (nem ar vecako SINCE); atkarto kamer tadi ir

		// log.Trance("Broker: wait 2 sec")
		time.Sleep(time.Duration(b.LoopSleep) * time.Millisecond)
	}
}
