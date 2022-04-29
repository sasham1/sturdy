package db

import (
	"context"
	"database/sql"

	"getsturdy.com/api/pkg/codebases"
	"getsturdy.com/api/pkg/snapshots"
	"getsturdy.com/api/pkg/users"
	"getsturdy.com/api/pkg/workspaces"
)

type memory struct {
	workspaces []*workspaces.Workspace
}

func NewMemory() Repository {
	return &memory{workspaces: make([]*workspaces.Workspace, 0)}
}

func (f *memory) Create(entity workspaces.Workspace) error {
	f.workspaces = append(f.workspaces, &entity)
	return nil
}
func (f *memory) Get(id string) (*workspaces.Workspace, error) {
	for _, ws := range f.workspaces {
		if ws.ID == id {
			return ws, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (f *memory) ListByCodebaseIDs(codebaseIDs []codebases.ID, includeArchived bool) ([]*workspaces.Workspace, error) {
	panic("not implemented")
}

func (f *memory) ListByCodebaseIDsAndUserID(codebaseIDs []codebases.ID, userID string) ([]*workspaces.Workspace, error) {
	panic("not implemented")
}

func (f *memory) UnsetUpToDateWithTrunkForAllInCodebase(codebaseID codebases.ID) error {
	for idx, ws := range f.workspaces {
		if ws.CodebaseID == codebaseID {
			f.workspaces[idx].UpToDateWithTrunk = nil
		}
	}
	return nil
}

func (f *memory) UpdateFields(_ context.Context, workspaceID string, fields ...UpdateOption) error {
	opts := Options(fields).Parse()
	for _, ws := range f.workspaces {
		if ws.ID != workspaceID {
			continue
		}
		if opts.updatedAtSet {
			ws.UpdatedAt = opts.updatedAt
		}
		if opts.upToDateWithTrunkSet {
			ws.UpToDateWithTrunk = opts.upToDateWithTrunk
		}
		if opts.headChangeIDSet {
			ws.HeadChangeID = opts.headChangeID
		}
		if opts.headChangeComputedSet {
			ws.HeadChangeComputed = opts.headChangeComputed
		}
		if opts.latestSnapshotIDSet {
			ws.LatestSnapshotID = opts.latestSnapshotID
		}
		if opts.diffsCountSet {
			ws.DiffsCount = opts.diffsCount
		}
		if opts.viewIDSet {
			ws.ViewID = opts.viewID
		}
		if opts.lastLandedAtSet {
			ws.LastLandedAt = opts.lastLandedAt
		}
		if opts.changeIDSet {
			ws.ChangeID = opts.changeID
		}
		if opts.draftDescriptionSet {
			ws.DraftDescription = opts.draftDescription
		}
		if opts.archivedAtSet {
			ws.ArchivedAt = opts.archivedAt
		}
		if opts.unarchivedAtSet {
			ws.UnarchivedAt = opts.unarchivedAt
		}
		if opts.nameSet {
			ws.Name = opts.name
		}
		if opts.userIDSet {
			ws.UserID = opts.userID
		}
		return nil
	}
	return sql.ErrNoRows
}

func (f *memory) GetByViewID(viewId string, includeArchived bool) (*workspaces.Workspace, error) {
	for _, ws := range f.workspaces {
		if ws.ViewID != nil && *ws.ViewID == viewId &&
			(includeArchived || ws.ArchivedAt == nil) {
			return ws, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (f *memory) GetBySnapshotID(id snapshots.ID) (*workspaces.Workspace, error) {
	panic("not implemented")
}

func (f *memory) ListByUserID(_ context.Context, userID users.ID) ([]*workspaces.Workspace, error) {
	ww := []*workspaces.Workspace{}
	for _, workspace := range f.workspaces {
		if workspace.UserID == userID {
			ww = append(ww, workspace)
		}
	}
	return ww, nil
}

func (f *memory) ListByIDs(_ context.Context, ids ...string) ([]*workspaces.Workspace, error) {
	ww := []*workspaces.Workspace{}
	for _, workspace := range f.workspaces {
		for _, id := range ids {
			if workspace.ID == id {
				ww = append(ww, workspace)
			}
		}
	}
	return ww, nil
}
