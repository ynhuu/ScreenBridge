//go:build !android || !arm64 || !cgo

package main

import "errors"

func nativeCaptureWebP(quality, scale int) ([]byte, error) {
	return nil, errors.New("NDK SurfaceFlinger capture is available only in the Android arm64 build")
}

func nativeEncodeScreencap(raw []byte, quality int) ([]byte, int, int, error) {
	return nil, 0, 0, errors.New("NDK WebP encoder is available only in the Android arm64 build")
}
