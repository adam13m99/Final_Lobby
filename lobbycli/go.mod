module finallobby/lobbycli

go 1.25.0

require finallobby/protocol v0.0.0

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/flynn/noise v1.1.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace finallobby/protocol => ../protocol
