//go:build !enterprise && !cloud
// +build !enterprise,!cloud

package module

import (
	"getsturdy.com/api/pkg/di"
	"getsturdy.com/api/pkg/integrations/buildkite/graphql"
)

func Module(c *di.Container) {
	c.Import(graphql.Module)
}
