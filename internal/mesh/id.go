package mesh

import (
	"crypto/rand"
	"encoding/binary"
)

// packetID returns a random non-zero uint32 for MeshPacket.Id.
// Zero means "no ack required" and is reserved.
func packetID() uint32 {
	var buf [4]byte
	for {
		_, _ = rand.Read(buf[:])
		id := binary.BigEndian.Uint32(buf[:])
		if id != 0 {
			return id
		}
	}
}
