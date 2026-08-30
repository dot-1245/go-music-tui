package artwork

import (
	"image"
	"sync"
)

// Cache is a small source-keyed LRU cache for decoded artwork. Images are
// treated as immutable after decoding, so sharing the image between renders is
// safe and avoids repeating expensive image.Decode and resize work.
type Cache struct {
	mu          sync.Mutex
	max         int
	maxPixels   int64
	usedPixels  int64
	values      map[string]image.Image
	pixelCounts map[string]int64
	order       []string
}

// NewCache creates a cache with at most maxEntries entries. Non-positive sizes
// use a conservative default.
func NewCache(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = 8
	}
	return &Cache{
		max:         maxEntries,
		maxPixels:   16_000_000,
		values:      make(map[string]image.Image),
		pixelCounts: make(map[string]int64),
	}
}

// Get returns a cached image and promotes it to the most recently used entry.
func (c *Cache) Get(source string) (image.Image, bool) {
	if c == nil || source == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	imageValue, ok := c.values[source]
	if !ok {
		return nil, false
	}
	c.promote(source)
	return imageValue, true
}

// Put stores an image under source and evicts the least recently used entry.
func (c *Cache) Put(source string, imageValue image.Image) {
	if c == nil || source == "" || imageValue == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	width, height := imageValue.Bounds().Dx(), imageValue.Bounds().Dy()
	if width <= 0 || height <= 0 {
		return
	}
	if int64(width) > c.maxPixels/int64(height) {
		return
	}
	pixels := int64(width) * int64(height)
	if pixels > c.maxPixels {
		return
	}
	if oldPixels, exists := c.pixelCounts[source]; exists {
		c.usedPixels -= oldPixels
	}
	c.values[source] = imageValue
	c.pixelCounts[source] = pixels
	c.usedPixels += pixels
	c.promote(source)
	for len(c.order) > c.max || c.usedPixels > c.maxPixels {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.values, oldest)
		c.usedPixels -= c.pixelCounts[oldest]
		delete(c.pixelCounts, oldest)
	}
}

// Clear removes every cached image and releases the cache's references to
// decoded artwork.
func (c *Cache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values = make(map[string]image.Image)
	c.pixelCounts = make(map[string]int64)
	c.order = nil
	c.usedPixels = 0
}

func (c *Cache) promote(source string) {
	for index, existing := range c.order {
		if existing == source {
			c.order = append(c.order[:index], c.order[index+1:]...)
			break
		}
	}
	c.order = append(c.order, source)
}
