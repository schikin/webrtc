package webrtc

import (
	"fmt"
	"github.com/pion/logging"
	"github.com/pion/rtcp"
	"github.com/pion/sdp/v2"
	"strconv"
	"strings"
)

type MediaTransport struct {
	DTLSRole DTLSRole
	DTLSTransport *DTLSTransport

	//either SCTP or RTP (or group of RTPs in case of bundling
	RTPTransceivers []*RTPTransceiver

	State TransportState

	pc *PeerConnection

	state        TransportState
	stateHandler func(state TransportState)

	events chan interface{}

	log logging.LeveledLogger
}

func (t *MediaTransport) getPeerConnection() *PeerConnection {
	return t.pc
}

func (t *MediaTransport) getIceTransport() *ICETransport {
	return t.DTLSTransport.iceTransport
}

func (t *MediaTransport) getLog() logging.LeveledLogger {
	return t.log
}

func (t *MediaTransport) handleEvent(untypedEvt interface{}) {
	switch evt := untypedEvt.(type) {
	case dtlsStateEvent:
		if(evt.state == DTLSTransportStateConnected) {
			t.log.Debugf("DTLS transport connected, starting SRTP connection")
			t.connectSRTP()
		}
	}
}

func (t *MediaTransport) eventLoop() {
	go func() {
		for {
			if !transportHandleEvent(t, <-t.events) {
				return
			}
		}
	}()
}

func (t *MediaTransport) GetMids() []string {
	ret := []string{}

	for _, trans := range t.RTPTransceivers {
		ret = append(ret, trans.Mid)
	}

	return ret
}

func (t *MediaTransport) AddIceCandidate(mid string, candidate *ICECandidate) {
	localMid := t.getLocalMediaDescription().GetMidValue()

	if localMid != mid {
		t.log.Debugf("skipping ICE candidate for MID=%s as we're only working with MID=%s", mid, localMid)
		return
	}

	t.getIceTransport().AddRemoteCandidate(*candidate)
}

// WriteRTCP sends a user provided RTCP packet to the connected peer
// If no peer is connected the packet is discarded
func (t *MediaTransport) WriteRTCP(pkts []rtcp.Packet) error {
	raw, err := rtcp.Marshal(pkts)
	if err != nil {
		return err
	}

	srtcpSession, err := t.DTLSTransport.getSRTCPSession()
	if err != nil {
		return nil
	}

	writeStream, err := srtcpSession.OpenWriteStream()
	if err != nil {
		return fmt.Errorf("WriteRTCP failed to open WriteStream: %v", err)
	}

	if _, err := writeStream.Write(raw); err != nil {
		return err
	}
	return nil
}

func (t *MediaTransport) getLocalMediaDescription() *sdp.MediaDescription {
	transceiver := t.RTPTransceivers[0]

	return transceiver.LocalMediaDescription
}

func (t *MediaTransport) getRemoteMediaDescription() *sdp.MediaDescription {
	transceiver := t.RTPTransceivers[0]

	return transceiver.RemoteMediaDescription
}

//connects SRTP channel once DTLS connection is established
func (t *MediaTransport) connectSRTP() {
	t.openSRTP()

	for _, tranceiver := range t.RTPTransceivers {
		if tranceiver.Sender != nil {
			err := tranceiver.Sender.Send(RTPSendParameters{
				Encodings: RTPEncodingParameters{
					RTPCodingParameters{
						SSRC:        tranceiver.Sender.track.SSRC(),
						PayloadType: tranceiver.Sender.track.PayloadType(),
					},
				}})

			if err != nil {
				t.log.Warnf("Failed to start Sender: %s", err)
			}
		}
	}

	go t.drainSRTP()
}


