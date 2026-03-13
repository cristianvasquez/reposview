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

type apiClient struct {
	base string
	http *http.Client
}

func newAPIClient(base string) *apiClient {
	return &apiClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *apiClient) health() error {
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
	var out rowsResponse
	err := c.getJSON("/rows?"+q.Encode(), &out)
	return out, err
}

func (c *apiClient) getStatus() (syncStatus, error) {
	var out syncStatus
	err := c.getJSON("/sync-status", &out)
	return out, err
}

func (c *apiClient) getRepoDetails(path string) (repoDetailsResponse, error) {
	var out repoDetailsResponse
	query := url.Values{}
	query.Set("path", path)
	err := c.getJSON("/repo-details?"+query.Encode(), &out)
	return out, err
}

func (c *apiClient) triggerSync() error {
	return c.postJSON("/sync", nil, nil)
}

func (c *apiClient) openTerminal(path string) error {
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
