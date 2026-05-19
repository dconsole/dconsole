package transport

import (
	"fmt"
	"os/exec"
	"sync"
)

// CLIAvailable returns nil if `name` is on PATH; otherwise an actionable
// error suggesting the install. Result is cached per process.
func CLIAvailable(name string) error {
	if err := pathCache.lookup(name); err != nil {
		return fmt.Errorf("transport requires %q on PATH (%w)", name, err)
	}
	return nil
}

type lookupCache struct {
	mu     sync.Mutex
	result map[string]error
}

func (c *lookupCache) lookup(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.result == nil {
		c.result = map[string]error{}
	}
	if err, ok := c.result[name]; ok {
		return err
	}
	_, err := exec.LookPath(name)
	c.result[name] = err
	return err
}

var pathCache = &lookupCache{}
