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
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/urfave/cli"
	wificlient "gitlab.com/pantacor/pvwificonnect/pkg/client"
)

// CommandDeviceScan : pvr wifi device-scan
func CommandDeviceScan() cli.Command {
	var (
		scanTimeout time.Duration
		scanJSON    bool
	)

	cmd := cli.Command{
		Name:        "device-scan",
		Aliases:     []string{"ds"},
		Usage:       "Scan for Improv BLE devices",
		Description: "Scan for nearby BLE devices advertising the Improv Wi-Fi service.",
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:        "timeout",
				Usage:       "stop scanning after this duration (0 = forever)",
				Value:       15 * time.Second,
				Destination: &scanTimeout,
			},
			cli.BoolFlag{
				Name:        "json",
				Usage:       "emit one JSON object per device",
				Destination: &scanJSON,
			},
		},
		Action: func(c *cli.Context) error {
			return runScan(scanTimeout, scanJSON)
		},
	}
	return cmd
}

func runScan(scanTimeout time.Duration, scanJSON bool) error {
	client, err := wificlient.NewClient()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if scanTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, scanTimeout)
		defer cancel()
	}

	var (
		mu      sync.Mutex
		devices = map[string]wificlient.ImprovDevice{}
		printed = map[string]bool{}
	)

	if !scanJSON {
		fmt.Fprintf(os.Stderr, "Scanning for Improv devices (press Ctrl+C to stop)...\n")
	}

	onResult := func(d wificlient.ImprovDevice) {
		mu.Lock()
		devices[d.Address] = d
		if scanJSON && !printed[d.Address] {
			printed[d.Address] = true
			mu.Unlock()
			b, _ := json.Marshal(jsonDevice(d))
			fmt.Println(string(b))
			return
		}
		if !scanJSON && !printed[d.Address] {
			printed[d.Address] = true
			mu.Unlock()
			fmt.Fprintf(os.Stderr, "  found %s (%s) rssi=%d %s\n",
				d.Address, wificlient.NameOr(d.Name), d.RSSI, wificlient.StateHint(d))
			return
		}
		mu.Unlock()
	}

	if err := client.Scan(ctx, onResult); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if scanJSON {
		return nil
	}

	if len(devices) == 0 {
		fmt.Fprintln(os.Stderr, "No Improv devices found.")
		return nil
	}

	list := make([]wificlient.ImprovDevice, 0, len(devices))
	for _, d := range devices {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].RSSI > list[j].RSSI })

	fmt.Println()
	fmt.Printf("%-19s %-24s %-4s %-12s %s\n", "ADDRESS", "NAME", "RSSI", "STATE", "CAPABILITIES")
	for _, d := range list {
		fmt.Printf("%-19s %-24s %-4d %-12s %s\n",
			d.Address,
			wificlient.Truncate(wificlient.NameOr(d.Name), 24),
			d.RSSI,
			wificlient.StateName(d.State, d.HasState),
			strings.Join(wificlient.CapList(d.Capabilities), ", "),
		)
	}
	return nil
}

type jsonDeviceOut struct {
	Address      string   `json:"address"`
	Name         string   `json:"name,omitempty"`
	RSSI         int16    `json:"rssi"`
	State        string   `json:"state,omitempty"`
	StateByte    *byte    `json:"state_byte,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

func jsonDevice(d wificlient.ImprovDevice) jsonDeviceOut {
	out := jsonDeviceOut{
		Address: d.Address,
		Name:    d.Name,
		RSSI:    d.RSSI,
	}
	if d.HasState {
		out.StateByte = &d.State
		out.State = wificlient.StateName(d.State, true)
		out.Capabilities = wificlient.CapList(d.Capabilities)
	}
	return out
}
