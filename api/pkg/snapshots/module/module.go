package module

import (
	"getsturdy.com/api/pkg/di"
	"getsturdy.com/api/pkg/snapshots/db"
	"getsturdy.com/api/pkg/snapshots/snapshotter"
	"getsturdy.com/api/pkg/snapshots/worker"
)

func Module(c *di.Container) {
	c.Import(db.Module)
	c.Import(snapshotter.Module)
	c.Import(worker.Module)
}
