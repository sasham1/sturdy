package worker

import "getsturdy.com/api/pkg/di"

func Module(c *di.Container) {
	c.Register(New)
}
