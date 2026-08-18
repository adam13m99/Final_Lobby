//go:build !linux && !darwin

package server

// getSockoptRcvbuf is not implemented off Unix; the relay only ships on
// Linux, and tests on Windows simply skip the check.
func getSockoptRcvbuf(fd uintptr) (int, error) { return 0, nil }
