module lobbybaz/lobbyapp

go 1.25.0

require (
	lobbybaz/client v0.0.0
	lobbybaz/protocol v0.0.0
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace lobbybaz/client => ../client

replace lobbybaz/protocol => ../protocol
