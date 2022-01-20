package graphql

import (
	"context"
	gqlerrors "mash/pkg/graphql/errors"
	"mash/pkg/graphql/resolvers"
	"mash/pkg/user"
	db_user "mash/pkg/user/db"

	"github.com/graph-gophers/graphql-go"
	"go.uber.org/zap"
)

type AuthorRootResolver struct {
	userRepo db_user.Repository
}

func NewResolver(userRepo db_user.Repository, logger *zap.Logger) resolvers.AuthorRootResolver {
	userRoot := &AuthorRootResolver{
		userRepo: userRepo,
	}
	return NewDataloader(userRoot, logger)
}

func (r *AuthorRootResolver) Author(id string) (resolvers.AuthorResolver, error) {
	uu, err := r.userRepo.Get(id)
	if err != nil {
		return nil, gqlerrors.Error(err)
	}
	return &AuthorResolver{user: uu}, nil
}

func (r *AuthorRootResolver) InternalAuthorFromNameAndEmail(_ context.Context, name, email string) resolvers.AuthorResolver {
	return &authorNameEmailResolver{name, email}
}

type AuthorResolver struct {
	user *user.User
}

func (r *AuthorResolver) ID() graphql.ID {
	return graphql.ID(r.user.ID)
}

func (r *AuthorResolver) Name() string {
	return r.user.Name
}

func (r *AuthorResolver) AvatarUrl() *string {
	return r.user.AvatarURL
}

func (r *AuthorResolver) Email() string {
	return r.user.Email
}