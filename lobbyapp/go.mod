module finallobby/lobbyapp

go 1.25.0

require (
	finallobby/client v0.0.0
	finallobby/protocol v0.0.0
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace finallobby/client => ../client

replace finallobby/protocol => ../protocol
