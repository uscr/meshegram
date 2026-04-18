package mesh

import (
	"fmt"
	"strings"

	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/transport"
	"google.golang.org/protobuf/encoding/protowire"
)

// meshPacketHopStartField is the protobuf field number for MeshPacket.hop_start.
// v0.1.7 of meshnet-gophers/meshtastic-go predates this field, so the value
// lives in unknown fields and we parse it by hand.
const meshPacketHopStartField = 15

// NodeLabel returns a human label for a node: "LongName (ShortName) !deadbeef"
// if the user info is known, otherwise just "!deadbeef". Not HTML-escaped.
func NodeLabel(num uint32, state *transport.State) string {
	hexID := fmt.Sprintf("!%08x", num)
	if state == nil {
		return hexID
	}
	for _, ni := range state.Nodes() {
		if ni == nil || ni.Num != num || ni.User == nil {
			continue
		}
		u := ni.User
		long := strings.TrimSpace(u.LongName)
		short := strings.TrimSpace(u.ShortName)
		switch {
		case long != "" && short != "":
			return fmt.Sprintf("%s (%s) %s", long, short, hexID)
		case long != "":
			return fmt.Sprintf("%s %s", long, hexID)
		case short != "":
			return fmt.Sprintf("%s %s", short, hexID)
		}
	}
	return hexID
}

// ChannelName returns the Meshtastic channel name for the given index.
// Primary channels with an empty name are rendered as "Default" (the public
// LongFast); unknown indices fall back to "#N".
func ChannelName(index uint32, state *transport.State) string {
	if state != nil {
		for _, ch := range state.Channels() {
			if ch == nil || uint32(ch.Index) != index {
				continue
			}
			if ch.Settings != nil && ch.Settings.Name != "" {
				return ch.Settings.Name
			}
			if ch.Role == pb.Channel_PRIMARY {
				return "Default"
			}
			break
		}
	}
	return fmt.Sprintf("#%d", index)
}

// HopStart returns the initial hop_limit value the sender set, if the packet
// contains the field. Combined with pkt.HopLimit it yields the number of
// hops the packet traversed: start - limit.
func HopStart(pkt *pb.MeshPacket) (uint32, bool) {
	if pkt == nil {
		return 0, false
	}
	raw := pkt.ProtoReflect().GetUnknown()
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return 0, false
		}
		raw = raw[n:]
		if int32(num) == meshPacketHopStartField && typ == protowire.VarintType {
			v, m := protowire.ConsumeVarint(raw)
			if m < 0 {
				return 0, false
			}
			return uint32(v), true
		}
		m := protowire.ConsumeFieldValue(num, typ, raw)
		if m < 0 {
			return 0, false
		}
		raw = raw[m:]
	}
	return 0, false
}

// TextPayload returns the text payload of a MeshPacket, or an empty string
// if the packet is encrypted or not a TEXT_MESSAGE_APP.
func TextPayload(pkt *pb.MeshPacket) string {
	if pkt == nil {
		return ""
	}
	dec, ok := pkt.PayloadVariant.(*pb.MeshPacket_Decoded)
	if !ok || dec.Decoded == nil {
		return ""
	}
	if dec.Decoded.Portnum != pb.PortNum_TEXT_MESSAGE_APP {
		return ""
	}
	return string(dec.Decoded.Payload)
}

// IsReaction reports whether the packet is a Meshtastic "tapback" reaction
// (a TEXT_MESSAGE_APP with Data.Emoji set, referencing another message via
// ReplyId). Such packets are noise when forwarded verbatim to Telegram.
func IsReaction(pkt *pb.MeshPacket) bool {
	if pkt == nil {
		return false
	}
	dec, ok := pkt.PayloadVariant.(*pb.MeshPacket_Decoded)
	if !ok || dec.Decoded == nil {
		return false
	}
	return dec.Decoded.Portnum == pb.PortNum_TEXT_MESSAGE_APP && dec.Decoded.Emoji != 0
}
