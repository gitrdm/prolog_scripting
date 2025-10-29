package engine

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestStream_ConcurrentWriteClose exercises concurrent writes and a concurrent Close
// to ensure the Stream locking does not deadlock and there are no races under -race.
func TestStream_ConcurrentWriteClose(t *testing.T) {
	// Use a bytes.Buffer as the underlying sink. Stream's own locking should
	// serialize access to it.
	buf := &bytes.Buffer{}
	s := NewOutputBinaryStream(buf)

	var wg sync.WaitGroup
	writers := 8
	writesPerWriter := 1000

	// Start several writers that repeatedly call WriteByte.
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(id)))
			for i := 0; i < writesPerWriter; i++ {
				// occasionally sleep a tiny amount to increase interleaving
				if r.Intn(100) == 0 {
					time.Sleep(time.Microsecond)
				}
				// Ignore errors — Close may race with writes; we only care about deadlocks/races.
				_ = s.WriteByte(byte(i % 256))
			}
		}(w)
	}

	// Start a goroutine that will randomly call Close while writers are active.
	closerDone := make(chan struct{})
	go func() {
		// Random short sleeps then close; do it a few times to increase stress.
		r := rand.New(rand.NewSource(time.Now().UnixNano() + 999))
		for i := 0; i < 3; i++ {
			time.Sleep(time.Duration(r.Intn(200)) * time.Microsecond)
			_ = s.Close()
		}
		close(closerDone)
	}()

	// Wait for writers to finish (with a timeout guard to detect hangs).
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// all good
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent writers to finish; possible deadlock")
	}

	// ensure closer goroutine finished too
	select {
	case <-closerDone:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for closer goroutine; possible deadlock")
	}
}
