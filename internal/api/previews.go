package api

import (
	"container/list"
	"sync"
)

// previewCache keeps recent voice previews (LRU, bounded in bytes): the
// voice editor plays the same sample again and again.
type previewCache struct {
	mu       sync.Mutex
	maxBytes int
	size     int
	order    *list.List // front = most recent
	items    map[string]*list.Element
}

type previewEntry struct {
	key  string
	data []byte
}

func newPreviewCache(maxBytes int) *previewCache {
	return &previewCache{maxBytes: maxBytes, order: list.New(), items: map[string]*list.Element{}}
}

func (c *previewCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(e)
	return e.Value.(*previewEntry).data, true
}

func (c *previewCache) put(key string, data []byte) {
	if len(data) > c.maxBytes/4 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		return
	}
	c.items[key] = c.order.PushFront(&previewEntry{key: key, data: data})
	c.size += len(data)
	for c.size > c.maxBytes {
		last := c.order.Back()
		ent := last.Value.(*previewEntry)
		c.order.Remove(last)
		delete(c.items, ent.key)
		c.size -= len(ent.data)
	}
}
