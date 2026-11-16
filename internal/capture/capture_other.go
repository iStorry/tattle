//go:build !linux

// Package capture reads raw frames from a network interface.
package capture

import (
	"context"
	"errors"
)

// Source is an open capture on one interface.
type Source struct{ Name string }

// Open always fails here: live capture uses Linux packet sockets.
func Open(name string) (*Source, error) {
	return nil, errors.New("live capture only works on Linux (the Proxmox host); use --demo to try Tattle on this machine")
}

func (s *Source) Run(ctx context.Context, fn func(frame []byte, wireLen int)) error { return nil }

func (s *Source) Dropped() uint64 { return 0 }
