package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

type osgEnvelope[T any] struct {
	Success bool   `json:"success"`
	Data    []T    `json:"data"`
	Error   string `json:"error"`
	Message string `json:"message"`
}

type osgInspectPayload struct {
	Cwd        string              `json:"cwd"`
	Repository *osgRepositoryState `json:"repository"`
	Error      string              `json:"error"`
}

type osgRepositoryPayload struct {
	Repository osgRepositoryState `json:"repository"`
}

type osgConfigPayload struct {
	Repositories []osgConfiguredRepository `json:"repositories"`
}

type osgConfiguredRepository struct {
	Identity string `json:"identity"`
	Path     string `json:"path"`
	URI      string `json:"uri"`
}

type osgRepositoryState struct {
	Identity   string               `json:"identity"`
	Path       string               `json:"path"`
	Connection osgConnectionPayload `json:"connection"`
}

type osgConnectionPayload struct {
	Connected bool `json:"connected"`
}

type apiClient struct {
	base  string
	http  *http.Client
	local *localBackend
}

func newAPIClient(base string) *apiClient {
	return &apiClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *apiClient) health() error {
	if c.local != nil {
		return nil
	}
	resp, err := c.http.Get(c.base + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("healthz returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *apiClient) getRows(q url.Values) (rowsResponse, error) {
	if c.local != nil {
		return c.local.getRows(q)
	}
	var out rowsResponse
	err := c.getJSON("/rows?"+q.Encode(), &out)
	return out, err
}

func (c *apiClient) getStatus() (syncStatus, error) {
	if c.local != nil {
		return c.local.getStatus()
	}
	var out syncStatus
	err := c.getJSON("/sync-status", &out)
	return out, err
}

func (c *apiClient) getRepoDetails(path string) (repoDetailsResponse, error) {
	if c.local != nil {
		return c.local.getRepoDetails(path)
	}
	var out repoDetailsResponse
	query := url.Values{}
	query.Set("path", path)
	err := c.getJSON("/repo-details?"+query.Encode(), &out)
	return out, err
}

func (c *apiClient) triggerSync() error {
	if c.local != nil {
		return c.local.triggerSync()
	}
	return c.postJSON("/sync", nil, nil)
}

func (c *apiClient) openTerminal(path string) error {
	if c.local != nil {
		return c.local.openTerminal(path)
	}
	body := map[string]string{"path": path}
	var out actionResponse
	if err := c.postJSON("/actions/open-terminal", body, &out); err != nil {
		return err
	}
	if !out.Opened && out.Error != "" {
		return errors.New(out.Error)
	}
	return nil
}

func (c *apiClient) openYazi(path string) error {
	if c.local != nil {
		return c.local.openYazi(path)
	}
	body := map[string]string{"path": path}
	var out actionResponse
	if err := c.postJSON("/actions/open-yazi", body, &out); err != nil {
		return err
	}
	if !out.Opened && out.Error != "" {
		return errors.New(out.Error)
	}
	return nil
}

func (c *apiClient) openBrowser(rawURL string) error {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return errors.New("browser target is empty")
	}

	candidates := []string{"xdg-open", "open"}
	for _, name := range candidates {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, target)
		if err := cmd.Start(); err != nil {
			return err
		}
		return nil
	}

	return errors.New("no supported browser opener found")
}

func (c *apiClient) inspectConnection(path string) (connectionStatus, error) {
	var payload osgEnvelope[osgInspectPayload]
	if err := runOSGJSON(path, &payload, "inspect", "--format", "json"); err != nil {
		return connectionStatus{}, err
	}
	if len(payload.Data) == 0 {
		return connectionStatus{}, errors.New("osg inspect returned no data")
	}

	data := payload.Data[0]
	status := connectionStatus{Path: path, Error: data.Error}
	if data.Repository != nil {
		status.Path = nonEmptyOrFallback(data.Repository.Path, path)
		status.Identity = data.Repository.Identity
		status.Connected = data.Repository.Connection.Connected
		status.Known = true
	}
	return status, nil
}

func (c *apiClient) connectRepository(path string) (connectionStatus, string, error) {
	var payload osgEnvelope[osgRepositoryPayload]
	if err := runOSGJSON(path, &payload, "connect", path, "--format", "json"); err != nil {
		return connectionStatus{}, "", err
	}
	return connectionStatusFromRepositoryPayload(path, payload)
}

func (c *apiClient) disconnectRepository(path string) (connectionStatus, string, error) {
	var payload osgEnvelope[osgRepositoryPayload]
	if err := runOSGJSON(path, &payload, "disconnect", path, "--format", "json"); err != nil {
		return connectionStatus{}, "", err
	}
	return connectionStatusFromRepositoryPayload(path, payload)
}

func (c *apiClient) listConfiguredRepositories() ([]osgConfiguredRepository, error) {
	var payload osgEnvelope[osgConfigPayload]
	if err := runOSGJSON(".", &payload, "config", "list", "--format", "json"); err != nil {
		return nil, err
	}
	repos := make([]osgConfiguredRepository, 0)
	for _, item := range payload.Data {
		repos = append(repos, item.Repositories...)
	}
	return repos, nil
}

func connectionStatusFromRepositoryPayload(path string, payload osgEnvelope[osgRepositoryPayload]) (connectionStatus, string, error) {
	if len(payload.Data) == 0 {
		return connectionStatus{}, "", errors.New("osg command returned no data")
	}

	repository := payload.Data[0].Repository
	return connectionStatus{
		Path:      nonEmptyOrFallback(repository.Path, path),
		Identity:  repository.Identity,
		Connected: repository.Connection.Connected,
		Known:     true,
	}, payload.Message, nil
}

func runOSGJSON(dir string, out any, args ...string) error {
	osgPath, err := exec.LookPath("osg")
	if err != nil {
		return errors.New("osg executable not found")
	}

	cmd := exec.Command(osgPath, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if err := decodeOSGJSON(stdout.Bytes(), out); err == nil {
		if runErr != nil {
			if msg := extractOSGError(out); msg != "" {
				return errors.New(msg)
			}
		}
		return nil
	}

	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return errors.New(msg)
	}

	return decodeOSGJSON(stdout.Bytes(), out)
}

func decodeOSGJSON(data []byte, out any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return errors.New("empty osg response")
	}
	return json.Unmarshal(trimmed, out)
}

func extractOSGError(payload any) string {
	switch value := payload.(type) {
	case *osgEnvelope[osgInspectPayload]:
		return value.Error
	case *osgEnvelope[osgRepositoryPayload]:
		return value.Error
	default:
		return ""
	}
}

func nonEmptyOrFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (c *apiClient) getJSON(path string, out any) error {
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func (c *apiClient) postJSON(path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		buf := bytes.NewBuffer(nil)
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return err
		}
		reader = buf
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload map[string]any
		if json.Unmarshal(body, &payload) == nil {
			if msg, ok := payload["error"].(string); ok && msg != "" {
				return errors.New(msg)
			}
		}
		return fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}
