// Package webui contains the static files used by verdict's embedded UI.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS returns the complete embedded UI for static exporters.
func FS() fs.FS { return files }

// Asset reads one whitelisted UI asset.
func Asset(name string) ([]byte, error) {
	switch name {
	case "index.html", "app.js", "app.css":
	default:
		return nil, fs.ErrNotExist
	}
	body, err := files.ReadFile("static/" + name)
	if err != nil {
		return nil, fs.ErrNotExist
	}
	return body, nil
}

// Names lists the assets in their stable public order.
func Names() []string { return []string{"index.html", "app.js", "app.css"} }
