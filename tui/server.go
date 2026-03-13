package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type spawnedServer struct {
	cmd    *exec.Cmd
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (s *spawnedServer) Stop() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}

	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = s.cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
}

func ensureAPI(client *apiClient, spawn bool, dbPath string, scanner string) (*spawnedServer, error) {
	if err := client.health(); err == nil {
		return nil, nil
	}

	if !spawn {
		return nil, fmt.Errorf("inspect API is unavailable at %s", client.base)
	}

	server, err := startInspectServer(client.base, dbPath, scanner)
	if err != nil {
		return nil, err
	}

	if err := waitForHealth(client, 8*time.Second); err != nil {
		logs := strings.TrimSpace(server.stderr.String())
		if logs == "" {
			logs = strings.TrimSpace(server.stdout.String())
		}
		server.Stop()
		if logs != "" {
			return nil, fmt.Errorf("inspect API failed to start: %w\n%s", err, logs)
		}
		return nil, fmt.Errorf("inspect API failed to start: %w", err)
	}

	return server, nil
}

func startInspectServer(apiOrigin string, dbPath string, scanner string) (*spawnedServer, error) {
	parsed, err := url.Parse(apiOrigin)
	if err != nil {
		return nil, fmt.Errorf("invalid api origin %q: %w", apiOrigin, err)
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" || port == "" {
		return nil, fmt.Errorf("api origin must include host and port: %s", apiOrigin)
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	repoRoot := filepath.Dir(wd)
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return nil, errors.New("node executable not found; cannot auto-start inspect API")
	}

	if dbPath == "" {
		dbPath = filepath.Join(repoRoot, "data", "reposview.sqlite")
	}
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(repoRoot, dbPath)
	}

	scriptPath := filepath.Join(repoRoot, "scripts", "inspect-table.mjs")
	args := []string{
		scriptPath,
		"--db", dbPath,
		"--host", host,
		"--port", port,
		"--scanner", scanner,
	}

	cmd := exec.Command(nodePath, args...)
	cmd.Dir = repoRoot
	stdout := bytes.NewBuffer(nil)
	stderr := bytes.NewBuffer(nil)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start inspect API: %w", err)
	}

	return &spawnedServer{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

func waitForHealth(client *apiClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.health(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("timeout waiting for healthz")
	}
	return lastErr
}
