package main

import (
	"context"
	"os"
	"runtime/debug"
	"syscall"

	fang "charm.land/fang/v2"

	"github.com/shadiramadan/kdelta/cmd"
)

// version comes from Go's built-in build metadata: `go build` stamps the main
// module version from VCS state (tag, pseudo-version, +dirty suffix), and
// `go install module@version` stamps the requested version. No ldflags.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	// No usable module version (e.g. built without VCS metadata): fall back
	// to the raw revision when present.
	var revision string
	var dirty bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return "(devel)"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if dirty {
		revision += "+dirty"
	}
	return revision
}

func main() {
	if err := fang.Execute(
		context.Background(),
		cmd.Root(),
		fang.WithVersion(version()),
		// Cancel cmd.Context() on signals so graceful shutdown and cleanups
		// (server drain, cache close, socket dir removal) actually run.
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	); err != nil {
		os.Exit(1)
	}
}
