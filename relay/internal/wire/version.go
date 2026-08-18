// Package wire defines the on-the-wire packet format used between the
// Windows net-service and the relay.
package wire

// ProtocolVersion is bumped whenever the packet layout changes
// incompatibly. Clients and relay must agree exactly.
const ProtocolVersion uint8 = 1
