package engine

import (
	"sync"
	"sync/atomic"
)

// StopCompactionWorkerForTest terminates the background compaction worker and
// prevents further enqueues. This helper is test-only (in a _test.go file)
// so it is not part of normal builds.
func StopCompactionWorkerForTest() {
	compactionMu.Lock()
	defer compactionMu.Unlock()

	if atomic.LoadInt32(&compactionStopped) != 0 {
		return
	}
	atomic.StoreInt32(&compactionStopped, 1)

	if compactionQueue != nil {
		// Closing the queue will cause runCompactionWorker to exit its
		// range loop and the goroutine to terminate.
		close(compactionQueue)
		compactionQueue = nil
	}

	// Allow the compactionOnce to be re-used in subsequent test runs if
	// desired (resetting the Once). Tests that call this function should
	// ensure they don't race with concurrent enqueues.
	compactionOnce = sync.Once{}
}

// StartCompactionWorkerForTest ensures the background compaction worker is
// running. It undoes StopCompactionWorkerForTest and creates the queue and
// goroutine if needed. Safe to call multiple times.
func StartCompactionWorkerForTest() {
	compactionMu.Lock()
	defer compactionMu.Unlock()

	// If already running, nothing to do.
	if atomic.LoadInt32(&compactionStopped) == 0 && compactionQueue != nil {
		return
	}

	// Mark as not stopped so enqueueCompaction may create/start the worker.
	atomic.StoreInt32(&compactionStopped, 0)

	// Reset the Once so a subsequent enqueueCompaction will initialize the
	// queue and start the worker. Alternatively, start it directly here.
	compactionOnce = sync.Once{}
	// Eagerly create the queue and worker to make the start immediate for
	// tests that expect background compaction to run.
	if compactionQueue == nil {
		compactionQueue = make(chan compactionTask, 64)
		go runCompactionWorker()
	}
}