// todo: rework, probably has inter-transport locks
// openSRTP opens knows inbound SRTP streams from the RemoteDescription
func (t *MediaTransport) openSRTP() {
	type incomingTrack struct {
		kind  RTPCodecType
		label string
		id    string
		ssrc  uint32
	}
	incomingTracks := map[uint32]incomingTrack{}

	pc := t.pc

	remoteIsPlanB := false
	switch pc.configuration.SDPSemantics {
	case SDPSemanticsPlanB:
		remoteIsPlanB = true
	case SDPSemanticsUnifiedPlanWithFallback:
		remoteIsPlanB = pc.descriptionIsPlanB(pc.RemoteDescription())
	}

	media := t.getRemoteMediaDescription()
	for _, attr := range media.Attributes {

		codecType := NewRTPCodecType(media.MediaName.Media)
		if codecType == 0 {
			continue
		}

		if attr.Key == sdp.AttrKeySSRC {
			split := strings.Split(attr.Value, " ")
			ssrc, err := strconv.ParseUint(split[0], 10, 32)
			if err != nil {
				pc.log.Warnf("Failed to parse SSRC: %v", err)
				continue
			}

			trackID := ""
			trackLabel := ""
			if len(split) == 3 && strings.HasPrefix(split[1], "msid:") {
				trackLabel = split[1][len("msid:"):]
				trackID = split[2]
			}

			incomingTracks[uint32(ssrc)] = incomingTrack{codecType, trackLabel, trackID, uint32(ssrc)}
			if trackID != "" && trackLabel != "" {
				break // Remote provided Label+ID, we have all the information we need
			}
		}
	}

	startReceiver := func(incoming incomingTrack, receiver *RTPReceiver) {
		err := receiver.Receive(RTPReceiveParameters{
			Encodings: RTPDecodingParameters{
				RTPCodingParameters{SSRC: incoming.ssrc},
			}})
		if err != nil {
			pc.log.Warnf("RTPReceiver Receive failed %s", err)
			return
		}

		if err = receiver.Track().determinePayloadType(); err != nil {
			pc.log.Warnf("Could not determine PayloadType for SSRC %d", receiver.Track().SSRC())
			return
		}

		pc.mu.RLock()
		defer pc.mu.RUnlock()

		if pc.currentLocalDescription == nil {
			pc.log.Warnf("SetLocalDescription not called, unable to handle incoming media streams")
			return
		}

		sdpCodec, err := pc.currentLocalDescription.parsed.GetCodecForPayloadType(receiver.Track().PayloadType())
		if err != nil {
			pc.log.Warnf("no codec could be found in RemoteDescription for payloadType %d", receiver.Track().PayloadType())
			return
		}

		codec, err := pc.api.mediaEngine.getCodecSDP(sdpCodec)
		if err != nil {
			pc.log.Warnf("codec %s in not registered", sdpCodec)
			return
		}

		receiver.Track().mu.Lock()
		receiver.Track().id = incoming.id
		receiver.Track().label = incoming.label
		receiver.Track().kind = codec.Type
		receiver.Track().codec = codec
		receiver.Track().mu.Unlock()

		if pc.onTrackHandler != nil {
			pc.onTrack(receiver.Track(), receiver)
		} else {
			pc.log.Warnf("OnTrack unset, unable to handle incoming media streams")
		}
	}

	localTransceivers := append([]*RTPTransceiver{}, pc.GetTransceivers()...)
	for ssrc, incoming := range incomingTracks {
		for i := range localTransceivers {
			t := localTransceivers[i]
			switch {
			case incomingTracks[ssrc].kind != t.kind:
				continue
			case t.Direction != RTPTransceiverDirectionRecvonly && t.Direction != RTPTransceiverDirectionSendrecv:
				continue
			case t.Receiver == nil:
				continue
			}

			delete(incomingTracks, ssrc)
			localTransceivers = append(localTransceivers[:i], localTransceivers[i+1:]...)
			go startReceiver(incoming, t.Receiver)
			break
		}
	}

	if remoteIsPlanB {
		for ssrc, incoming := range incomingTracks {
			t, err := pc.AddTransceiver(incoming.kind, RtpTransceiverInit{
				Direction: RTPTransceiverDirectionSendrecv,
			})
			if err != nil {
				pc.log.Warnf("Could not add transceiver for remote SSRC %d: %s", ssrc, err)
				continue
			}
			go startReceiver(incoming, t.Receiver)
		}
	}
}

// drainSRTP pulls and discards RTP/RTCP packets that don't match any SRTP
// These could be sent to the user, but right now we don't provide an API
// to distribute orphaned RTCP messages. This is needed to make sure we don't block
// and provides useful debugging messages
func (t *MediaTransport) drainSRTP() {
	go func() {
		for {
			srtpSession, err := t.DTLSTransport.getSRTPSession()
			if err != nil {
				t.log.Warnf("drainSRTP failed to open SrtpSession: %v", err)
				return
			}

			_, ssrc, err := srtpSession.AcceptStream()
			if err != nil {
				t.log.Warnf("Failed to accept RTP %v \n", err)
				return
			}

			t.log.Debugf("Incoming unhandled RTP ssrc(%d)", ssrc)
		}
	}()

	for {
		srtcpSession, err := t.DTLSTransport.getSRTCPSession()
		if err != nil {
			t.log.Warnf("drainSRTP failed to open SrtcpSession: %v", err)
			return
		}

		_, ssrc, err := srtcpSession.AcceptStream()
		if err != nil {
			t.log.Warnf("Failed to accept RTCP %v \n", err)
			return
		}
		t.log.Debugf("Incoming unhandled RTCP ssrc(%d)", ssrc)
	}
}

func (t *MediaTransport) OnStateChange(hdlr func(state TransportState)) {
	t.stateHandler = hdlr
}

func (t *MediaTransport) Connect() {
	//t.state = TransportStateConnecting
	//t.events <- transportEvent{t, t.state}

	iceRole := ICERoleControlling
	iceMediaParams := t.getRemoteMediaDescription().GetICEParams()
	iceSessionParams := t.pc.RemoteDescription().parsed.GetICEParams()

	iceParams := ICEParameters{
		UsernameFragment: iceMediaParams.UserFragment,
		Password: iceMediaParams.Password,
		ICELite: iceSessionParams.Lite,
	}

	t.log.Debug("Starting ICE connection")

	go func() {
		err := t.DTLSTransport.iceTransport.Start(
			t.DTLSTransport.iceTransport.gatherer,
			iceParams,
			&iceRole,
		)

		if err != nil {
			t.log.Warnf("failed start ICE connection: %s", err)
		}
	}()
}

func (t *MediaTransport) Disconnect() {
	t.DTLSTransport.Stop()

	t.events <- terminationEvent{}
}

func (t *MediaTransport) setDtlsTransport(trans *DTLSTransport) {
	t.DTLSTransport = trans
}

func (t *MediaTransport) getDtlsTransport() *DTLSTransport {
	return t.DTLSTransport
}

func (t *MediaTransport) getEvents() chan interface{} {
	return t.events
}

func (t *MediaTransport) getDtlsRole() DTLSRole {
	return t.DTLSRole
}

func (t *MediaTransport) setDtlsRole(role DTLSRole) {
	t.DTLSRole = role
}

func MediaTransportCreate(pc *PeerConnection) *MediaTransport {
	ret := &MediaTransport{}

	ret.pc = pc
	ret.DTLSRole = DTLSRoleAuto
	ret.state = TransportStateNew
	ret.log = pc.api.settingEngine.LoggerFactory.NewLogger("transport")
	ret.events = make(chan interface{})

	ret.eventLoop()

	transportInitDtls(ret, pc)

	return ret
}
