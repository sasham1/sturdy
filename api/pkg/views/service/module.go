package service

import (
	"getsturdy.com/api/pkg/di"
	"getsturdy.com/api/pkg/events"
	"getsturdy.com/api/pkg/logger"
	db_snapshots "getsturdy.com/api/pkg/snapshots/db"
	service_snapshots "getsturdy.com/api/pkg/snapshots/service"
	db_view "getsturdy.com/api/pkg/views/db"
	db_workspaces "getsturdy.com/api/pkg/workspaces/db"
	meta_workspaces "getsturdy.com/api/pkg/workspaces/meta"
)

func Module(c *di.Container) {
	c.Import(logger.Module)
	c.Import(db_view.Module)
	c.Import(db_workspaces.Module)
	c.Import(service_snapshots.Module)
	c.Import(db_snapshots.Module)
	c.Import(meta_workspaces.Module)
	c.Import(events.Module)
	c.Register(New)
}
