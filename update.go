package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// ─── Message types ────────────────────────────────────────────────────────────

type runsLoadedMsg []WorkflowRun
type jobsLoadedMsg []Job
type logsLoadedMsg string
type rerunMsg struct {
	message string
	runID   int64
	jobID   int64 // job ID of the retriggered job, if known
}
type logPollTickMsg struct{}
type errMsg struct{ err error }

// ─── Command helpers ──────────────────────────────────────────────────────────

func fetchRunsCmd(c *GitHubClient) tea.Cmd {
	return func() tea.Msg {
		runs, err := c.ListRuns()
		if err != nil {
			return errMsg{err}
		}
		return runsLoadedMsg(runs)
	}
}

func fetchJobsCmd(c *GitHubClient, runID int64) tea.Cmd {
	return func() tea.Msg {
		jobs, err := c.ListJobs(runID)
		if err != nil {
			return errMsg{err}
		}
		return jobsLoadedMsg(jobs)
	}
}

func fetchLogsCmd(c *GitHubClient, jobID int64) tea.Cmd {
	return func() tea.Msg {
		logs, err := c.GetJobLogs(jobID)
		if err != nil {
			return errMsg{err}
		}
		return logsLoadedMsg(logs)
	}
}

func logPollCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(_ time.Time) tea.Msg {
		return logPollTickMsg{}
	})
}

func rerunFailedCmd(c *GitHubClient, runID int64) tea.Cmd {
	return func() tea.Msg {
		if err := c.RerunFailedJobs(runID); err != nil {
			return errMsg{err}
		}
		return rerunMsg{message: "Re-run triggered for failed jobs!", runID: runID}
	}
}

func rerunAllCmd(c *GitHubClient, runID int64) tea.Cmd {
	return func() tea.Msg {
		if err := c.RerunAll(runID); err != nil {
			return errMsg{err}
		}
		return rerunMsg{message: "Re-run triggered for all jobs!", runID: runID}
	}
}

// ─── Init ─────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return tea.Batch(
		fetchRunsCmd(m.client),
		m.spinner.Tick,
	)
}

