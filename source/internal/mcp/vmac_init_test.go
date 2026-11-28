package mcp

import (
	"logdir/internal/core"
)

func init() { core.VmacAutoOff.Store(true) }
