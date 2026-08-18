module finallobby/netservice

go 1.25.0

require golang.zx2c4.com/wireguard v0.0.0-20260522210424-ecfc5a8d5446

require (
	github.com/flynn/noise v1.1.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
)

require (
	finallobby/protocol v0.0.0
	golang.org/x/sys v0.47.0
)

replace finallobby/protocol => ../protocol
