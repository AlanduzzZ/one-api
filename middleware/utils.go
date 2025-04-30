package middleware

import (
	"strings"

	gmw "github.com/Laisky/gin-middlewares/v6"
	"github.com/Laisky/zap"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
)

func abortWithMessage(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": helper.MessageWithRequestId(message, c.GetString(helper.RequestIdKey)),
			"type":    "one_api_error",
		},
	})
	c.Abort()
	logger.Error(c.Request.Context(), message)
}

// AbortWithError aborts the request with an error message
func AbortWithError(c *gin.Context, statusCode int, err error) {
	logger := gmw.GetLogger(c)
	logger.Error("server abort", zap.Error(err))
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": helper.MessageWithRequestId(err.Error(), c.GetString(helper.RequestIdKey)),
			"type":    "one_api_error",
		},
	})
	c.Abort()
}

func getRequestModel(c *gin.Context) (string, error) {
	var modelRequest ModelRequest
	err := common.UnmarshalBodyReusable(c, &modelRequest)
	if err != nil {
		return "", errors.Wrap(err, "common.UnmarshalBodyReusable failed")
	}

	switch {
	case strings.HasPrefix(c.Request.URL.Path, "/v1/moderations"):
		if modelRequest.Model == "" {
			modelRequest.Model = "text-moderation-stable"
		}
	case strings.HasSuffix(c.Request.URL.Path, "embeddings"):
		if modelRequest.Model == "" {
			modelRequest.Model = c.Param("model")
		}
	case strings.HasPrefix(c.Request.URL.Path, "/v1/images/generations"),
		strings.HasPrefix(c.Request.URL.Path, "/v1/images/edits"):
		if modelRequest.Model == "" {
			modelRequest.Model = "dall-e-2"
		}
	case strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions"),
		strings.HasPrefix(c.Request.URL.Path, "/v1/audio/translations"):
		if modelRequest.Model == "" {
			modelRequest.Model = "whisper-1"
		}
	}

	return modelRequest.Model, nil
}

func isModelInList(modelName string, models string) bool {
	modelList := strings.Split(models, ",")
	for _, model := range modelList {
		if modelName == model {
			return true
		}
	}
	return false
}

func GetTokenKeyParts(c *gin.Context) []string {
	key := c.Request.Header.Get("Authorization")
	key = strings.TrimPrefix(key, "Bearer ")
	key = strings.TrimPrefix(strings.TrimPrefix(key, "sk-"), "laisky-")
	return strings.Split(key, "-")
}
