//go:build unix

package tools

import "syscall"

const artifactNonblockFlag = syscall.O_NONBLOCK
