package workers

import "mash/pkg/di"

func Module(c *di.Container) {
	c.Register(New)
}
