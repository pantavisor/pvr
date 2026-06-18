//go:build !linux
// +build !linux

package device

import "github.com/urfave/cli"

func CommandTty() cli.Command {
	return cli.Command{
		Name:      "tty",
		Usage:     "Connect to a Pantavisor device over USB serial console",
		UsageText: "pvr device tty",
		Action: func(c *cli.Context) error {
			return cli.NewExitError("serial console (tty) is not supported on this platform (Linux only)", 1)
		},
	}
}
