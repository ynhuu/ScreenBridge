//go:build android && arm64 && cgo

package main

import "screenbridge/native"

func nativeCaptureWebP(quality, scale int) ([]byte, error) {
	return native.CaptureWebP(quality, scale)
}

func nativeEncodeScreencap(raw []byte, quality int) ([]byte, int, int, error) {
	return native.EncodeScreencap(raw, quality)
}
