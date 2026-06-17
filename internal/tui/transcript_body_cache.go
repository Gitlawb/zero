package tui

import (
	"container/list"
	"sync"
)

const defaultTranscriptBodyHeightCacheMaxEntries = 512

type transcriptBodyHeightCache struct {
	mu         sync.Mutex
	maxEntries int
	items      map[string]*list.Element
	lru        *list.List
}

type transcriptBodyHeightCacheEntry struct {
	key    string
	height int
}

func newTranscriptBodyHeightCache(maxEntries int) *transcriptBodyHeightCache {
	return &transcriptBodyHeightCache{
		maxEntries: maxEntries,
		items:      map[string]*list.Element{},
		lru:        list.New(),
	}
}

func (c *transcriptBodyHeightCache) get(key string) (int, bool) {
	if c == nil || key == "" {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.items[key]
	if !ok {
		return 0, false
	}
	c.lru.MoveToFront(element)
	return element.Value.(*transcriptBodyHeightCacheEntry).height, true
}

func (c *transcriptBodyHeightCache) set(key string, height int) {
	if c == nil || key == "" || c.maxEntries <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[key]; ok {
		element.Value.(*transcriptBodyHeightCacheEntry).height = height
		c.lru.MoveToFront(element)
		return
	}
	c.items[key] = c.lru.PushFront(&transcriptBodyHeightCacheEntry{key: key, height: height})
	for len(c.items) > c.maxEntries {
		element := c.lru.Back()
		if element == nil {
			return
		}
		entry := element.Value.(*transcriptBodyHeightCacheEntry)
		delete(c.items, entry.key)
		c.lru.Remove(element)
	}
}
