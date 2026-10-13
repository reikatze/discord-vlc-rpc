package modules

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

type vlcIdentity struct {
	Version string `json:"version"`
	API     int    `json:"apiversion"`
	State   string `json:"state"`
}

func (s vlcIdentity) valid() bool { return s.Version != "" && s.API >= 1 }

// Setup probes and playback share transport, authentication and bounded decoding,
// while retaining their own response limits and user-facing error messages.
type vlcHTTPPolicy struct {
	limit                                         int64
	unavailable, unauthorized, status, body, json string
}

func localHTTPClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext},
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func vlcHTTP(ctx context.Context, client *http.Client, options map[string]string, endpoint string, target any) error {
	return vlcHTTPWithPolicy(ctx, client, options, endpoint, target, vlcHTTPPolicy{
		limit:        1024 * 1024,
		unavailable:  "VLC HTTP unavailable",
		unauthorized: "VLC HTTP password rejected; restart VLC",
		status:       "VLC HTTP status failed",
		body:         "VLC HTTP response exceeds limit",
		json:         "Invalid VLC HTTP JSON",
	})
}

func vlcHTTPWithPolicy(ctx context.Context, client *http.Client, options map[string]string, endpoint string, target any, policy vlcHTTPPolicy) error {
	host := "127.0.0.1"
	if options["http-host"] == "::1" {
		host = "::1"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, strconv.Itoa(httpPort(options)))+"/requests/"+endpoint, nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth("", options["http-password"])
	response, err := client.Do(request)
	if err != nil {
		return errors.New(policy.unavailable)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return errors.New(policy.unauthorized)
	}
	if response.StatusCode != http.StatusOK {
		return errors.New(policy.status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, policy.limit+1))
	if err != nil || int64(len(body)) > policy.limit {
		return errors.New(policy.body)
	}
	if json.Unmarshal(body, target) != nil {
		return errors.New(policy.json)
	}
	return nil
}
