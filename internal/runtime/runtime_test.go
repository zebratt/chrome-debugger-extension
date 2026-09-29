package runtime

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateRuntimeDirRejectsBroadPermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "connector")
	if err := EnsurePrivateDir(directory); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("mode %o, want 700", got)
	}
}

func TestPeerUIDOfUnixSocketIsCurrentUser(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "peer.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	uid, err := PeerUID(server.(*net.UnixConn))
	if err != nil {
		t.Fatal(err)
	}
	if uid != uint32(os.Getuid()) {
		t.Fatalf("peer UID %d, want %d", uid, os.Getuid())
	}
}

func TestSocketPathsFitMacOSUnixSocketLimit(t *testing.T) {
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths.BrokerSocket) >= 104 || len(paths.LockFile) >= 104 {
		t.Fatalf("runtime paths are too long: %+v", paths)
	}
	info, err := os.Stat(paths.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("runtime mode %o", info.Mode().Perm())
	}
}

func TestRuntimeDirectoryCanBeIsolatedForTests(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	t.Setenv("CHROME_CONNECTOR_RUN_DIR", directory)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Directory != directory {
		t.Fatalf("directory %q, want %q", paths.Directory, directory)
	}
}
