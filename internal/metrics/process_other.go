//go:build !linux

package metrics

// registerProcess is a no-op off Linux: the process_* family is defined in
// terms of /proc, and inventing approximations from other sources would make
// dashboards that silently mean something different per platform. EDG ships on
// Linux; this build tag exists so `go build` and `go test` work on a developer
// laptop.
func (r *Registry) registerProcess() {}
