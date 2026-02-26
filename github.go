package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/cli/go-gh/v2/pkg/repository"
)

// ─── Debug logging ────────────────────────────────────────────────────────────

var debugLogger *log.Logger

func initDebugLog(filename string) {
	if filename == "" {
		return
	}
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return
	}
	debugLogger = log.New(f, "", log.Ltime|log.Lmicroseconds)
	debugLogger.Println("=== tgh debug log started ===")
}

func dbg(format string, args ...interface{}) {
	if debugLogger != nil {
		debugLogger.Printf(format, args...)
	}
}

// WorkflowRun represents a single GitHub Actions workflow run.
type WorkflowRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	HeadBranch string    `json:"head_branch"`
	Event      string    `json:"event"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	HTMLURL    string    `json:"html_url"`
}

// Job represents a single job within a workflow run.
type Job struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Steps       []Step    `json:"steps"`
	HTMLURL     string    `json:"html_url"`
}

// Step represents a single step within a job.
type Step struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	Number      int       `json:"number"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// GitHubClient wraps the go-gh REST client with repo context.
type GitHubClient struct {
	rest  *api.RESTClient
	host  string
	owner string
	repo  string
}

// liveHTTPClient is used for requests to GitHub web endpoints.
// We deliberately do NOT follow redirects: a redirect means the endpoint is
// requiring browser-session auth (login page), which we should treat as failure.
var liveHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Timeout: 15 * time.Second,
}

// changeToRepoDir changes the current working directory to the specified repo path.
func changeToRepoDir(repoPath string) error {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}
	return os.Chdir(absPath)
}

// NewGitHubClient creates a client scoped to the current git repository's GitHub remote.
// Works with both github.com and GitHub Enterprise.
// If repoPath is non-empty, uses that directory instead of the current directory.
func NewGitHubClient(repoPath ...string) (*GitHubClient, error) {
	// Change to repo directory if provided
	if len(repoPath) > 0 && repoPath[0] != "" {
		if err := changeToRepoDir(repoPath[0]); err != nil {
			return nil, fmt.Errorf("could not change to repository directory: %w", err)
		}
	}

	repo, err := repository.Current()
	if err != nil {
		return nil, fmt.Errorf("could not detect GitHub repository: %w\nRun tgh inside a directory with a GitHub remote", err)
	}

	client, err := api.NewRESTClient(api.ClientOptions{Host: repo.Host})
	if err != nil {
		return nil, fmt.Errorf("could not create GitHub client: %w", err)
	}

	return &GitHubClient{
		rest:  client,
		host:  repo.Host,
		owner: repo.Owner,
		repo:  repo.Name,
	}, nil
}

// ListRuns fetches the 30 most recent workflow runs.
func (c *GitHubClient) ListRuns() ([]WorkflowRun, error) {
	var result struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	err := c.rest.Get(
		fmt.Sprintf("repos/%s/%s/actions/runs?per_page=30", c.owner, c.repo),
		&result,
	)
	return result.WorkflowRuns, err
}

// ListJobs fetches jobs for a given workflow run.
func (c *GitHubClient) ListJobs(runID int64) ([]Job, error) {
	var result struct {
		Jobs []Job `json:"jobs"`
	}
	err := c.rest.Get(
		fmt.Sprintf("repos/%s/%s/actions/runs/%d/jobs?per_page=100", c.owner, c.repo, runID),
		&result,
	)
	return result.Jobs, err
}

