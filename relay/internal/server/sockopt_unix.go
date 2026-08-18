//go:build linux || darwin

package server

import "golang.org/x/sys/unix"

// getSockoptRcvbuf returns the effective receive buffer. Linux reports twice
// what was asked for - it reserves half for bookkeeping - so halve it to get
// the number that actually holds payload.
func getSockoptRcvbuf(fd uintptr) (int, error) {
	v, err := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF)
	if err != nil {
		return 0, err
	}
	return v / 2, nil
}
