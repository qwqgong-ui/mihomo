package tunnel

import (
	"errors"
	"sync"
	"time"

	C "github.com/metacubex/mihomo/constant"

	"golang.org/x/exp/slices"
)

// maxParkedDrops bounds how many connections/sessions may be parked at the
// same time. A parked entry only holds its socket (or nat table slot), but a
// flood of matched REJECT-DROP rules would still pile up file descriptors, so
// the oldest entries are released early once the limit is hit.
const maxParkedDrops = 4096

// parkedDrop is one connection waiting to be released by the parker.
type parkedDrop struct {
	seq      uint64
	deadline time.Time
	release  func()
}

// dropParker keeps connections that matched a reject rule alive without
// spending any per-connection resource on them: no relay loop, no read
// goroutine and no timer. All entries share the same lifetime, so the queue
// stays ordered by deadline and a single lazily started goroutine can release
// them in order.
type dropParker struct {
	mutex   sync.Mutex
	queue   []parkedDrop
	nextSeq uint64
	running bool
}

var defaultDropParker = &dropParker{}

// Park releases the given connection after ttl and returns immediately.
func (p *dropParker) Park(ttl time.Duration, release func()) {
	var evicted []func()
	p.mutex.Lock()
	for len(p.queue) >= maxParkedDrops {
		evicted = append(evicted, p.queue[0].release)
		p.queue = p.queue[1:]
	}
	p.nextSeq++
	p.queue = append(p.queue, parkedDrop{
		seq:      p.nextSeq,
		deadline: time.Now().Add(ttl),
		release:  release,
	})
	if !p.running {
		p.running = true
		go p.run()
	}
	p.mutex.Unlock()

	for _, release := range evicted {
		release()
	}
}

func (p *dropParker) run() {
	for {
		p.mutex.Lock()
		if len(p.queue) == 0 {
			p.running = false // no work left, don't keep a goroutine around
			p.queue = nil     // release the backing array
			p.mutex.Unlock()
			return
		}
		drop := p.queue[0]
		p.mutex.Unlock()

		if wait := time.Until(drop.deadline); wait > 0 {
			time.Sleep(wait)
		}

		p.mutex.Lock()
		released := len(p.queue) > 0 && p.queue[0].seq == drop.seq
		if released {
			p.queue = p.queue[1:]
		}
		p.mutex.Unlock()

		if released { // otherwise Park already evicted and released it
			drop.release()
		}
	}
}

// rejectAdapter returns the reject adapter proxy would finally dial through,
// unwrapping any group nesting, and the chain leading to it.
func rejectAdapter(proxy C.Proxy, metadata *C.Metadata) (C.Proxy, C.Chain, bool) {
	var chain C.Chain
	for adapter := proxy; adapter != nil; adapter = adapter.Unwrap(metadata, false) {
		chain = append(chain, adapter.Name())
		switch adapter.Type() {
		case C.Reject, C.RejectDrop:
			slices.Reverse(chain) // the innermost adapter comes first in a chain
			return adapter, chain, true
		}
	}
	return nil, nil, false
}

// errRejectAbsorbed tells the udp dial caller that the nat table entry was
// deliberately kept alive to absorb the rest of a rejected session.
var errRejectAbsorbed = errors.New("rejected session absorbed")
