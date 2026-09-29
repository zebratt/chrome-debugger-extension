package runtime

import (
	"errors"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type Paths struct {
	Directory    string
	BrokerSocket string
	LockFile     string
}

func DefaultPaths() (Paths, error) {
	directory := filepath.Join(os.TempDir(), "chrome-connector")
	if override := os.Getenv("CHROME_CONNECTOR_RUN_DIR"); override != "" {
		directory = override
	}
	if err := EnsurePrivateDir(directory); err != nil {
		return Paths{}, err
	}
	paths := Paths{
		Directory:    directory,
		BrokerSocket: filepath.Join(directory, "broker.sock"),
		LockFile:     filepath.Join(directory, "broker.lock"),
	}
	if len(paths.BrokerSocket) >= 104 {
		return Paths{}, errors.New("runtime socket path exceeds macOS limit")
	}
	return paths, nil
}

func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("runtime path is not a directory")
	}
	return os.Chmod(path, 0700)
}

func PeerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credential *unix.Xucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if socketErr != nil {
		return 0, socketErr
	}
	return credential.Uid, nil
}
