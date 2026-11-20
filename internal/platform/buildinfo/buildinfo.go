// Package buildinfo reports what binary is running, from the metadata the Go
// toolchain embeds at build time (module version and VCS stamp). No ldflags:
// `go build` inside a git checkout records everything needed.
package buildinfo

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Info identifies a build. Fields are empty when the toolchain did not record
// them, e.g. `go run` or a build outside a git checkout.
type Info struct {
	Version    string `json:"version"`
	Commit     string `json:"commit,omitempty"`
	CommitTime string `json:"commit_time,omitempty"`
	Modified   bool   `json:"modified"`
	GoVersion  string `json:"go_version"`
}

// Read never fails: a binary without build info still has to start.
func Read() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{Version: "unknown"}
	}
	return fromBuildInfo(bi)
}

func fromBuildInfo(bi *debug.BuildInfo) Info {
	info := Info{Version: bi.Main.Version, GoVersion: bi.GoVersion}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.time":
			info.CommitTime = s.Value
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}

func (i Info) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("version", i.Version),
		slog.String("commit", i.Commit),
		slog.Bool("modified", i.Modified),
		slog.String("go", i.GoVersion),
	)
}

// Handler serves GET /version. Public on purpose: it lets anyone confirm
// which build a deploy is running, and reveals nothing go.mod does not.
func Handler(info Info) http.Handler {
	body, _ := json.Marshal(info) // a struct of strings and a bool cannot fail
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(append(body, '\n'))
	})
}
