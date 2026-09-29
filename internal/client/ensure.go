package client

import (
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func EnsureBroker(socket, lock string, start func() error) error {
	if socketReady(socket) {
		return nil
	}
	file, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)
	if socketReady(socket) {
		return nil
	}
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := start(); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if socketReady(socket) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("broker did not open socket %s within 3 seconds", socket)
}

func socketReady(path string) bool {
	connection, err := net.DialTimeout("unix", path, 50*time.Millisecond)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}
