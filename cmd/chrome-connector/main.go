package main

import (
	"context"
	"encoding/base64"
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

var convenienceMethods = map[string]map[string]bool{
	"system":  {"ping": true, "policy": true, "capabilities": true},
	"browser": {"list": true, "run": true},
	"tab":     {"list": true, "open": true, "info": true, "navigate": true, "snapshot": true, "screenshot": true, "claim": true, "renew": true, "release": true, "click": true, "select": true, "type": true, "key": true, "scroll": true, "wait": true},
	"cdp":     {"send": true},
}

func expandConvenienceArgs(args []string) []string {
	if len(args) < 2 || !convenienceMethods[args[0]][args[1]] {
		return args
	}
	return append([]string{"call", args[0] + "." + args[1]}, args[2:]...)
}

func callArguments(args []string) (any, string, error) {
	if len(args) < 2 || len(args) > 5 {
		return nil, "", fmt.Errorf("usage: chrome-connector call METHOD [PARAMS_JSON] [--output FILE]")
	}
	var params any
	if len(args) >= 3 {
		if err := json.Unmarshal([]byte(args[2]), &params); err != nil {
			return nil, "", err
		}
	}
	if len(args) <= 3 {
		return params, "", nil
	}
	if len(args) != 5 || args[1] != "tab.screenshot" || args[3] != "--output" || args[4] == "" {
		return nil, "", fmt.Errorf("--output FILE is only available for tab.screenshot")
	}
	return params, args[4], nil
}

func normalizeArgs(args []string) []string {
	if len(args) > 0 && strings.HasPrefix(args[0], "chrome-extension://") {
		return []string{"native-host"}
	}
	return args
}

func run(args []string, stdout, stderr io.Writer) int {
	args = expandConvenienceArgs(args)
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
		params, outputPath, err := callArguments(args)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if err := ensureBroker(paths); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		response, err := callWithWorkflow(context.Background(), paths.BrokerSocket, args[1], params)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if outputPath != "" && response.Error == nil {
			if err := saveScreenshot(&response, outputPath); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		encoded, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
		if response.Error != nil || args[1] == "browser.run" && workflowNeedsAttention(response) {
			return 1
		}
		return 0
	case "mcp":
		if err := mcp.Run(os.Stdin, stdout, func(method string, params any) (protocol.Response, error) {
			if err := ensureBroker(paths); err != nil {
				return protocol.Response{}, err
			}
			return callWithWorkflow(context.Background(), paths.BrokerSocket, method, params)
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
		if err := client.StopBroker(paths.BrokerSocket, 3*time.Second); err != nil {
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
		if err := ensureBrokerFrom(paths, result.BinaryPath); err != nil {
			fmt.Fprintln(stderr, "installed files but new broker did not start:", err)
			return 1
		}
		if err := reloadConnectedExtensions(paths.BrokerSocket); err != nil {
			fmt.Fprintln(stderr, "installed broker, but extension host reload needs attention:", err)
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
		if report.HostManifest && report.HostBinary && report.PolicyValid {
			if err := ensureBroker(paths); err != nil {
				report.Issues = append(report.Issues, "broker could not start: "+err.Error())
			} else {
				if response, err := client.Call(paths.BrokerSocket, "system.capabilities", nil, 5*time.Second); err != nil || response.Error != nil {
					report.Issues = append(report.Issues, "broker capabilities could not be read")
				} else if capabilities, ok := response.Result.(map[string]any); ok {
					annotateCapabilitiesReport(&report, capabilities)
				}
				if response, err := client.Call(paths.BrokerSocket, "browser.list", nil, 5*time.Second); err != nil || response.Error != nil {
					report.Issues = append(report.Issues, "browser discovery failed")
				} else if state, ok := response.Result.(map[string]any); ok {
					annotateBrowserReport(&report, state)
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
		if err := client.StopBroker(paths.BrokerSocket, 3*time.Second); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		root := filepath.Join(configRoot, "chrome-connector")
		if err := install.Rollback(root); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := ensureBrokerFrom(paths, filepath.Join(root, "current")); err != nil {
			fmt.Fprintln(stderr, "rolled back files but broker did not start:", err)
			return 1
		}
		if err := reloadConnectedExtensions(paths.BrokerSocket); err != nil {
			fmt.Fprintln(stderr, "rolled back broker, but extension host reload needs attention:", err)
			return 1
		}
		fmt.Fprintln(stdout, "previous host version and broker selected; connected extension hosts reloaded")
		return 0
	default:
		fmt.Fprintln(stderr, "unknown command")
		return 2
	}
}

func annotateCapabilitiesReport(report *doctor.Report, capabilities map[string]any) {
	report.BrokerProtocolVersion, _ = capabilities["protocolVersion"].(string)
	report.BrokerVersion, _ = capabilities["brokerVersion"].(string)
}

func annotateBrowserReport(report *doctor.Report, state map[string]any) {
	report.BrowserState, _ = state["discoveryState"].(string)
	profiles, _ := state["profiles"].([]any)
	report.ExtensionVersions = make(map[string]string)
	report.HostVersions = make(map[string]string)
	brokerVersion := report.BrokerProtocolVersion
	if brokerVersion == "" {
		brokerVersion = broker.ProtocolVersion
	}
	for _, item := range profiles {
		profile, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := profile["profileId"].(string)
		version, _ := profile["extensionVersion"].(string)
		peerProtocol, _ := profile["protocolVersion"].(string)
		hostVersion, _ := profile["hostVersion"].(string)
		status, _ := profile["status"].(string)
		report.ExtensionVersions[id] = version
		report.HostVersions[id] = hostVersion
		if status == "online" && protocol.CompatibleVersion(brokerVersion, peerProtocol) {
			report.ConnectedProfiles++
		} else if status == "version_mismatch" || !protocol.CompatibleVersion(brokerVersion, peerProtocol) {
			report.Issues = append(report.Issues, "profile "+id+" uses an incompatible protocol; reload or update the extension")
		}
	}
	if report.ConnectedProfiles == 0 && report.BrowserState != "version_mismatch" {
		report.Issues = append(report.Issues, "Chrome extension is not connected")
	}
}

func saveScreenshot(response *protocol.Response, outputPath string) error {
	result, ok := response.Result.(map[string]any)
	if !ok {
		return fmt.Errorf("screenshot response is not an object")
	}
	encoded, ok := result["dataBase64"].(string)
	if !ok {
		return fmt.Errorf("screenshot response has no image data")
	}
	image, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	if len(image) > 8*1024*1024 {
		return fmt.Errorf("screenshot exceeds 8 MiB")
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	var written int
	if written, err = file.Write(image); err != nil || written != len(image) {
		file.Close()
		os.Remove(outputPath)
		if err == nil {
			return io.ErrShortWrite
		}
		return err
	}
	if err = file.Close(); err != nil {
		os.Remove(outputPath)
		return err
	}
	delete(result, "dataBase64")
	result["output"] = outputPath
	return nil
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
	if err := os.Chmod(paths.BrokerSocket, 0600); err != nil {
		listener.Close()
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return broker.NewServer(activePolicy, 120*time.Second).Serve(ctx, listener)
}

func ensureBroker(paths runtimepath.Paths) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return ensureBrokerFrom(paths, executable)
}

func ensureBrokerFrom(paths runtimepath.Paths, executable string) error {
	return client.EnsureBroker(paths.BrokerSocket, paths.LockFile, func() error {
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

func reloadConnectedExtensions(socket string) error {
	listed, err := client.Call(socket, "browser.list", nil, 5*time.Second)
	if err != nil {
		return err
	}
	if listed.Error != nil {
		return fmt.Errorf("browser discovery: %s", listed.Error.Message)
	}
	result, ok := listed.Result.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid browser list response")
	}
	profiles, _ := result["profiles"].([]any)
	for _, item := range profiles {
		profile, ok := item.(map[string]any)
		if !ok {
			continue
		}
		profileID, _ := profile["profileId"].(string)
		previousID, _ := profile["connectionId"].(string)
		if profileID == "" {
			continue
		}
		response, err := client.Call(socket, "extension.reload", map[string]any{"profileId": profileID}, 5*time.Second)
		if err != nil {
			return err
		}
		if response.Error != nil {
			return fmt.Errorf("profile %s cannot auto-reload (%s); open chrome://extensions and reload Chrome Connector manually", profileID, response.Error.Data.Kind)
		}
		deadline := time.Now().Add(5 * time.Second)
		connected := false
		for time.Now().Before(deadline) {
			current, err := client.Call(socket, "browser.list", nil, 5*time.Second)
			if err == nil && current.Error == nil {
				state, _ := current.Result.(map[string]any)
				entries, _ := state["profiles"].([]any)
				for _, entry := range entries {
					candidate, _ := entry.(map[string]any)
					id, _ := candidate["profileId"].(string)
					connectionID, _ := candidate["connectionId"].(string)
					if id == profileID && connectionID != "" && connectionID != previousID {
						connected = true
						break
					}
				}
			}
			if connected {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !connected {
			return fmt.Errorf("profile %s did not reconnect after extension reload", profileID)
		}
	}
	return nil
}
