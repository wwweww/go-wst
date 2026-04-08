package buffer

import "sync"

const (
	defaultBufferSize = 4096
	maxBufferSize     = 65536
)

// Pool is a buffer pool for reusing byte slices.
type Pool struct {
	pool     sync.Pool
	initSize int
}

// NewPool creates a new buffer pool with the specified initial buffer size.
func NewPool(initSize int) *Pool {
	if initSize <= 0 {
		initSize = defaultBufferSize
	}
	return &Pool{
		initSize: initSize,
		pool: sync.Pool{
			New: func() any {
				buf := make([]byte, initSize)
				return &buf
			},
		},
	}
}

// Get retrieves a buffer from the pool.
// The returned buffer should be Put back after use.
func (p *Pool) Get() *[]byte {
	return p.pool.Get().(*[]byte)
}

// Put returns a buffer to the pool.
// Do not use the buffer after putting it back.
func (p *Pool) Put(buf *[]byte) {
	// Don't put oversized buffers back
	if cap(*buf) > maxBufferSize {
		return
	}
	// Reset slice length but keep capacity
	*buf = (*buf)[:cap(*buf)]
	p.pool.Put(buf)
}

// GetWithSize retrieves a buffer with at least the specified size.
// If the pool's default size is too small, a new buffer is allocated.
func (p *Pool) GetWithSize(size int) *[]byte {
	if size <= p.initSize {
		return p.Get()
	}
	buf := make([]byte, size)
	return &buf
}

// DefaultPool is the default buffer pool.
var DefaultPool = NewPool(defaultBufferSize)
