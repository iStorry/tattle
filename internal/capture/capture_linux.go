//go:build linux

// Package capture reads raw frames from a network interface.
package capture

import (
	"context"
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// snapLen is how much of each frame is copied to Tattle. Headers, DNS
// answers and most TLS ClientHellos fit; bulk data is never copied.
const snapLen = 4096

// Source is an open capture on one interface.
type Source struct {
	Name string
	fd   int
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// Open starts capturing on the named interface in promiscuous mode, which
// on a Linux bridge delivers every frame the bridge forwards.
func Open(name string) (*Source, error) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", name, err)
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return nil, fmt.Errorf("opening capture on %s: permission denied (run as root or grant CAP_NET_RAW)", name)
		}
		return nil, fmt.Errorf("opening capture on %s: %w", name, err)
	}
	fail := func(what string, err error) (*Source, error) {
		unix.Close(fd)
		return nil, fmt.Errorf("%s on %s: %w", what, name, err)
	}
	// A larger buffer rides out bursts; failure just leaves the default.
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, 16<<20)
	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ALL), Ifindex: ifi.Index}); err != nil {
		return fail("bind", err)
	}
	mreq := unix.PacketMreq{Ifindex: int32(ifi.Index), Type: unix.PACKET_MR_PROMISC}
	if err := unix.SetsockoptPacketMreq(fd, unix.SOL_PACKET, unix.PACKET_ADD_MEMBERSHIP, &mreq); err != nil {
		return fail("promiscuous mode", err)
	}
	// Wake up once a second so Run can notice cancellation.
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1}); err != nil {
		return fail("receive timeout", err)
	}
	return &Source{Name: name, fd: fd}, nil
}

// Run calls fn for every frame until ctx is cancelled. frame is only valid
// during the call; wireLen is the frame's real length, which can be larger
// than len(frame).
func (s *Source) Run(ctx context.Context, fn func(frame []byte, wireLen int)) error {
	defer unix.Close(s.fd)
	buf := make([]byte, snapLen)
	for ctx.Err() == nil {
		n, _, err := unix.Recvfrom(s.fd, buf, unix.MSG_TRUNC)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.ENETDOWN) {
				continue // interface is down for now; the timeout paces the retry
			}
			return fmt.Errorf("reading from %s: %w", s.Name, err)
		}
		if n <= 0 {
			continue
		}
		fn(buf[:min(n, len(buf))], n)
	}
	return nil
}

// Dropped returns how many frames the kernel discarded because Tattle was
// too slow, since the last call.
func (s *Source) Dropped() uint64 {
	st, err := unix.GetsockoptTpacketStats(s.fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
	if err != nil {
		return 0
	}
	return uint64(st.Drops)
}
