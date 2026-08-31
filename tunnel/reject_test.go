package tunnel

import (
	"sync"
	"testing"
	"time"
)

func TestDropParkerReleasesInOrder(t *testing.T) {
	p := &dropParker{}

	var mutex sync.Mutex
	var released []int
	done := make(chan struct{})

	for i := range 3 {
		i := i
		p.Park(time.Duration(i+1)*20*time.Millisecond, func() {
			mutex.Lock()
			released = append(released, i)
			last := len(released) == 3
			mutex.Unlock()
			if last {
				close(done)
			}
		})
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parked connections were not released")
	}

	mutex.Lock()
	defer mutex.Unlock()
	for i, got := range released {
		if got != i {
			t.Fatalf("released out of order: %v", released)
		}
	}

	time.Sleep(50 * time.Millisecond) // let the parker notice the empty queue
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.running {
		t.Fatal("parker goroutine still running with an empty queue")
	}
}

func TestDropParkerEvictsOldestWhenFull(t *testing.T) {
	p := &dropParker{}

	evicted := make(chan int, maxParkedDrops+1)
	for i := range maxParkedDrops + 2 {
		i := i
		p.Park(time.Hour, func() { evicted <- i })
	}

	if len(evicted) != 2 {
		t.Fatalf("expected the 2 oldest entries to be evicted, got %d", len(evicted))
	}
	if first := <-evicted; first != 0 {
		t.Fatalf("expected the oldest entry to be evicted first, got %d", first)
	}

	p.mutex.Lock()
	queued := len(p.queue)
	p.mutex.Unlock()
	if queued != maxParkedDrops {
		t.Fatalf("expected the queue to stay bounded at %d, got %d", maxParkedDrops, queued)
	}
}
