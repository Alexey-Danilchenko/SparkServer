// Package monitorcli contains the HTTP client used by sparkctl commands.
package monitorcli

import (
	"bufio"
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

// Client wraps Spark Server HTTP routes with typed results for CLI rendering.
type Client struct {
	baseURL    string
	token      string
	username   string
	password   string
	httpClient *http.Client
}

// Device is the CLI projection of a server device response.
type Device struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Connected bool              `json:"connected"`
	Online    bool              `json:"online"`
	ProductID string            `json:"product_id"`
	Variables map[string]string `json:"variables"`
	Functions []string          `json:"functions"`
	LastHeard string            `json:"last_heard,omitempty"`
}

// VariableResult is returned after reading a live device variable.
type VariableResult struct {
	Command string `json:"cmd"`
	Name    string `json:"name"`
	Result  string `json:"result"`
}

// FunctionResult is returned after invoking a live device function.
type FunctionResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Connected   bool   `json:"connected"`
	ReturnValue int    `json:"return_value"`
}

// PingResult summarizes live ping/online state for a device.
type PingResult struct {
	ID        string `json:"id"`
	Connected bool   `json:"connected"`
	Online    bool   `json:"online"`
}

// Event is the SSE projection consumed by sparkctl events.
type Event struct {
	Name        string `json:"name"`
	Data        string `json:"data"`
	CoreID      string `json:"coreid"`
	PublishedAt string `json:"published_at"`
}

// NewClient validates the base URL and prepares authentication for later requests.
func NewClient(
	baseURL string,
	token string,
	username string,
	password string,
	httpClient *http.Client,
) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid base URL %q", baseURL)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, username: username, password: password, httpClient: httpClient}, nil
}

// Login performs the password-grant request and stores the returned bearer token.
func (client *Client) Login(ctx context.Context) (string, error) {
	if client.username == "" || client.password == "" {
		return "", errors.New("username and password are required")
	}
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("username", client.username)
	form.Set("password", client.password)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var body struct {
		AccessToken string `json:"access_token"`
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", responseError(response)
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", errors.New("server returned an empty access token")
	}
	client.token = body.AccessToken
	return body.AccessToken, nil
}

func (client *Client) ListDevices(ctx context.Context) ([]Device, error) {
	var devices []Device
	return devices, client.getJSON(ctx, "/v1/devices", &devices)
}

func (client *Client) GetDevice(ctx context.Context, deviceID string) (*Device, error) {
	var device Device
	return &device, client.getJSON(ctx, "/v1/devices/"+url.PathEscape(deviceID), &device)
}

func (client *Client) Ping(ctx context.Context, deviceID string) (*PingResult, error) {
	request, err := client.newRequest(ctx, http.MethodPut, "/v1/devices/"+url.PathEscape(deviceID)+"/ping", nil)
	if err != nil {
		return nil, err
	}
	var result PingResult
	return &result, client.doJSON(request, &result)
}

func (client *Client) GetVariable(
	ctx context.Context,
	deviceID string,
	variableName string,
) (*VariableResult, error) {
	var result VariableResult
	path := "/v1/devices/" + url.PathEscape(deviceID) + "/" + url.PathEscape(variableName)
	return &result, client.getJSON(ctx, path, &result)
}

func (client *Client) CallFunction(
	ctx context.Context,
	deviceID string,
	functionName string,
	argument string,
) (*FunctionResult, error) {
	form := url.Values{}
	form.Set("arg", argument)
	path := "/v1/devices/" + url.PathEscape(deviceID) + "/" + url.PathEscape(functionName)
	request, err := client.newRequest(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var result FunctionResult
	return &result, client.doJSON(request, &result)
}

func (client *Client) StreamEvents(
	ctx context.Context,
	deviceID string,
	prefix string,
	handle func(Event) error,
) error {
	if err := client.ensureToken(ctx); err != nil {
		return err
	}
	path := "/v1/events"
	if deviceID != "" {
		path = "/v1/devices/" + url.PathEscape(deviceID) + "/events"
	}
	if prefix != "" {
		path += "/" + strings.TrimLeft(prefix, "/")
	}
	request, err := client.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return responseError(response)
	}
	return readSSE(ctx, response.Body, handle)
}

func (client *Client) getJSON(ctx context.Context, path string, target any) error {
	request, err := client.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return client.doJSON(request, target)
}

func (client *Client) doJSON(request *http.Request, target any) error {
	if err := client.ensureToken(request.Context()); err != nil {
		return err
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return responseError(response)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (client *Client) newRequest(
	ctx context.Context,
	method string,
	path string,
	body io.Reader,
) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	return request, nil
}

func (client *Client) ensureToken(ctx context.Context) error {
	if client.token != "" {
		return nil
	}
	if client.username == "" && client.password == "" {
		return errors.New("set SPARK_TOKEN or pass -token; alternatively pass -username and -password")
	}
	_, err := client.Login(ctx)
	return err
}

func responseError(response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return fmt.Errorf("server returned %s", response.Status)
	}
	return fmt.Errorf("server returned %s: %s", response.Status, string(payload))
}

func readSSE(ctx context.Context, reader io.Reader, handle func(Event) error) error {
	scanner := bufio.NewScanner(reader)
	var data strings.Builder
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			var event Event
			if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
				return err
			}
			if err := handle(event); err != nil {
				return err
			}
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}
