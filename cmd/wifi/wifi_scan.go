//go:build go1.24

//
// Copyright 2017-2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package wifi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvwificonnect/modules/ble"
	wificlient "gitlab.com/pantacor/pvwificonnect/pkg/client"
)

// CommandWifiScan : pvr wifi wifi-scan
func CommandWifiScan() cli.Command {
	var wifiScanConnectWait time.Duration

	cmd := cli.Command{
		Name:    "wifi-scan",
		Aliases: []string{"ws"},
		Usage:   "Ask an Improv BLE device to scan for Wi-Fi networks",
		Description: "Send an Improv Scan RPC and print the networks the device reports. " +
			"The Improv protocol terminates the stream with an empty RPC Result.",
		ArgsUsage: "[address]",
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:        "connect-timeout",
				Usage:       "time to wait for BLE connect",
				Value:       20 * time.Second,
				Destination: &wifiScanConnectWait,
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return cli.NewExitError("wifi-scan needs BLE address argument. See --help", 2)
			}
			return runWifiScanCmd(c.Args().First(), wifiScanConnectWait)
		},
	}
	return cmd
}

func runWifiScanCmd(address string, connectTimeout time.Duration) error {
	ctx, cancel := wificlient.SignalContext(context.Background())
	defer cancel()

	client, err := wificlient.ConnectAndSetup(ctx, address, connectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect() }()

	networks, err := runWifiScan(ctx, client)
	if err != nil {
		return fmt.Errorf("wifi-scan: %w", err)
	}
	printNetworks(networks)
	return nil
}

// runWifiScan triggers an Improv Wi-Fi scan RPC and collects the per-SSID
// results. Improv terminates the stream with an empty RPC Result.
func runWifiScan(ctx context.Context, client *wificlient.Client) ([]wifiNetwork, error) {
	fmt.Fprintln(os.Stderr, "Asking device to scan for Wi-Fi networks...")
	if err := client.SendRPC(ble.RPCPacket{Command: ble.RPCScan}); err != nil {
		return nil, err
	}

	dedup := map[string]wifiNetwork{}
	timeout := time.After(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			if len(dedup) == 0 {
				return nil, errors.New("scan timed out with no results")
			}
			return sortedNetworks(dedup), nil
		case code := <-client.ErrorEvents():
			if code != ble.ErrorNone {
				return nil, fmt.Errorf("device reported error 0x%02x (%s)", code, wificlient.ErrorName(code))
			}
		case pkt := <-client.ResultEvents():
			if pkt.Command != ble.RPCScan {
				continue
			}
			if len(pkt.Data) == 0 {
				return sortedNetworks(dedup), nil
			}
			items, err := ble.DecodeStringList(pkt.Data)
			if err != nil {
				return nil, err
			}
			if len(items) == 0 {
				return sortedNetworks(dedup), nil
			}
			n := wifiNetwork{SSID: items[0], RSSI: -127}
			if len(items) > 1 {
				if rssi, err := strconv.Atoi(items[1]); err == nil {
					n.RSSI = rssi
				}
			}
			if len(items) > 2 {
				n.Auth = items[2]
			}
			if n.SSID != "" {
				if cur, ok := dedup[n.SSID]; !ok || n.RSSI > cur.RSSI {
					dedup[n.SSID] = n
				}
			}
		}
	}
}

type wifiNetwork struct {
	SSID string `json:"ssid"`
	RSSI int    `json:"rssi"`
	Auth string `json:"auth"`
}

func sortedNetworks(m map[string]wifiNetwork) []wifiNetwork {
	out := make([]wifiNetwork, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RSSI > out[j].RSSI })
	return out
}

func printNetworks(networks []wifiNetwork) {
	if len(networks) == 0 {
		fmt.Fprintln(os.Stderr, "(no networks reported)")
		return
	}
	fmt.Printf("%-32s %-6s %s\n", "SSID", "RSSI", "AUTH")
	for _, n := range networks {
		fmt.Printf("%-32s %-6d %s\n", wificlient.Truncate(n.SSID, 32), n.RSSI, n.Auth)
	}
}
