package main

import (
	"embed"
	"net/http"
)

// All resources stay in the single executable; no runtime web directory is needed.
//
//go:embed web/index.html web/settings.js web/favicon.ico web/apple-touch-icon.png
var webFiles embed.FS

type webAsset struct {
	data        []byte
	contentType string
}

var webAssets = map[string]webAsset{
	"/":                     loadWebAsset("web/index.html", "text/html; charset=utf-8"),
	"/assets/settings.js":   loadWebAsset("web/settings.js", "text/javascript; charset=utf-8"),
	"/favicon.ico":          loadWebAsset("web/favicon.ico", "image/x-icon"),
	"/apple-touch-icon.png": loadWebAsset("web/apple-touch-icon.png", "image/png"),
}

func loadWebAsset(path, contentType string) webAsset {
	data, err := webFiles.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return webAsset{data: data, contentType: contentType}
}

func serveWebAsset(w http.ResponseWriter, path string) {
	asset, ok := webAssets[path]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "asset not found"})
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeBytes(w, http.StatusOK, asset.contentType, asset.data)
}
