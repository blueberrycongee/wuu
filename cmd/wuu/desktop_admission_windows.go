package main

import (
	"errors"
	"os"
)

func openDesktopAdmissionPipe(fd uintptr) (*os.File, error) {
	file := os.NewFile(fd, "desktop-parent-admission")
	if file == nil {
		return nil, errors.New("desktop parent admission pipe is unavailable")
	}
	return file, nil
}
