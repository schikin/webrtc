package webrtc

import (
	"fmt"
	"github.com/pion/sdp/v2"
	"strings"
)

// DTLSRole indicates the role of the DTLS transport.
type DTLSRole byte

const (
	// DTLSRoleAuto defines the DLTS role is determined based on
	// the resolved ICE role: the ICE controlled role acts as the DTLS
	// client and the ICE controlling role acts as the DTLS server.
	DTLSRoleAuto DTLSRole = iota + 1

	// DTLSRoleClient defines the DTLS client role.
	DTLSRoleClient

	// DTLSRoleServer defines the DTLS server role.
	DTLSRoleServer
)

func (r DTLSRole) String() string {
	switch r {
	case DTLSRoleAuto:
		return "auto"
	case DTLSRoleClient:
		return "client"
	case DTLSRoleServer:
		return "server"
	default:
		return unknownStr
	}
}

func dtlsParamsFromSession(s *sdp.SessionDescription) (*DTLSParameters, error) {
	ret := &DTLSParameters{}

	setupAttribute, _ := s.Attribute("setup")
	role := dtlsRoleFromSetupAttribute(setupAttribute)

	fingerprint, haveFingerprint := s.Attribute("fingerprint")

	if !haveFingerprint {
		//try to get session-level

		return nil, fmt.Errorf("could not find fingerprint")
	}

	parts := strings.Split(fingerprint, " ")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid fingerprint")
	}

	fingerprint = parts[1]
	fingerprintHash := parts[0]

	ret.Role = role
	ret.Fingerprints = []DTLSFingerprint{{Algorithm: fingerprintHash, Value: fingerprint}}

	return ret, nil
}


func dtlsParamsFromMediaSection(mediaSection *sdp.MediaDescription) (*DTLSParameters, error) {
	ret := &DTLSParameters{}

	setupAttribute, _ := mediaSection.Attribute("setup")
	role := dtlsRoleFromSetupAttribute(setupAttribute)

	fingerprint, haveFingerprint := mediaSection.Attribute("fingerprint")

	if !haveFingerprint {
		//try to get session-level

		return nil, fmt.Errorf("could not find fingerprint")
	}

	parts := strings.Split(fingerprint, " ")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid fingerprint")
	}

	fingerprint = parts[1]
	fingerprintHash := parts[0]

	ret.Role = role
	ret.Fingerprints = []DTLSFingerprint{{Algorithm: fingerprintHash, Value: fingerprint}}

	return ret, nil
}

// Iterate a SessionDescription from a remote to determine if an explicit
// role can been determined for local connection. The decision is made from the first role we we parse.
// If no role can be found we return DTLSRoleAuto
func dtlsRoleFromSetupAttribute(attribute string) DTLSRole {
	switch attribute {
			case "active":
				return DTLSRoleClient
			case "passive":
				return DTLSRoleServer
			default:
				return DTLSRoleAuto
			}
}
