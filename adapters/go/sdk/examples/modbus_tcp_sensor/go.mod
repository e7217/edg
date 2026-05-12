module github.com/e7217/edg/adapters/go/sdk/examples/modbus_tcp_sensor

go 1.25.4

replace github.com/e7217/edg/adapters/go/sdk => ../..

require (
	github.com/e7217/edg/adapters/go/sdk v0.0.0-00010101000000-000000000000
	github.com/goburrow/modbus v0.1.0
	github.com/tbrandon/mbserver v0.0.0-20231208015628-36eb59221ac2
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/goburrow/serial v0.1.0 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/nats-io/nats.go v1.51.0 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
)
