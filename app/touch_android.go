//go:build android && arm64 && cgo

package main

import "screenbridge/native"

type androidTouchInjector struct{}

func newTouchInjector() touchInjector { return &androidTouchInjector{} }

func (*androidTouchInjector) Down(width, height, x, y int) error {
	return native.TouchDown(width, height, x, y)
}

func (*androidTouchInjector) Move(x, y int) error { return native.TouchMove(x, y) }

func (*androidTouchInjector) Up(x, y int) error { return native.TouchUp(x, y) }

func (*androidTouchInjector) Cancel() error { return native.TouchCancel() }
