package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
)

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
	owner string
	repo  string
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
func (c *GitHubClient) GetJobLogs(jobID int64) (string, error) {
	resp, err := c.rest.Request("GET",
		fmt.Sprintf("repos/%s/%s/actions/jobs/%d/logs", c.owner, c.repo, jobID),
		nil,
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

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
