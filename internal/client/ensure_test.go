package client

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEnsureBrokerStartsOnlyOnceAcrossConcurrentClients(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-ensure-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	socket := filepath.Join(directory, "broker.sock")
	lock := filepath.Join(directory, "broker.lock")
	var listener net.Listener
	var mutex sync.Mutex
	starts := 0
	start := func() error {
		mutex.Lock()
		defer mutex.Unlock()
		starts++
		var err error
		listener, err = net.Listen("unix", socket)
		return err
	}
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := EnsureBroker(socket, lock, start); err != nil {
				t.Errorf("ensure: %v", err)
			}
		}()
	}
	group.Wait()
	if listener != nil {
		listener.Close()
	}
	if starts != 1 {
		t.Fatalf("broker started %d times, want once", starts)
	}
}
