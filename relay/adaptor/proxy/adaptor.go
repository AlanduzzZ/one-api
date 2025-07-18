package proxy

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Laisky/errors/v2"
	"github.com/gin-gonic/gin"

	"github.com/songquanpeng/one-api/relay/adaptor"
	channelhelper "github.com/songquanpeng/one-api/relay/adaptor"
	"github.com/songquanpeng/one-api/relay/meta"
	"github.com/songquanpeng/one-api/relay/model"
	relaymodel "github.com/songquanpeng/one-api/relay/model"
)

var _ adaptor.Adaptor = new(Adaptor)

const channelName = "proxy"

type Adaptor struct {
	adaptor.DefaultPricingMethods
}

func (a *Adaptor) Init(meta *meta.Meta) {
}

func (a *Adaptor) ConvertRequest(c *gin.Context, relayMode int, request *model.GeneralOpenAIRequest) (any, error) {
	return nil, errors.New("notimplement")
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, meta *meta.Meta) (usage *model.Usage, err *model.ErrorWithStatusCode) {
	for k, v := range resp.Header {
		for _, vv := range v {
			c.Writer.Header().Set(k, vv)
		}
	}

	c.Writer.WriteHeader(resp.StatusCode)
	if _, gerr := io.Copy(c.Writer, resp.Body); gerr != nil {
		return nil, &relaymodel.ErrorWithStatusCode{
			StatusCode: http.StatusInternalServerError,
			Error: relaymodel.Error{
				Message: gerr.Error(),
			},
		}
	}

	// Return empty usage with zero tokens for proxy requests
	// This will allow proper logging in postConsumeQuota without charging anything
	return &model.Usage{
		PromptTokens:     0,
		CompletionTokens: 0,
		TotalTokens:      0,
		ToolsCost:        0,
	}, nil
}

func (a *Adaptor) GetModelList() (models []string) {
	return nil
}

func (a *Adaptor) GetChannelName() string {
	return channelName
}

// GetRequestURL remove static prefix, and return the real request url to the upstream service
func (a *Adaptor) GetRequestURL(meta *meta.Meta) (string, error) {
	prefix := fmt.Sprintf("/v1/oneapi/proxy/%d", meta.ChannelId)
	return meta.BaseURL + strings.TrimPrefix(meta.RequestURLPath, prefix), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Request, meta *meta.Meta) error {
	for k, v := range c.Request.Header {
		req.Header.Set(k, v[0])
	}

	// remove unnecessary headers
	req.Header.Del("Host")
	req.Header.Del("Content-Length")
	req.Header.Del("Accept-Encoding")
	req.Header.Del("Connection")

	// set authorization header
	req.Header.Set("Authorization", meta.APIKey)

	return nil
}

func (a *Adaptor) ConvertImageRequest(_ *gin.Context, request *model.ImageRequest) (any, error) {
	return nil, errors.Errorf("not implement")
}

func (a *Adaptor) DoRequest(c *gin.Context, meta *meta.Meta, requestBody io.Reader) (*http.Response, error) {
	return channelhelper.DoRequestHelper(a, c, meta, requestBody)
}
