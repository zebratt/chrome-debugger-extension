package client

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"chrome-connector/internal/broker"
	"chrome-connector/internal/policy"
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

func TestStopBrokerAcknowledgesAndClosesListener(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-stop-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := broker.NewServer(policy.Default(), time.Minute).Serve(ctx, listener)
		time.Sleep(50 * time.Millisecond)
		os.Remove(path)
		done <- err
	}()
	if err := StopBroker(path, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("stop returned before socket cleanup: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("broker stayed alive")
	}
}