// GetJobLogs downloads and parses logs for a given job.
// Handles both plain-text and zip-encoded responses; strips timestamps.
// Returns empty string with no error if job is still running (logs not yet available).
func (c *GitHubClient) GetJobLogs(jobID int64) (string, error) {
	path := fmt.Sprintf("repos/%s/%s/actions/jobs/%d/logs", c.owner, c.repo, jobID)
	dbg("GetJobLogs: GET %s", path)
	resp, err := c.rest.Request("GET", path, nil)
	if err != nil {
		dbg("GetJobLogs: error: %v", err)
		return "", err
	}
	defer resp.Body.Close()

	dbg("GetJobLogs: status=%d finalURL=%s", resp.StatusCode, resp.Request.URL)

	// 404 means logs not yet available (job still running)
	if resp.StatusCode == 404 {
		return "", nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Check for zip magic bytes "PK"
	if len(data) >= 2 && data[0] == 'P' && data[1] == 'K' {
		return parseZipLog(data)
	}

	return processLogLines(string(data)), nil
}

// GetLiveJobLogs streams live log content using GitHub's undocumented web endpoint:
//
//	GET https://github.com/{owner}/{repo}/actions/runs/{runID}/job/{jobID}/steps?change_id={n}
//
// changeID=0 fetches all content from the start; subsequent calls should pass the
// returned nextChangeID to receive only new lines. This endpoint is not publicly
// supported and may break at any time.
//
// Returns ("", changeID, false, nil) when the endpoint is not reachable (non-200).
// Returns ("", changeID, true, nil) when reachable but no new content yet.
func (c *GitHubClient) GetLiveJobLogs(jobHTMLURL string, changeID int) (lines string, nextChangeID int, endpointOK bool, err error) {
	token, _ := auth.TokenForHost(c.host)
	if token == "" {
		dbg("GetLiveJobLogs: no token for host %s", c.host)
		return "", changeID, false, nil
	}

	reqURL := fmt.Sprintf("%s/steps?change_id=%d", jobHTMLURL, changeID)
	dbg("GetLiveJobLogs: GET %s", reqURL)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", changeID, false, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", "tgh")

	resp, err := liveHTTPClient.Do(req)
	if err != nil {
		dbg("GetLiveJobLogs: request error: %v", err)
		return "", changeID, false, err
	}
	defer resp.Body.Close()

	dbg("GetLiveJobLogs: status=%d finalURL=%s", resp.StatusCode, resp.Request.URL)

	if resp.StatusCode != http.StatusOK {
		return "", changeID, false, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", changeID, true, err
	}

	preview := string(body)
	if len(preview) > 500 {
		preview = preview[:500] + "...[truncated]"
	}
	dbg("GetLiveJobLogs: body (%d bytes): %s", len(body), preview)

	content, next := parseLiveLogResponse(body, changeID)
	dbg("GetLiveJobLogs: parsed lines=%d bytes, nextChangeID=%d", len(content), next)
	return content, next, true, nil
}

// parseLiveLogResponse extracts log text and the next change_id from a live log response.
// It tries JSON first (with several possible schemas), then falls back to plain text.
func parseLiveLogResponse(body []byte, currentChangeID int) (string, int) {
	var raw map[string]interface{}
	if json.Unmarshal(body, &raw) == nil {
		nextID := liveExtractChangeID(raw, currentChangeID)
		logLines := liveExtractLines(raw)
		if len(logLines) > 0 {
			return processLogLines(strings.Join(logLines, "\n")), nextID
		}
		// changeID advanced but no parseable lines — still record progress
		return "", nextID
	}

	// Plain text fallback: accept if it doesn't look like HTML
	content := strings.TrimSpace(string(body))
	if len(content) > 0 && !strings.HasPrefix(content, "<") {
		return processLogLines(content), currentChangeID + 1
	}
	return "", currentChangeID
}

func liveExtractChangeID(raw map[string]interface{}, fallback int) int {
	for _, key := range []string{"latest_change_id", "next_change_id", "change_id"} {
		if v, ok := raw[key].(float64); ok && int(v) > fallback {
			return int(v)
		}
	}
	return fallback
}

func liveExtractLines(raw map[string]interface{}) []string {
	var out []string

	if arr, ok := raw["lines"].([]interface{}); ok {
		for _, item := range arr {
			liveAppendItem(&out, item)
		}
	}

	if steps, ok := raw["steps"].([]interface{}); ok {
		for _, s := range steps {
			sm, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			if log, ok := sm["log"].(string); ok {
				out = append(out, log)
			}
			if arr, ok := sm["lines"].([]interface{}); ok {
				for _, item := range arr {
					liveAppendItem(&out, item)
				}
			}
		}
	}

	return out
}

func liveAppendItem(out *[]string, item interface{}) {
	switch v := item.(type) {
	case string:
		*out = append(*out, v)
	case map[string]interface{}:
		for _, key := range []string{"message", "content", "line", "text", "data"} {
			if s, ok := v[key].(string); ok {
				*out = append(*out, s)
				return
			}
		}
	}
}

// GetJobLogBlobURL returns the redirect URL for a job's log without downloading it.
// For a running job this may return a plain-text append-blob; for a completed job
// it returns the zip blob. Returns ("", nil) when no log is available yet (404).
func (c *GitHubClient) GetJobLogBlobURL(jobID int64) (string, error) {
	token, _ := auth.TokenForHost(c.host)

	apiBase := "https://api.github.com"
	if c.host != "github.com" {
		apiBase = "https://" + c.host + "/api/v3"
	}
	reqURL := fmt.Sprintf("%s/repos/%s/%s/actions/jobs/%d/logs", apiBase, c.owner, c.repo, jobID)
	dbg("GetJobLogBlobURL: GET %s", reqURL)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	noRedirect := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 10 * time.Second,
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	dbg("GetJobLogBlobURL: status=%d location=%s", resp.StatusCode, resp.Header.Get("Location"))
	if resp.StatusCode == http.StatusFound {
		return resp.Header.Get("Location"), nil
	}
	return "", nil
}

