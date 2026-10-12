package main

import (
	"bytes"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedWebAssets(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	for _, path := range []string{"/", "/assets/settings.js", "/favicon.ico", "/apple-touch-icon.png"} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("missing asset %s", path)
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing MIME protection")
		}
		if strings.Contains(response.Body.String(), "backdrop-filter") {
			t.Fatal("video-distorting backdrop blur must not return")
		}
		if path == "/" && (strings.Contains(response.Body.String(), "isInsideScreenshot") || strings.Contains(response.Body.String(), "手势已取消")) {
			t.Fatal("dragging beyond the preview must not cancel the gesture")
		}
		if path == "/" && !strings.Contains(response.Body.String(), `/assets/settings.js`) {
			t.Fatal("missing settings script")
		}
		if path == "/assets/settings.js" && !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") {
			t.Fatal("invalid JavaScript MIME type")
		}
		if path == "/" {
			for _, link := range []string{`rel="icon" href="/favicon.ico"`, `rel="apple-touch-icon" href="/apple-touch-icon.png"`} {
				if !strings.Contains(response.Body.String(), link) {
					t.Fatalf("missing icon link: %s", link)
				}
			}
		}
		if path == "/favicon.ico" {
			if response.Header().Get("Content-Type") != "image/x-icon" || !bytes.HasPrefix(response.Body.Bytes(), []byte{0, 0, 1, 0}) {
				t.Fatal("invalid favicon")
			}
		}
		if path == "/apple-touch-icon.png" {
			if response.Header().Get("Content-Type") != "image/png" {
				t.Fatal("invalid touch icon MIME type")
			}
			icon, err := png.Decode(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if icon.Bounds().Dx() != 180 || icon.Bounds().Dy() != 180 {
				t.Fatal("touch icon must be 180x180")
			}
			_, _, _, alpha := icon.At(0, 0).RGBA()
			if alpha != 0xffff {
				t.Fatal("touch icon must have an opaque background")
			}
		}
	}
	for _, path := range []string{"/assets/missing.js", "/assets/../main.go", "/web/index.html"} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("unexpected asset exposure: %s", path)
		}
	}
}
