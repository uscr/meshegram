// Package mesh provides a TCP client for a Meshtastic node.
package mesh

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/transport"
	"google.golang.org/protobuf/proto"
)

const (
	DefaultTCPPort   = "4403"
	DialTimeout      = 10 * time.Second
	ConnectTimeout   = 30 * time.Second
	BroadcastAddress = 0xFFFFFFFF
)

// PacketHandler is called for every MeshPacket received from the node.
// state is the shared cache of nodes, channels and configs.
type PacketHandler func(pkt *pb.MeshPacket, state *transport.State)

// init silences the meshtastic-go library's slog output. The library logs
// "error reading from radio" in a tight loop when the TCP connection breaks.
func init() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

type Client struct {
	conn   net.Conn
	stream *transport.StreamConn
	client *transport.Client

	mu     sync.Mutex
	closed bool
	notify *notifyConn
}

// Dial opens a TCP connection and performs the initial WantConfigId /
// ConfigCompleteId handshake. Create a new Client per connection lifecycle.
func Dial(ctx context.Context, address string, onPacket PacketHandler) (*Client, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		host, port = address, DefaultTCPPort
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", address, err)
	}

	notify := newNotifyConn(conn)
	stream, err := transport.NewClientStreamConn(notify)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("stream conn: %w", err)
	}

	c := transport.NewClient(stream, false)
	if onPacket != nil {
		c.Handle(new(pb.MeshPacket), func(msg proto.Message) {
			if pkt, ok := msg.(*pb.MeshPacket); ok {
				onPacket(pkt, &c.State)
			}
		})
	}

	connCtx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()
	if err := c.Connect(connCtx); err != nil {
		stream.Close()
		conn.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}

	return &Client{conn: conn, stream: stream, client: c, notify: notify}, nil
}

// SendText sends a text message to the given Meshtastic channel index.
// Packet ID is randomly generated to be non-zero so the radio won't drop it.
func (c *Client) SendText(channel uint32, hopLimit uint32, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("client closed")
	}

	pkt := &pb.ToRadio{
		PayloadVariant: &pb.ToRadio_Packet{
			Packet: &pb.MeshPacket{
				To:       BroadcastAddress,
				Channel:  channel,
				HopLimit: hopLimit,
				Id:       packetID(),
				PayloadVariant: &pb.MeshPacket_Decoded{
					Decoded: &pb.Data{
						Portnum: pb.PortNum_TEXT_MESSAGE_APP,
						Payload: []byte(text),
					},
				},
			},
		},
	}
	return c.client.SendToRadio(pkt)
}

func (c *Client) State() *transport.State {
	return &c.client.State
}

// Disconnected fires when the underlying TCP connection returns any read
// error (EOF, timeout, reset, etc.). Listeners should react by closing the
// client and re-dialing.
func (c *Client) Disconnected() <-chan struct{} {
	return c.notify.dead
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	err1 := c.stream.Close()
	err2 := c.conn.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// notifyConn wraps a net.Conn so that the first read error flips an internal
// flag and further Read calls block forever. This both signals the listener
// via the dead channel and parks the meshtastic-go library's internal read
// loop (which otherwise spin-loops on EOF).
type notifyConn struct {
	net.Conn
	dead chan struct{}
	once sync.Once
}

func newNotifyConn(c net.Conn) *notifyConn {
	return &notifyConn{Conn: c, dead: make(chan struct{})}
}

func (n *notifyConn) Read(p []byte) (int, error) {
	select {
	case <-n.dead:
		<-make(chan struct{})
	default:
	}
	c, err := n.Conn.Read(p)
	if err != nil {
		n.once.Do(func() { close(n.dead) })
	}
	return c, err
}

func (n *notifyConn) Close() error {
	n.once.Do(func() { close(n.dead) })
	return n.Conn.Close()
}
