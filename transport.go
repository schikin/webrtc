package webrtc

import (
	"fmt"
	"github.com/pion/logging"
	"github.com/pion/sdp/v2"
	"strings"
)

//one of:
// - bundled media
// - single RTP transceiver
// - single application stream

type ApplicationTransport struct {
	SCTPTransport *SCTPTransport

	Channels []*DataChannel

	State TransportState
}

type TransportState int

const (
	TransportStateNew TransportState = iota + 1
	TransportStateConnecting
	TransportStateConnected
	TransportStateClosed
)

type TransportStateEvent struct {
	Transport Transport
	NewState TransportState
	PrevState TransportState
}

type Transport interface {
	//Initialize(pc *PeerConnection)

	//start asynchronous connection
	Connect()

	Disconnect()
	AddIceCandidate(mid string, candidate *ICECandidate)

	//list of MIDs carried by this transport
	GetMids() []string

	getDtlsRole() DTLSRole
	setDtlsRole(role DTLSRole)

	getDtlsTransport() *DTLSTransport
	setDtlsTransport(t *DTLSTransport)

	getEvents() chan interface{}
	getPeerConnection() *PeerConnection
	getIceTransport() *ICETransport
	getLog() logging.LeveledLogger
	getLocalMediaDescription() *sdp.MediaDescription
	getRemoteMediaDescription() *sdp.MediaDescription

	OnStateChange(func(state TransportState))
}

func transportInitDtls(s Transport, pc *PeerConnection) {
	iceGatherer, err := pc.api.NewICEGatherer(ICEGatherOptions{
		ICEServers:      pc.configuration.ICEServers,
		ICEGatherPolicy: pc.configuration.ICETransportPolicy,
	})

	if err != nil {
		pc.log.Errorf("Failed to create ICE gatherer")
		return
	}

	iceTransport := pc.api.NewICETransport(iceGatherer)
	dtlsTransport, err := pc.api.NewDTLSTransport(iceTransport, pc.configuration.Certificates)

	iceTransport.gatherer.onLocalCandidateHdlr = func(candidate *ICECandidate) {
		s.getEvents() <- iceCandidateEvent{iceTransport,candidate}
	}

	iceTransport.gatherer.onStateChangeHdlr = func(state ICEGathererState) {
		switch(state) {
		case ICEGathererStateGathering:
			s.getEvents() <- iceGatheringEvent{iceTransport, ICEGatheringStateGathering}
		case ICEGathererStateComplete:
			s.getEvents() <- iceGatheringEvent{iceTransport, ICEGatheringStateComplete}
		}
	}

	iceTransport.onConnectionStateChangeHdlr = func(state ICETransportState) {
		s.getEvents() <- iceStateEvent{iceTransport, state}
	}

	dtlsTransport.onStateChangeHdlr = func(state DTLSTransportState) {
		s.getEvents() <- dtlsStateEvent{dtlsTransport, state}
	}

	if err != nil {
		pc.log.Errorf("Failed to initialize DTLS transport")
		return
	}

	//todo: handle SCTP

	s.setDtlsTransport(dtlsTransport)
}

//returns false in case processing needs to be stopped
//just proxy everything to upstream PeerConnection
func transportHandleEvent(t Transport, untypedEvt interface{}) bool {
	switch evt := untypedEvt.(type) {
	case iceGatheringEvent:
		t.getPeerConnection().events <- evt
	case iceStateEvent:
		if evt.state == ICETransportStateConnected {
			transportIceConnected(t)
		}
		t.getPeerConnection().events <- evt
	case iceCandidateEvent:
		t.getPeerConnection().events <- evt
	case dtlsStateEvent:
		t.getPeerConnection().events <- evt
	case terminationEvent:
		return false
	}

	return true
}

func transportIceConnected(t Transport) {
	t.getLog().Debugf("ICE transport connected - engaging DTLS")

	dtlsParams, err := transportGetDtlsParams(t)

	if err != nil {
		t.getLog().Errorf("failed to extract DTLS params from SDP: %s", err)
		return
	}

	t.getLog().Debugf("DTLS params: role=%s, fingerprint=%s", dtlsParams.Role, dtlsParams.Fingerprints[0])

	go func() {
		err := t.getDtlsTransport().Start(*dtlsParams)

		if err != nil {
			t.getLog().Errorf(err.Error())
		}
	}()
}

func transportGatherIceCandidates(t Transport) {
	t.getLog().Debugf("Starting ICE gathering")

	go t.getIceTransport().gatherer.Gather()
}

func dtlsRoleFromSdp(media *sdp.MediaDescription, sess *sdp.SessionDescription) DTLSRole {
	setupAttribute, setupFound := media.Attribute("setup")

	if !setupFound {
		setupAttribute, setupFound = sess.Attribute("setup")
	}

	var role DTLSRole

	if !setupFound {
		role = DTLSRoleAuto
	} else {
		role = dtlsRoleFromSetupAttribute(setupAttribute)
	}

	return role
}

func transportGetDtlsParams(t Transport) (*DTLSParameters, error) {
	ret := &DTLSParameters{}

	sess := t.getPeerConnection().currentLocalDescription.parsed
	media := t.getLocalMediaDescription()


	fingerprint, haveFingerprint := media.Attribute("fingerprint")

	if !haveFingerprint {
		fingerprint, haveFingerprint = sess.Attribute("fingerprint")
	}

	if !haveFingerprint {
		return nil, fmt.Errorf("fingerprint attribute not found")
	}

	parts := strings.Split(fingerprint, " ")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid fingerprint")
	}

	fingerprint = parts[1]
	fingerprintHash := parts[0]

	ret.Role = t.getDtlsRole()
	ret.Fingerprints = []DTLSFingerprint{{Algorithm: fingerprintHash, Value: fingerprint}}

	return ret, nil
}




