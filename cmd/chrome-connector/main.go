package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"chrome-connector/internal/broker"
	"chrome-connector/internal/client"
	"chrome-connector/internal/doctor"
	"chrome-connector/internal/install"
	"chrome-connector/internal/mcp"
	"chrome-connector/internal/native"
	"chrome-connector/internal/policy"
	"chrome-connector/internal/protocol"
	runtimepath "chrome-connector/internal/runtime"
)

func main() { os.Exit(run(normalizeArgs(os.Args[1:]), os.Stdout, os.Stderr)) }

func normalizeArgs(args []string) []string {
	if len(args) > 0 && strings.HasPrefix(args[0], "chrome-extension://") {
		return []string{"native-host"}
	}
	return args
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: chrome-connector <broker ensure|broker serve|native-host|call METHOD [PARAMS_JSON]>")
		return 2
	}
	paths, err := runtimepath.DefaultPaths()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch args[0] {
	case "native-host":
		if err := native.Run(context.Background(), os.Stdin, stdout, paths.BrokerSocket); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "broker":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "broker requires ensure or serve")
			return 2
		}
		if args[1] == "serve" {
			if err := serveBroker(paths); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		}
		if args[1] == "ensure" {
			if err := ensureBroker(paths); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintln(stdout, paths.BrokerSocket)
			return 0
		}
		fmt.Fprintln(stderr, "unknown broker command")
		return 2
	case "call":
		if len(args) < 2 || len(args) > 3 {
			fmt.Fprintln(stderr, "usage: chrome-connector call METHOD [PARAMS_JSON]")
			return 2
		}
		var params any
		if len(args) == 3 {
			if err := json.Unmarshal([]byte(args[2]), &params); err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
		}
		if err := ensureBroker(paths); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		response, err := client.Call(paths.BrokerSocket, args[1], params, 20*time.Second)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		encoded, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
		if response.Error != nil {
			return 1
		}
		return 0
	case "mcp":
		if err := mcp.Run(os.Stdin, stdout, func(method string, params any) (protocol.Response, error) {
			if err := ensureBroker(paths); err != nil {
				return protocol.Response{}, err
			}
			return client.Call(paths.BrokerSocket, method, params, 20*time.Second)
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "install":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "usage: chrome-connector install EXTENSION_ID EXTENSION_DIR")
			return 2
		}
		configRoot, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		extensionDir, err := filepath.Abs(args[2])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		result, err := install.Install(install.Config{
			Root:         filepath.Join(configRoot, "chrome-connector"),
			HostManifest: filepath.Join(configRoot, "Google", "Chrome", "NativeMessagingHosts", "com.chromeconnector.bridge.json"),
			SourceBinary: executable, ExtensionDir: extensionDir, ExtensionID: args[1],
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		encoded, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(encoded))
		return 0
	case "doctor":
		configRoot, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		root := filepath.Join(configRoot, "chrome-connector")
		report := doctor.Check(filepath.Join(configRoot, "Google", "Chrome", "NativeMessagingHosts", "com.chromeconnector.bridge.json"), filepath.Join(root, "config.json"))
		report.BrokerProtocolVersion = broker.ProtocolVersion
		if report.HostManifest && report.HostBinary && report.PolicyValid {
			if err := ensureBroker(paths); err != nil {
				report.Issues = append(report.Issues, "broker could not start: "+err.Error())
			} else if response, err := client.Call(paths.BrokerSocket, "browser.list", nil, 5*time.Second); err != nil || response.Error != nil {
				report.Issues = append(report.Issues, "browser discovery failed")
			} else if state, ok := response.Result.(map[string]any); ok {
				report.BrowserState, _ = state["discoveryState"].(string)
				if profiles, ok := state["profiles"].([]any); ok {
					report.ConnectedProfiles = len(profiles)
					report.ExtensionVersions = make(map[string]string)
					for _, item := range profiles {
						profile, ok := item.(map[string]any)
						if !ok {
							continue
						}
						id, _ := profile["profileId"].(string)
						version, _ := profile["extensionVersion"].(string)
						peerProtocol, _ := profile["protocolVersion"].(string)
						report.ExtensionVersions[id] = version
						if !protocol.CompatibleVersion(broker.ProtocolVersion, peerProtocol) {
							report.Issues = append(report.Issues, "profile "+id+" uses an incompatible protocol; reload or update the extension")
						}
					}
				}
				if report.ConnectedProfiles == 0 {
					report.Issues = append(report.Issues, "Chrome extension is not connected")
				}
			}
		}
		encoded, _ := json.MarshalIndent(report, "", "  ")
		fmt.Fprintln(stdout, string(encoded))
		if len(report.Issues) > 0 {
			return 1
		}
		return 0
	case "rollback":
		configRoot, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := install.Rollback(filepath.Join(configRoot, "chrome-connector")); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "previous host version selected; reload the Chrome Connector extension to start it")
		return 0
	default:
		fmt.Fprintln(stderr, "unknown command")
		return 2
	}
}

func serveBroker(paths runtimepath.Paths) error {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	activePolicy, err := policy.Load(filepath.Join(configRoot, "chrome-connector", "config.json"))
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", paths.BrokerSocket)
	if err != nil {
		return err
	}
	defer os.Remove(paths.BrokerSocket)
	if err := os.Chmod(paths.BrokerSocket, 0600); err != nil {
		listener.Close()
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return broker.NewServer(activePolicy, 120*time.Second).Serve(ctx, listener)
}

func ensureBroker(paths runtimepath.Paths) error {
	return client.EnsureBroker(paths.BrokerSocket, paths.LockFile, func() error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		command := exec.Command(executable, "broker", "serve")
		command.Env = os.Environ()
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer devNull.Close()
		command.Stdout = devNull
		command.Stderr = devNull
		if err := command.Start(); err != nil {
			return err
		}
		return command.Process.Release()
	})
}
