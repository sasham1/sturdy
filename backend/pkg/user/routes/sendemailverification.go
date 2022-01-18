package routes

import (
	"mash/pkg/auth"
	service_user "mash/pkg/user/service"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func SendEmailVerification(
	logger *zap.Logger,
	userService *service_user.Service,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := auth.UserID(c.Request.Context())
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if err := userService.SendEmailVerification(c.Request.Context(), userID); err != nil {
			logger.Error("failed to send email verification", zap.Error(err))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}
}