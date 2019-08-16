package webrtc

type dtlsStateEvent struct {
	transport *DTLSTransport
	state  DTLSTransportState
}

type iceStateEvent struct {
	transport *ICETransport
	state  ICETransportState
}


type signalingStateEvent struct {
	state SignalingState
}

type connectionStateEvent struct {
	state PeerConnectionState
}

type transportEvent struct {
	transport Transport
	state TransportState
}

type iceCandidateEvent struct {
	transport *ICETransport
	candidate *ICECandidate
}

type iceGatheringEvent struct {
	transport *ICETransport
	state ICEGatheringState
}

type terminationEvent struct {
}

func (pc *PeerConnection) handleIceGatheringTransition(transport *ICETransport, state ICEGatheringState) {
	//https://w3c.github.io/webrtc-pc/#rtcicegatheringstate-enum

	if state == pc.iceGatheringState {
		return
	}

	switch(state) {
	case ICEGatheringStateComplete:
		for _, knownState := range pc.iceGatheringStates {
			if knownState != ICEGatheringStateComplete {
				return
			}
		}

		pc.iceGatheringState = state

		if pc.onICEGatheringStateHandler != nil {
			pc.onICEGatheringStateHandler(state)
		}

		if pc.onICECandidateHandler != nil {
			pc.onICECandidateHandler(nil)
		}

		pc.startIceConnection()

		return
	case ICEGatheringStateGathering:
		pc.iceGatheringState = state

		if pc.onICEGatheringStateHandler != nil {
			pc.onICEGatheringStateHandler(state)
		}

		return
	case ICEGatheringStateNew:
		pc.log.Warnf("illegal ICE gathering state transition: cannot transition to NEW")
	}
}

func (pc *PeerConnection) handleIceStateTransition(transport *ICETransport, state ICETransportState) {


}

func (pc *PeerConnection) handleDtlsStateTransition(transport *DTLSTransport, state DTLSTransportState) {

}

func (pc *PeerConnection) handleSignalingState(state SignalingState) {
	//https://w3c.github.io/webrtc-pc/#rtcsignalingstate-enum
	pc.signalingState = state

	if pc.onSignalingStateChangeHandler != nil {
		pc.onSignalingStateChangeHandler(state)
	}

	switch(state) {
	case SignalingStateHaveLocalOffer:
	case SignalingStateHaveRemoteOffer:
	case SignalingStateStable:
		pc.startIceGathering()

	}
}

func (pc *PeerConnection) handleConnectionState(state PeerConnectionState) {
	if pc.onPeerConnectionStateHandler != nil {
		pc.onPeerConnectionStateHandler(state)
	}
}

func (pc *PeerConnection) handleIceCandidate(transport *ICETransport, candidate *ICECandidate) {
	if candidate != nil && pc.onICECandidateHandler != nil {
		pc.onICECandidateHandler(candidate)
	}
}

func (pc *PeerConnection) startEventLoop() {
	go func() {
		pc.log.Debug("event loop started")

		for {
			untypedEvt := <-pc.events

			switch event := untypedEvt.(type) {
			case iceStateEvent:
				transport := event.transport

				pc.log.Tracef("ICE transport state: %s", event.state)

				_, found := pc.iceStates[event.transport]

				if found {
					pc.iceStates[transport] = event.state
				} else {
					pc.log.Warnf("ICE transport not found in local state. unclean initialization?")
					pc.iceStates[transport] = event.state
				}

				//trigger follow-up processing
				pc.handleIceStateTransition(transport, event.state)
			case dtlsStateEvent:
				transport := event.transport

				pc.log.Tracef("DTLS transport state: %s", event.state)

				_, found := pc.dtlsStates[event.transport]

				if found {
					pc.dtlsStates[transport] = event.state
				} else {
					pc.log.Warnf("DTLS transport not found in local state. unclean initialization?")
					pc.dtlsStates[transport] = event.state
				}

				//trigger follow-up processing
				pc.handleDtlsStateTransition(transport, event.state)
			case iceGatheringEvent:
				transport := event.transport

				pc.log.Tracef("ICE transport - gathering state: %s", event.state)

				_, found := pc.iceGatheringStates[event.transport]

				if found {
					pc.iceGatheringStates[transport] = event.state
				} else {
					pc.log.Warnf("ICE transport not found in local state. unclean initialization?")
					pc.iceGatheringStates[transport] = event.state
				}

				//trigger follow-up processing
				pc.handleIceGatheringTransition(transport, event.state)
			case iceCandidateEvent:
				transport := event.transport

				if event.candidate != nil {
					pc.log.Tracef("New local ICE candidate %s", event.candidate.String())
				}

				//trigger follow-up processing
				pc.handleIceCandidate(transport, event.candidate)
			case signalingStateEvent:
				pc.log.Tracef("Signaling state changed: %s", event.state)

				//trigger follow-up processing
				pc.handleSignalingState(event.state)

			case terminationEvent:
				pc.log.Debugf("Gracefully terminating connection")

				//todo: graceful disconnect
				return
			default:
				pc.log.Warnf("Unknown message type: %s", event)
			}
		}
	}()
}