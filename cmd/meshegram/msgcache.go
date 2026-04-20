package main

import "sync"

// msgEntry is the per-message state we keep for a bridged mesh message:
// the Telegram message ID, the HTML body we originally sent, and any
// "fallback" emoji reactions we've appended via editMessageText because
// Telegram refused them as native reactions.
type msgEntry struct {
	tgID      int
	body      string
	reactions []string
}

// msgCache maps mesh MeshPacket.Id to the bridge state we keep for that
// message. Bounded FIFO — oldest entries are evicted when capacity is
// exceeded.
type msgCache struct {
	mu    sync.Mutex
	cap   int
	m     map[uint32]*msgEntry
	order []uint32
}

func newMsgCache(capacity int) *msgCache {
	return &msgCache{
		cap:   capacity,
		m:     make(map[uint32]*msgEntry, capacity),
		order: make([]uint32, 0, capacity),
	}
}

func (c *msgCache) Put(meshID uint32, tgID int, body string) {
	if meshID == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, exists := c.m[meshID]; exists {
		e.tgID = tgID
		e.body = body
		return
	}
	if len(c.m) >= c.cap && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.m, oldest)
	}
	c.m[meshID] = &msgEntry{tgID: tgID, body: body}
	c.order = append(c.order, meshID)
}

func (c *msgCache) Get(meshID uint32) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[meshID]
	if !ok {
		return 0, false
	}
	return e.tgID, true
}

// AddFallbackReaction records an emoji reaction Telegram refused as a
// native reaction. Emojis are deduplicated in first-seen order. Returns
// a copy of the updated entry and whether the mesh message is still
// cached.
func (c *msgCache) AddFallbackReaction(meshID uint32, emoji string) (tgID int, body string, reactions []string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[meshID]
	if !ok {
		return 0, "", nil, false
	}
	found := false
	for _, r := range e.reactions {
		if r == emoji {
			found = true
			break
		}
	}
	if !found {
		e.reactions = append(e.reactions, emoji)
	}
	return e.tgID, e.body, append([]string(nil), e.reactions...), true
}
