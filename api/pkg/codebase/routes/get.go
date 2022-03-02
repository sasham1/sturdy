package routes

import (
	"database/sql"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"getsturdy.com/api/pkg/auth"
	"getsturdy.com/api/pkg/codebase"
	"getsturdy.com/api/pkg/codebase/access"
	"getsturdy.com/api/pkg/codebase/db"
	service_user "getsturdy.com/api/pkg/users/service"

	"github.com/gin-gonic/gin"
)

func Get(
	repo db.CodebaseRepository,
	codebaseUserRepo db.CodebaseUserRepository,
	logger *zap.Logger,
	userService service_user.Service,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		userID, err := auth.UserID(c.Request.Context())
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		var cb *codebase.Codebase

		// If ID is a short ID
		if len(id) == 7 {
			cb, err = repo.GetByShortID(id)
		} else {
			cb, err = repo.Get(id)
		}

		if errors.Is(err, sql.ErrNoRows) {
			logger.Warn("codebase not found", zap.String("codebase_id", id), zap.Error(err))
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if err != nil {
			logger.Error("failed to get codebase", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unable to get codebase"})
			return
		}

		// Since this API allows lookup by the ShortID, this access check (which uses the UUID) is done after fetching the codebase
		if !access.UserHasAccessToCodebase(codebaseUserRepo, userID, cb.ID) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		memberAuthors, err := membersAsAuthors(c.Request.Context(), codebaseUserRepo, userService, cb.ID)
		if err != nil {
			logger.Error("failed to get members", zap.Error(err))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		c.JSON(http.StatusOK, codebase.CodebaseWithMetadata{
			Codebase: *cb,
			Members:  memberAuthors,
		})
	}
}
