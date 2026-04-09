package web

import "sync"

// FrameStore holds the latest JPEG frame and broadcasts to waiting readers.
type FrameStore struct {
	mu      sync.Mutex
	cond    *sync.Cond
	frame   []byte
	version uint64
}

func NewFrameStore() *FrameStore {
	fs := &FrameStore{}
	fs.cond = sync.NewCond(&fs.mu)
	return fs
}

// SetFrame stores a new JPEG frame and wakes all waiting readers.
func (fs *FrameStore) SetFrame(jpeg []byte) {
	fs.mu.Lock()
	fs.frame = jpeg
	fs.version++
	fs.mu.Unlock()
	fs.cond.Broadcast()
}

// WaitFrame blocks until a frame newer than lastVersion is available.
func (fs *FrameStore) WaitFrame(lastVersion uint64) ([]byte, uint64) {
	fs.mu.Lock()
	for fs.version == lastVersion {
		fs.cond.Wait()
	}
	frame := fs.frame
	ver := fs.version
	fs.mu.Unlock()
	return frame, ver
}
