package scan

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"
)

// ProviderHealth reports readiness without sending credentials or inference requests.
type ProviderHealth struct {
	ModelMode        string `json:"model_mode"`
	APIKeyEnvPresent bool   `json:"api_key_env_present"`
	Attempted        bool   `json:"attempted"`
	Status           string `json:"status"`
}

var errRemoteDoctor = errors.New("doctor endpoint is not exclusively loopback")

const (
	doctorUnsafeEndpoint = "refused_unsafe_endpoint"
	doctorUnreachable    = "unreachable"
)

// CheckProvider only probes the models resource on an exclusively loopback host.
func CheckProvider(ctx context.Context, model, baseURL, keyEnv string) (ProviderHealth, error) {
	result := ProviderHealth{ModelMode: "deterministic", APIKeyEnvPresent: keyEnv != "" && os.Getenv(keyEnv) != "", Status: "not_configured"}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if model == "" {
		return result, nil
	}
	result.ModelMode = "configured-model"
	endpoint, err := url.Parse(baseURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil {
		result.Status = doctorUnsafeEndpoint
		return result, nil
	}
	endpoint.Path = path.Join(endpoint.Path, "models")
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return result, errors.New("build provider readiness request")
	}
	transport := &http.Transport{DialContext: doctorLoopbackDial}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if errors.Is(err, errRemoteDoctor) {
			result.Status = "refused_non_loopback"
			return result, nil
		}
		result.Attempted = true
		result.Status = doctorUnreachable
		return result, nil
	}
	result.Attempted = true
	result.Status = providerResponseStatus(response)
	return result, nil
}

func providerResponseStatus(response *http.Response) string {
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	closeErr := response.Body.Close()
	switch {
	case readErr != nil || closeErr != nil:
		return doctorUnreachable
	case len(body) > 1<<20:
		return "response_too_large"
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return doctorUnreachable
	case !json.Valid(body):
		return "invalid_response"
	default:
		return "reachable"
	}
}

func doctorLoopbackDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, errRemoteDoctor
	}
	for _, ip := range addresses {
		if !ip.IP.IsLoopback() {
			return nil, errRemoteDoctor
		}
	}
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
}
