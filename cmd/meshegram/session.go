package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/transport"

	"gitlab.uscr.ru/public-projects/meshegram/internal/logx"
	"gitlab.uscr.ru/public-projects/meshegram/internal/mesh"
)

const sendAttempts = 3

// session owns the single mesh connection used for both reading and sending.
// It auto-reconnects on disconnect and feeds received packets into onPacket.
type session struct {
	address   string
	hopLimit  uint32
	reconnect time.Duration
	onPacket  mesh.PacketHandler

	mu     sync.Mutex
	client *mesh.Client
	closed bool
}

func newSession(address string, hopLimit uint32, reconnect time.Duration, onPacket mesh.PacketHandler) *session {
	return &session{
		address:   address,
		hopLimit:  hopLimit,
		reconnect: reconnect,
		onPacket:  onPacket,
	}
}

// Run blocks until ctx is cancelled, keeping the mesh connection alive.
// Reconnects on any failure: TCP drop, handshake timeout (typically means
// another client is holding the node), protocol error, etc.
func (s *session) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.connectAndHold(ctx); err != nil && ctx.Err() == nil {
			if errors.Is(err, transport.ErrTimeout) {
				logx.Error.Printf("mesh handshake timed out on %s — node may be busy with another client, retrying in %s",
					s.address, s.reconnect)
			} else {
				logx.Error.Printf("mesh session on %s: %v (retrying in %s)", s.address, err, s.reconnect)
			}
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.reconnect):
		}
	}
}

func (s *session) connectAndHold(ctx context.Context) error {
	c, err := mesh.Dial(ctx, s.address, s.onPacket)
	if err != nil {
		return err
	}
	s.setClient(c)
	defer s.resetClient()

	logx.Info.Printf("mesh connected to %s", s.address)

	select {
	case <-ctx.Done():
		return nil
	case <-c.Disconnected():
		return fmt.Errorf("mesh disconnected")
	}
}

// SendText sends text to the given channel index. Retries a few times with a
// reconnect in between so a single flap doesn't drop a message.
func (s *session) SendText(ctx context.Context, channel uint32, text string) error {
	var lastErr error
	for attempt := 1; attempt <= sendAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := s.getClient()
		if c == nil {
			lastErr = errors.New("mesh not connected")
			time.Sleep(time.Second)
			continue
		}
		if err := c.SendText(channel, s.hopLimit, text); err != nil {
			lastErr = err
			logx.Error.Printf("mesh send attempt %d: %v", attempt, err)
			s.resetClient()
			time.Sleep(time.Second)
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("send failed")
	}
	return fmt.Errorf("after %d attempts: %w", sendAttempts, lastErr)
}

// Channels returns the non-disabled channels the node reported.
func (s *session) Channels() []*pb.Channel {
	c := s.getClient()
	if c == nil {
		return nil
	}
	all := c.State().Channels()
	out := make([]*pb.Channel, 0, len(all))
	for _, ch := range all {
		if ch == nil || ch.Role == pb.Channel_DISABLED {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// ChannelIndexByName resolves a channel name (case-insensitive) to its index.
// "Default" aliases to the primary channel whose Settings.Name is empty.
func (s *session) ChannelIndexByName(name string) (uint32, error) {
	wantDefault := strings.EqualFold(name, "Default")
	for _, ch := range s.Channels() {
		chName := ""
		if ch.Settings != nil {
			chName = ch.Settings.Name
		}
		if strings.EqualFold(chName, name) {
			return uint32(ch.Index), nil
		}
		if wantDefault && chName == "" && ch.Role == pb.Channel_PRIMARY {
			return uint32(ch.Index), nil
		}
	}
	return 0, fmt.Errorf("channel %q not found", name)
}

// State returns the underlying transport state if connected, else nil.
func (s *session) State() *transport.State {
	if c := s.getClient(); c != nil {
		return c.State()
	}
	return nil
}

func (s *session) getClient() *mesh.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

func (s *session) setClient(c *mesh.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

func (s *session) resetClient() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
}
