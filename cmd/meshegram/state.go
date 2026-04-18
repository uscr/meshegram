package main

import "sync/atomic"

// bridgeState is the small runtime state shared between the mesh receiver
// goroutine and Telegram handlers.
type bridgeState struct {
	cache *msgCache

	// lastChannel is the Meshtastic channel index of the most recent incoming
	// packet that we forwarded to Telegram. Used to default outgoing /send in
	// the bridged group to the channel of the latest mesh conversation.
	lastChannel atomic.Uint32
}

func newBridgeState(cacheCap int, initialChannel uint32) *bridgeState {
	s := &bridgeState{cache: newMsgCache(cacheCap)}
	s.lastChannel.Store(initialChannel)
	return s
}

func (s *bridgeState) LastChannel() uint32      { return s.lastChannel.Load() }
func (s *bridgeState) SetLastChannel(ch uint32) { s.lastChannel.Store(ch) }
