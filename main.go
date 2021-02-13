package main

import (
	"github.com/docker/machine/libmachine/drivers/plugin"
	vcd "github.com/juanfont/docker-machine-driver-vcd/driver"
)

func main() {
	plugin.RegisterDriver(vcd.NewDriver("", ""))
}
