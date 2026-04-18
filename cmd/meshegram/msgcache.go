package main

import "sync"

// msgCache maps mesh MeshPacket.Id to the Telegram message ID that carries
// that mesh message in the bridged chat. Bounded FIFO — oldest entries are
// evicted when capacity is exceeded.
type msgCache struct {
	mu    sync.Mutex
	cap   int
	m     map[uint32]int
	order []uint32
}

func newMsgCache(capacity int) *msgCache {
	return &msgCache{
		cap:   capacity,
		m:     make(map[uint32]int, capacity),
		order: make([]uint32, 0, capacity),
	}
}

func (c *msgCache) Put(meshID uint32, tgID int) {
	if meshID == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.m[meshID]; exists {
		c.m[meshID] = tgID
		return
	}
	if len(c.m) >= c.cap && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.m, oldest)
	}
	c.m[meshID] = tgID
	c.order = append(c.order, meshID)
}

func (c *msgCache) Get(meshID uint32) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.m[meshID]
	return id, ok
}