// FetchLogRange fetches bytes from a blob URL starting at offset.
// Returns ("", sameOffset, nil) when no new content is available (416).
// The returned content has timestamps stripped. newOffset is offset+len(raw bytes read).
func FetchLogRange(blobURL string, offset int64) (content string, newOffset int64, err error) {
	req, err := http.NewRequest("GET", blobURL, nil)
	if err != nil {
		return "", offset, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	dbg("FetchLogRange: GET offset=%d url=%s", offset, blobURL[:min(80, len(blobURL))])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", offset, err
	}
	defer resp.Body.Close()

	dbg("FetchLogRange: status=%d", resp.StatusCode)

	switch resp.StatusCode {
	case http.StatusRequestedRangeNotSatisfiable: // 416 — no new bytes
		return "", offset, nil
	case http.StatusOK, http.StatusPartialContent: // 200 or 206
		// fine
	default:
		return "", offset, fmt.Errorf("blob fetch: unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", offset, err
	}
	dbg("FetchLogRange: got %d bytes", len(data))

	// Running-job blobs are plain text, but be safe: reject zip data in Range responses.
	if len(data) >= 2 && data[0] == 'P' && data[1] == 'K' {
		dbg("FetchLogRange: zip detected, skipping range approach")
		return "", offset, fmt.Errorf("blob is zip-encoded, range not supported")
	}

	return processLogLines(string(data)), offset + int64(len(data)), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseZipLog(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		sb.WriteString(processLogLines(string(content)))
	}
	return sb.String(), nil
}

// processLogLines strips GitHub Actions timestamp prefixes from each log line.
// Timestamps look like: "2024-01-01T00:00:00.0000000Z "
func processLogLines(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		// A timestamp prefix starts with a 4-digit year and contains 'T'
		if len(line) > 30 && line[4] == '-' && line[7] == '-' {
			if idx := strings.IndexByte(line, ' '); idx > 0 && idx < 35 {
				line = line[idx+1:]
			}
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// OpenInBrowser opens a URL in the default browser
func OpenInBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return exec.Command(cmd, args...).Start()
}

// RerunFailedJobs triggers a re-run of only failed jobs in a workflow run.
func (c *GitHubClient) RerunFailedJobs(runID int64) error {
	return c.rest.Post(
		fmt.Sprintf("repos/%s/%s/actions/runs/%d/rerun-failed-jobs", c.owner, c.repo, runID),
		nil, nil,
	)
}

// RerunAll triggers a re-run of all jobs in a workflow run.
func (c *GitHubClient) RerunAll(runID int64) error {
	return c.rest.Post(
		fmt.Sprintf("repos/%s/%s/actions/runs/%d/rerun", c.owner, c.repo, runID),
		nil, nil,
	)
}

// ─── GHES pipeline service (per-step log fetching) ───────────────────────────

// pipelineServiceInfo holds the coordinates needed to call the Azure DevOps
// Pipelines API that GHES uses for per-step log storage.
type pipelineServiceInfo struct {
	serviceBase   string // e.g. "https://host/_services/pipelines/{token}"
	pipelineID    int
	pipelineRunID int
	authToken     string // GitHub PAT for the host
}

// parsePipelineServiceURL extracts pipeline service coordinates from a signed
// log URL returned by the GHES job logs redirect.
//
// Example URL:
//
//	https://host/_services/pipelines/{token}/_apis/pipelines/1/runs/14165/signedlogcontent/32?...
//
// Returns nil if the URL is not a GHES pipeline service URL (e.g. Azure blob).
func parsePipelineServiceURL(rawURL string) *pipelineServiceInfo {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.Contains(u.Path, "/_services/pipelines/") {
		return nil
	}
	// Path: /_services/pipelines/{token}/_apis/pipelines/{pipelineID}/runs/{runID}/...
	parts := strings.Split(u.Path, "/")
	apisIdx := -1
	for i, p := range parts {
		if p == "_apis" {
			apisIdx = i
			break
		}
	}
	// Need at least: _apis / pipelines / {id} / runs / {id}
	if apisIdx < 0 || apisIdx+5 > len(parts) {
		return nil
	}
	pipelineID, err := strconv.Atoi(parts[apisIdx+2])
	if err != nil {
		return nil
	}
	runID, err := strconv.Atoi(parts[apisIdx+4])
	if err != nil {
		return nil
	}
	// serviceBase = scheme://host + path up to (not including) /_apis/...
	serviceBase := u.Scheme + "://" + u.Host + strings.Join(parts[:apisIdx], "/")
	return &pipelineServiceInfo{
		serviceBase:   serviceBase,
		pipelineID:    pipelineID,
		pipelineRunID: runID,
	}
}

// GetPipelineServiceInfo follows the job logs redirect and returns pipeline
// coordinates if the destination is a GHES pipeline service URL.
// Returns (nil, nil) when the job is on github.com or logs aren't ready yet.
func (c *GitHubClient) GetPipelineServiceInfo(jobID int64) (*pipelineServiceInfo, error) {
	blobURL, err := c.GetJobLogBlobURL(jobID)
	if err != nil || blobURL == "" {
		return nil, err
	}
	info := parsePipelineServiceURL(blobURL)
	if info != nil {
		info.authToken, _ = auth.TokenForHost(c.host)
	}
	return info, nil
}

// timelineLog is the log reference embedded in a build timeline record.
type timelineLog struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

// timelineRecord is one entry in the Azure DevOps build timeline.
type timelineRecord struct {
	Name string       `json:"name"`
	Type string       `json:"type"` // "Job", "Task", "Phase", "Checkpoint"
	Log  *timelineLog `json:"log"`
}

// getBuildTimeline fetches the Azure DevOps build timeline, which maps each
// step name to its log ID and content URL.
func getBuildTimeline(info *pipelineServiceInfo) ([]timelineRecord, error) {
	reqURL := fmt.Sprintf("%s/_apis/build/builds/%d/timeline?api-version=5.0",
		info.serviceBase, info.pipelineRunID)
	dbg("getBuildTimeline: GET %s", reqURL)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if info.authToken != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(":" + info.authToken))
		req.Header.Set("Authorization", "Basic "+encoded)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	dbg("getBuildTimeline: status=%d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("build timeline: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Records []timelineRecord `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	dbg("getBuildTimeline: got %d records", len(result.Records))
	return result.Records, nil
}

// fetchLogFromURL downloads log content from a direct Build API log URL.
func fetchLogFromURL(logURL, authToken string) (string, error) {
	req, err := http.NewRequest("GET", logURL, nil)
	if err != nil {
		return "", err
	}
	if authToken != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(":" + authToken))
		req.Header.Set("Authorization", "Basic "+encoded)
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	dbg("fetchLogFromURL: status=%d url=%s", resp.StatusCode, logURL[:min(80, len(logURL))])
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("log fetch: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return processLogLines(string(data)), nil
}

// FetchNewStepLogs fetches log content for each completed step whose Number
// is greater than maxFetchedStepNum. It uses the build timeline to map step
// names to their log URLs, then fetches each log directly via the Build API.
func FetchNewStepLogs(info *pipelineServiceInfo, steps []Step, maxFetchedStepNum int) (string, int, error) {
	records, err := getBuildTimeline(info)
	if err != nil {
		return "", maxFetchedStepNum, err
	}

	// Build a name → log URL map from Task-type timeline records.
	nameToLogURL := make(map[string]string)
	for _, r := range records {
		if r.Type == "Task" && r.Log != nil && r.Log.URL != "" {
			nameToLogURL[r.Name] = r.Log.URL
		}
	}
	dbg("FetchNewStepLogs: %d task records in timeline", len(nameToLogURL))

	var sb strings.Builder
	newMax := maxFetchedStepNum
	for _, step := range steps {
		if step.Status != "completed" || step.Number <= maxFetchedStepNum {
			continue
		}
		logURL, ok := nameToLogURL[step.Name]
		if !ok {
			dbg("FetchNewStepLogs: no timeline record for step %d (%q)", step.Number, step.Name)
			continue
		}
		content, err := fetchLogFromURL(logURL, info.authToken)
		if err != nil {
			dbg("FetchNewStepLogs: step %d (%s): %v", step.Number, step.Name, err)
			continue
		}
		content = strings.TrimRight(content, "\n")
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(content)
		if step.Number > newMax {
			newMax = step.Number
		}
	}
	return sb.String(), newMax, nil
}
