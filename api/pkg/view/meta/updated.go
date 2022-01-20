package meta

import (
	"context"
	"fmt"

	"mash/pkg/snapshots"
	worker_snapshotter "mash/pkg/snapshots/worker"
	"mash/pkg/view"
	"mash/pkg/view/events"
	db_workspace "mash/pkg/workspace/db"
	workspace_meta "mash/pkg/workspace/meta"
)

type ViewUpdatedFunc func(ctx context.Context, view *view.View, action snapshots.Action) error

// NewViewUpdatedFunc returns a function that sends events for updates of a views
func NewViewUpdatedFunc(
	workspaceReader db_workspace.WorkspaceReader,
	workspaceWriter db_workspace.WorkspaceWriter,
	eventsSender events.EventSender,
	snapshotterQueue worker_snapshotter.Queue,
) ViewUpdatedFunc {
	return func(ctx context.Context, view *view.View, action snapshots.Action) error {
		// Workspace has updated
		if err := workspace_meta.Updated(workspaceReader, workspaceWriter, view.WorkspaceID); err != nil {
			return fmt.Errorf("error updating workspace meta: %w", err)
		}

		// Add to snapshotter queue
		if err := snapshotterQueue.Enqueue(ctx, view.CodebaseID, view.ID, view.WorkspaceID, []string{"."}, action); err != nil {
			return fmt.Errorf("failed to enqueue snapshot: %w", err)
		}

		if err := eventsSender.Codebase(view.CodebaseID, events.ViewUpdated, view.ID); err != nil {
			return fmt.Errorf("failed to send view updated event: %w", err)
		}

		return nil
	}
}