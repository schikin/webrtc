package webrtc

import (
	"math/rand"
	"time"
)

// ICEParameters includes the ICE username fragment
// and password and other ICE-related parameters.
type ICEParameters struct {
	UsernameFragment string `json:"usernameFragment"`
	Password         string `json:"password"`
	ICELite          bool   `json:"iceLite"`
}

func randSeq(n int) string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[r.Intn(len(letters))]
	}
	return string(b)
}

func generateIceParams() ICEParameters {
	return ICEParameters{
		UsernameFragment:    randSeq(16),
		Password:      randSeq(32),
		ICELite: false,
	}
}