// ─── Update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Subtract header (3 lines) + footer (1 line) = 4
		listH := max(1, msg.Height-4)
		m.runsList.SetSize(msg.Width, listH)
		m.jobsList.SetSize(msg.Width, listH)
		// Update delegate widths
		m.runsList.SetDelegate(runDelegate{width: msg.Width})
		m.jobsList.SetDelegate(jobDelegate{width: msg.Width})
		m.updateSizes()

	case tea.KeyMsg:
		// While the runs list is in filter mode, route all input directly to it.
		if m.state == stateRuns && m.runsList.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.runsList, cmd = m.runsList.Update(msg)
			return m, cmd
		}

		switch msg.String() {

		case "ctrl+c":
			return m, tea.Quit

		case "q":
			// Don't quit while filter is applied — let esc clear it first.
			if m.state == stateRuns && m.runsList.FilterState() == list.FilterApplied {
				var cmd tea.Cmd
				m.runsList, cmd = m.runsList.Update(msg)
				return m, cmd
			}
			return m, tea.Quit

		case "enter":
			switch m.state {
			case stateRuns:
				if item, ok := m.runsList.SelectedItem().(runItem); ok {
					m.selectedRun = item.run
					m.state = stateJobs
					m.loading = true
					m.statusMsg = ""
					cmds = append(cmds, fetchJobsCmd(m.client, item.run.ID))
					return m, tea.Batch(cmds...)
				}
			case stateJobs:
				if item, ok := m.jobsList.SelectedItem().(jobItem); ok {
					m.selectedJob = item.job
					m.state = stateLogs
					m.logContent = ""
					m.logRaw = ""
					m.logLoaded = false
					m.autoScroll = true
					m.selectedLogLine = 0
					m.statusMsg = ""
					m.updateSizes()
					cmds = append(cmds, fetchLogsCmd(m.client, item.job.ID))
					return m, tea.Batch(cmds...)
				}
			}

		case "esc", "b":
			switch m.state {
			case stateJobs:
				m.state = stateRuns
				m.statusMsg = ""
				return m, nil
			case stateLogs:
				m.state = stateJobs
				m.statusMsg = ""
				return m, nil
			}
			// stateRuns: fall through — let the list handle esc (clear filter)

		case "r":
			switch m.state {
			case stateRuns:
				if item, ok := m.runsList.SelectedItem().(runItem); ok {
					cmds = append(cmds, rerunFailedCmd(m.client, item.run.ID))
					return m, tea.Batch(cmds...)
				}
			case stateJobs:
				cmds = append(cmds, rerunFailedCmd(m.client, m.selectedRun.ID))
				return m, tea.Batch(cmds...)
			case stateLogs:
				m.logLoaded = false
				cmds = append(cmds, fetchLogsCmd(m.client, m.selectedJob.ID))
				return m, tea.Batch(cmds...)
			}

		case "R":
			switch m.state {
			case stateRuns:
				if item, ok := m.runsList.SelectedItem().(runItem); ok {
					cmds = append(cmds, rerunAllCmd(m.client, item.run.ID))
					return m, tea.Batch(cmds...)
				}
			case stateJobs:
				cmds = append(cmds, rerunAllCmd(m.client, m.selectedRun.ID))
				return m, tea.Batch(cmds...)
			}

		case "tab", "ctrl+r":
			if m.state == stateRuns {
				m.loading = true
				m.statusMsg = ""
				cmds = append(cmds, fetchRunsCmd(m.client))
				return m, tea.Batch(cmds...)
			}

		case "a":
			if m.state == stateLogs {
				m.autoScroll = !m.autoScroll
				if m.autoScroll {
					m.logViewport.GotoBottom()
				}
			}

		case "g":
			if m.state == stateLogs {
				m.logViewport.GotoTop()
				m.autoScroll = false
				return m, nil
			}

		case "G":
			if m.state == stateLogs {
				m.logViewport.GotoBottom()
				return m, nil
			}

		case "up":
			if m.state == stateLogs {
				if m.selectedLogLine > 0 {
					m.selectedLogLine--
					if m.logRaw != "" {
						rendered := renderLogs(m.logRaw, m.selectedLogLine)
						m.logContent = rendered
						m.logViewport.SetContent(rendered)
					}
				}
				return m, nil
			}

		case "down":
			if m.state == stateLogs {
				logLines := strings.Count(m.logRaw, "\n") + 1
				if m.selectedLogLine < logLines-1 {
					m.selectedLogLine++
					if m.logRaw != "" {
						rendered := renderLogs(m.logRaw, m.selectedLogLine)
						m.logContent = rendered
						m.logViewport.SetContent(rendered)
					}
				}
				return m, nil
			}

		case "c":
			if m.state == stateLogs {
				if err := clipboard.WriteAll(m.logRaw); err != nil {
					m.statusMsg = fmt.Sprintf("error copying logs: %v", err)
				} else {
					m.statusMsg = "✓ Logs copied to clipboard"
				}
				return m, nil
			}
		}

	// ─── Data messages ─────────────────────────────────────────────────────

	case runsLoadedMsg:
		m.loading = false
		items := make([]list.Item, len(msg))
		for i, r := range msg {
			items[i] = runItem{r}
		}
		_ = m.runsList.SetItems(items)

	case jobsLoadedMsg:
		m.loading = false
		items := make([]list.Item, len(msg))
		for i, j := range msg {
			items[i] = jobItem{j}
		}
		// SetItems also accepts commands - call it but don't queue the command
		_ = m.jobsList.SetItems(items)
		
		// Check for new jobs (auto-jump on rerun)
		runID := m.selectedRun.ID
		oldJobs := m.lastJobsForRun[runID]
		
		// Find new jobs that weren't in the old list
		var newJobs []Job
		for _, j := range msg {
			found := false
			for _, old := range oldJobs {
				if old.ID == j.ID {
					found = true
					break
				}
			}
			if !found {
				newJobs = append(newJobs, j)
			}
		}
		
		// If we're in stateJobs and there are new jobs, select the first one
		if m.state == stateJobs && len(newJobs) > 0 {
			// Find the index of the new job and auto-select it
			for i, item := range items {
				if ji, ok := item.(jobItem); ok && ji.job.ID == newJobs[0].ID {
					m.jobsList.Select(i)
					m.statusMsg = "✓ Jumped to re-triggered job"
					break
				}
			}
		}
		
		// Update the cache for this run
		m.lastJobsForRun[runID] = msg
		
		// Keep selectedJob in sync while viewing logs
		if m.state == stateLogs {
			for _, j := range msg {
				if j.ID == m.selectedJob.ID {
					m.selectedJob = j
					break
				}
			}
		}

	case logsLoadedMsg:
		rawContent := string(msg)
		rendered := renderLogs(rawContent, m.selectedLogLine)
		savedOffset := m.logViewport.YOffset
		m.logViewport.SetContent(rendered)
		m.logContent = rendered
		m.logRaw = rawContent
		m.logLoaded = true
		if m.autoScroll {
			m.logViewport.GotoBottom()
		} else {
			m.logViewport.YOffset = savedOffset
		}
		// Continue polling while viewing logs to enable live log streaming
		if m.state == stateLogs {
			cmds = append(cmds, logPollCmd())
		}

	case logPollTickMsg:
		if m.state == stateLogs {
			cmds = append(cmds, fetchLogsCmd(m.client, m.selectedJob.ID))
			cmds = append(cmds, fetchJobsCmd(m.client, m.selectedRun.ID))
		}

	case rerunMsg:
		m.statusMsg = msg.message
		m.loading = true
		// After rerun, refresh jobs for this run and potentially auto-jump
		cmds = append(cmds, fetchJobsCmd(m.client, msg.runID))

	case errMsg:
		m.loading = false
		m.statusMsg = fmt.Sprintf("error: %v", msg.err)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Delegate remaining messages to the active component.
	switch m.state {
	case stateRuns:
		var cmd tea.Cmd
		m.runsList, cmd = m.runsList.Update(msg)
		cmds = append(cmds, cmd)
	case stateJobs:
		var cmd tea.Cmd
		m.jobsList, cmd = m.jobsList.Update(msg)
		cmds = append(cmds, cmd)
	case stateLogs:
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// updateSizes resizes the log viewport to fit the current terminal dimensions.
func (m *model) updateSizes() {
	h := max(1, m.height-4)
	savedOffset := m.logViewport.YOffset
	m.logViewport.Width = m.width
	m.logViewport.Height = h
	if m.logContent != "" {
		m.logViewport.SetContent(m.logContent)
		if m.autoScroll {
			m.logViewport.GotoBottom()
		} else {
			m.logViewport.YOffset = savedOffset
		}
	}
}
