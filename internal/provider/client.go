package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultResponseLimit = 8 << 20
	providerOpenAI       = "openai"
	providerAnthropic    = "anthropic"
	providerGemini       = "gemini"
)

// Client makes bounded completion requests using one provider wire protocol.
// A configured BaseURL always selects the OpenAI-compatible protocol.
type Client struct {
	endpoint         string
	provider         string
	model            string
	authorization    string
	apiKey           string
	timeout          time.Duration
	limit            int64
	maxRetries       int
	maxBackoff       time.Duration
	rateLimitBackoff time.Duration
	http             *http.Client
}

// HTTPError exposes status without leaking response bodies or credentials.
type HTTPError struct {
	StatusCode int
	RetryAfter string
	Detail     string
}

func (e *HTTPError) Error() string {
	message := fmt.Sprintf("provider returned HTTP %d", e.StatusCode)
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

// New validates the selected provider and clones HTTP policy to forbid redirects.
// A custom base URL stays OpenAI-compatible, including for named providers.
func New(cfg Config) (*Client, error) {
	provider, endpoint, err := providerEndpoint(cfg.Provider, cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{}
	if cfg.HTTPClient != nil {
		*client = *cfg.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = defaultResponseLimit
	}
	if cfg.MaxResponseBytes > 1<<30 {
		return nil, errors.New("provider response limit exceeds 1 GiB")
	}
	if cfg.ProviderMaxRetries < 0 {
		return nil, errors.New("provider retries must not be negative")
	}
	auth := cfg.AuthHeader
	if auth == "" && cfg.APIKey != "" && provider == providerOpenAI {
		auth = "Bearer " + cfg.APIKey
	}
	if strings.ContainsAny(auth, "\r\n") {
		return nil, errors.New("provider authorization contains a line break")
	}
	return &Client{endpoint: endpoint, provider: provider, model: cfg.Model, authorization: auth, apiKey: cfg.APIKey,
		timeout: cfg.Timeout, limit: cfg.MaxResponseBytes, maxRetries: cfg.ProviderMaxRetries,
		maxBackoff: cfg.MaxProviderBackoff, rateLimitBackoff: cfg.RateLimitBackoff, http: client}, nil
}

func providerEndpoint(name, base string) (provider, endpoint string, err error) {
	if base != "" {
		endpoint, err := completionEndpoint(base)
		return providerOpenAI, endpoint, err
	}
	switch strings.ToLower(name) {
	case providerOpenAI:
		return providerOpenAI, "https://api.openai.com/v1/chat/completions", nil
	case providerAnthropic:
		return providerAnthropic, "https://api.anthropic.com/v1/messages", nil
	case providerGemini:
		return providerGemini, "https://generativelanguage.googleapis.com/v1beta", nil
	default:
		return "", "", errors.New("provider requires a supported name or an OpenAI-compatible base URL")
	}
}

func completionEndpoint(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u == nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("provider requires an absolute HTTP(S) base URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("provider base URL cannot contain credentials, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/chat/completions") {
		u.Path += "/chat/completions"
	}
	u.RawPath = ""
	return u.String(), nil
}

// Complete transmits one request. Retries are opt-in and only follow a known
// provider HTTP rejection; transport, read, and decode failures remain unknown.
func (c *Client) Complete(ctx context.Context, request Request) (Response, error) {
	if request.Model == "" {
		request.Model = c.model
	}
	if request.Model == "" || len(request.Messages) == 0 {
		return Response{}, errors.New("provider request requires a model and messages")
	}
	for attempt := 0; ; attempt++ {
		response, err := c.completeOnce(ctx, request)
		if err == nil || attempt >= c.maxRetries || !retryableHTTPError(err) {
			return response, err
		}
		delayErr := waitRetry(ctx, retryDelay(err, attempt, c.maxBackoff, c.rateLimitBackoff))
		if delayErr != nil {
			return Response{}, delayErr
		}
	}
}

func (c *Client) completeOnce(ctx context.Context, request Request) (Response, error) {
	body, endpoint, err := c.encode(request)
	if err != nil {
		return Response{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, errors.New("construct provider request")
	}
	c.setHeaders(req)
	res, err := c.http.Do(req)
	if err != nil {
		if requestCtx.Err() != nil {
			return Response{}, fmt.Errorf("provider request: %w", requestCtx.Err())
		}
		return Response{}, errors.New("provider transport failed; submission outcome unknown")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return Response{}, c.httpError(res)
	}
	return c.decode(res.Body, request.Model)
}

func (c *Client) httpError(res *http.Response) *HTTPError {
	result := &HTTPError{StatusCode: res.StatusCode, RetryAfter: res.Header.Get("Retry-After")}
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<10))
	if err != nil {
		return result
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return result
	}
	detail := strings.TrimSpace(strings.Join([]string{payload.Error.Status, payload.Error.Message}, ": "))
	detail = strings.Trim(detail, ": ")
	detail = strings.Join(strings.Fields(detail), " ")
	for _, secret := range []string{c.apiKey, c.authorization} {
		if secret != "" {
			detail = strings.ReplaceAll(detail, secret, "[redacted]")
		}
	}
	if len(detail) > 512 {
		detail = detail[:512] + "..."
	}
	result.Detail = detail
	return result
}

func (c *Client) encode(request Request) ([]byte, string, error) {
	switch c.provider {
	case providerAnthropic:
		body, err := encodeAnthropic(request)
		return body, c.endpoint, err
	case providerGemini:
		return encodeGemini(request, c.endpoint)
	default:
		body, err := encodeOpenAI(request)
		return body, c.endpoint, err
	}
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.provider == providerAnthropic {
		req.Header.Set("Anthropic-Version", "2023-06-01")
		if c.apiKey != "" {
			req.Header.Set("X-API-Key", c.apiKey)
		}
	}
	if c.provider == providerGemini && c.apiKey != "" {
		query := req.URL.Query()
		query.Set("key", c.apiKey)
		req.URL.RawQuery = query.Encode()
	}
	if c.authorization != "" {
		req.Header.Set("Authorization", c.authorization)
	}
}

func (c *Client) decode(body io.Reader, model string) (Response, error) {
	data, err := readResponse(body, c.limit)
	if err != nil {
		return Response{}, err
	}
	switch c.provider {
	case providerAnthropic:
		return decodeAnthropic(data)
	case providerGemini:
		return decodeGemini(data, model)
	default:
		return decodeOpenAI(data)
	}
}

func readResponse(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, errors.New("read provider response; submission outcome unknown")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("provider response exceeds configured byte limit")
	}
	return data, nil
}

func decodeOpenAI(data []byte) (Response, error) {
	var response Response
	if err := json.Unmarshal(data, &response); err != nil {
		return Response{}, errors.New("provider returned invalid completion JSON")
	}
	if len(response.Choices) == 0 {
		return Response{}, errors.New("provider returned no completion choices")
	}
	return response, nil
}
