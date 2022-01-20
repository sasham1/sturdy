//go:build !enterprise && !cloud
// +build !enterprise,!cloud

package module

import (
	"getsturdy.com/api/pkg/di"
	"getsturdy.com/api/pkg/license/oss/graphql"
)

func Module(c *di.Container) {
	c.Register(graphql.New)
}
