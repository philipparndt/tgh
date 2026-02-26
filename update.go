package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
type jobsPollTickMsg struct{}
type errMsg struct{ err error }
type pipelineInfoMsg struct{ info *pipelineServiceInfo }
type stepLogsMsg struct {
	content      string
	maxFetchedID int
}

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

// isRunning reports whether a job status means the job hasn't finished.
func isRunning(status string) bool {
	return status == "in_progress" || status == "queued"
}

// countCompletedSteps returns the number of completed steps in a job.
func countCompletedSteps(steps []Step) int {
	count := 0
	for _, s := range steps {
		if s.Status == "completed" {
			count++
		}
	}
	return count
}

// applyLogFilter re-renders the log viewport from m.logRaw, applying m.logFilter.
func (m *model) applyLogFilter() {
	content := m.logRaw
	if m.logFilter != "" {
		lower := strings.ToLower(m.logFilter)
		var filtered []string
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(strings.ToLower(line), lower) {
				filtered = append(filtered, line)
			}
		}
		content = strings.Join(filtered, "\n")
	}
	rendered := renderLogs(content)
	m.logViewport.SetContent(rendered)
	m.logContent = rendered
	if m.autoScroll {
		m.logViewport.GotoBottom()
	}
}

func jobsPollCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(_ time.Time) tea.Msg {
		return jobsPollTickMsg{}
	})
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

func fetchPipelineInfoCmd(c *GitHubClient, jobID int64) tea.Cmd {
	return func() tea.Msg {
		info, err := c.GetPipelineServiceInfo(jobID)
		if err != nil {
			dbg("fetchPipelineInfoCmd: %v", err)
			return pipelineInfoMsg{info: nil}
		}
		return pipelineInfoMsg{info: info}
	}
}

func fetchStepLogsCmd(info *pipelineServiceInfo, steps []Step, maxFetchedID int) tea.Cmd {
	return func() tea.Msg {
		content, newMax, err := FetchNewStepLogs(info, steps, maxFetchedID)
		if err != nil {
			return errMsg{err}
		}
		return stepLogsMsg{content: content, maxFetchedID: newMax}
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

		// While the log filter bar is active, handle input for the filter.
		if m.state == stateLogs && m.logFilterMode {
			switch msg.String() {
			case "esc":
				m.logFilter = ""
				m.logFilterMode = false
				m.applyLogFilter()
				m.updateSizes()
			case "enter":
				m.logFilterMode = false
				m.updateSizes()
			case "backspace":
				if len(m.logFilter) > 0 {
					runes := []rune(m.logFilter)
					m.logFilter = string(runes[:len(runes)-1])
					m.applyLogFilter()
				}
			case "ctrl+u":
				m.logFilter = ""
				m.applyLogFilter()
			case "up":
				if m.logViewport.YOffset > 0 {
					m.logViewport.YOffset--
					m.autoScroll = false
				}
			case "down":
				totalH := lipgloss.Height(m.logContent)
				maxOff := max(0, totalH-m.logViewport.Height)
				if m.logViewport.YOffset < maxOff {
					m.logViewport.YOffset++
				}
				if m.logViewport.YOffset >= maxOff {
					m.autoScroll = true
				}
			default:
				if len(msg.Runes) > 0 {
					m.logFilter += string(msg.Runes)
					m.applyLogFilter()
				}
			}
			return m, nil
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

		case "/":
			if m.state == stateLogs && !isRunning(m.selectedJob.Status) {
				m.logFilterMode = true
				m.updateSizes()
				return m, nil
			}

		case "enter":
			switch m.state {
			case stateRuns:
				if item, ok := m.runsList.SelectedItem().(runItem); ok {
					m.selectedRun = item.run
					m.state = stateJobs
					m.loading = true
					m.statusMsg = ""
					m.jobsPolling = true
					cmds = append(cmds, fetchJobsCmd(m.client, item.run.ID))
					cmds = append(cmds, jobsPollCmd())
					return m, tea.Batch(cmds...)
				}
			case stateJobs:
				if item, ok := m.jobsList.SelectedItem().(jobItem); ok {
					m.selectedJob = item.job
					m.state = stateLogs
					m.jobsPolling = false
					m.logContent = ""
					m.logRaw = ""
					m.lastLogLength = 0
					m.logLoaded = false
					m.autoScroll = true
					m.statusMsg = ""
					m.logFilter = ""
					m.logFilterMode = false
					m.pipelineInfo = nil
					m.stepLogsFetched = 0
					m.updateSizes()
					if isRunning(item.job.Status) {
						// While running we show the steps view; just poll for job/step updates.
						cmds = append(cmds, fetchJobsCmd(m.client, m.selectedRun.ID))
						cmds = append(cmds, logPollCmd())
					} else {
						cmds = append(cmds, fetchLogsCmd(m.client, item.job.ID))
					}
					return m, tea.Batch(cmds...)
				}
			}

		case "esc", "b":
			switch m.state {
			case stateJobs:
				m.state = stateRuns
				m.jobsPolling = false
				m.jobsPollStartIDs = nil
				m.statusMsg = ""
				return m, nil
			case stateLogs:
				m.state = stateJobs
				m.statusMsg = ""
				m.jobsPolling = true
				cmds = append(cmds, jobsPollCmd())
				return m, tea.Batch(cmds...)
			}
			// stateRuns: fall through — let the list handle esc (clear filter)

		case "r":
			switch m.state {
			case stateRuns:
				if item, ok := m.runsList.SelectedItem().(runItem); ok {
					m.statusMsg = "Triggering rerun of failed jobs…"
					m.loading = true
					cmds = append(cmds, rerunFailedCmd(m.client, item.run.ID))
					return m, tea.Batch(cmds...)
				}
			case stateJobs:
				m.statusMsg = "Triggering rerun of failed jobs…"
				m.loading = true
				cmds = append(cmds, rerunFailedCmd(m.client, m.selectedRun.ID))
				return m, tea.Batch(cmds...)
			case stateLogs:
				m.logLoaded = false
				m.lastLogLength = 0
				m.logRaw = ""
				m.logContent = ""
				m.logFilter = ""
				m.logFilterMode = false
				m.pipelineInfo = nil
				m.stepLogsFetched = 0
				if isRunning(m.selectedJob.Status) {
					cmds = append(cmds, fetchJobsCmd(m.client, m.selectedRun.ID))
					cmds = append(cmds, logPollCmd())
				} else {
					cmds = append(cmds, fetchLogsCmd(m.client, m.selectedJob.ID))
				}
				return m, tea.Batch(cmds...)
			}

		case "R":
			switch m.state {
			case stateRuns:
				if item, ok := m.runsList.SelectedItem().(runItem); ok {
					m.statusMsg = "Triggering rerun of all jobs…"
					m.loading = true
					cmds = append(cmds, rerunAllCmd(m.client, item.run.ID))
					return m, tea.Batch(cmds...)
				}
			case stateJobs:
				m.statusMsg = "Triggering rerun of all jobs…"
				m.loading = true
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

		case "o":
			switch m.state {
			case stateJobs:
				if item, ok := m.jobsList.SelectedItem().(jobItem); ok {
					if item.job.HTMLURL != "" {
						if err := OpenInBrowser(item.job.HTMLURL); err != nil {
							m.statusMsg = fmt.Sprintf("error opening browser: %v", err)
						} else {
							m.statusMsg = "✓ Opened job in browser"
						}
					} else {
						m.statusMsg = "Job URL not available"
					}
				}
				return m, nil
			case stateLogs:
				if m.selectedJob.HTMLURL != "" {
					if err := OpenInBrowser(m.selectedJob.HTMLURL); err != nil {
						m.statusMsg = fmt.Sprintf("error opening browser: %v", err)
					} else {
						m.statusMsg = "✓ Opened job in browser"
					}
				} else {
					m.statusMsg = "Job URL not available"
				}
				return m, nil
			}

		case "up":
			if m.state == stateLogs {
				if m.logViewport.YOffset > 0 {
					m.logViewport.YOffset--
					m.autoScroll = false
				}
				return m, nil
			}

		case "pgup":
			if m.state == stateLogs {
				m.logViewport.YOffset = max(0, m.logViewport.YOffset-m.logViewport.Height/2)
				m.autoScroll = false
				return m, nil
			}

		case "down":
			if m.state == stateLogs {
				totalHeight := lipgloss.Height(m.logContent)
				maxOffset := max(0, totalHeight-m.logViewport.Height)

				if m.logViewport.YOffset < maxOffset {
					m.logViewport.YOffset++
				}

				// Re-enable auto-scroll when at the bottom
				if m.logViewport.YOffset >= maxOffset {
					m.autoScroll = true
					m.logViewport.GotoBottom()
				}
				return m, nil
			}

		case "pgdn":
			if m.state == stateLogs {
				totalHeight := lipgloss.Height(m.logContent)
				maxOffset := max(0, totalHeight-m.logViewport.Height)
				m.logViewport.YOffset = min(maxOffset, m.logViewport.YOffset+m.logViewport.Height/2)
				if m.logViewport.YOffset >= maxOffset {
					m.autoScroll = true
					m.logViewport.GotoBottom()
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
		cmds = append(cmds, m.runsList.SetItems(items))

	case jobsLoadedMsg:
		m.loading = false

		// Phase 1 (after rerun): keep list empty until genuinely new job IDs appear.
		if m.jobsPollStartIDs != nil {
			hasNew := false
			for _, j := range msg {
				if !m.jobsPollStartIDs[j.ID] {
					hasNew = true
					break
				}
			}
			if !hasNew {
				break // new jobs not created yet; keep list empty, tick will retry
			}
			m.jobsPollStartIDs = nil // new jobs detected, exit waiting phase
		}

		runID := m.selectedRun.ID
		oldJobs := m.lastJobsForRun[runID]

		// Build items and detect first-time-seen jobs (for auto-jump).
		items := make([]list.Item, len(msg))
		for i, j := range msg {
			items[i] = jobItem{j}
		}
		cmds = append(cmds, m.jobsList.SetItems(items))

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
		if m.state == stateJobs && len(newJobs) > 0 {
			for i, item := range items {
				if ji, ok := item.(jobItem); ok && ji.job.ID == newJobs[0].ID {
					m.jobsList.Select(i)
					m.statusMsg = "✓ Jumped to re-triggered job"
					break
				}
			}
		}

		// Update the cache.
		m.lastJobsForRun[runID] = msg


		// Keep selectedJob in sync while viewing logs.
		if m.state == stateLogs {
			for _, j := range msg {
				if j.ID == m.selectedJob.ID {
					wasRunning := isRunning(m.selectedJob.Status)
					m.selectedJob = j
					isNowDone := wasRunning && !isRunning(m.selectedJob.Status)
					if isNowDone {
						// Job just completed — fetch the full log.
						m.pipelineInfo = nil
						cmds = append(cmds, fetchLogsCmd(m.client, m.selectedJob.ID))
					}
					break
				}
			}
		}

	case logsLoadedMsg:
		rawContent := string(msg)
		dbg("logsLoadedMsg: %d bytes, jobStatus=%s", len(rawContent), m.selectedJob.Status)
		if rawContent != "" {
			m.logRaw = rawContent
			m.lastLogLength = len(rawContent)
			m.logLoaded = true
			m.applyLogFilter()
		} else if !m.logLoaded {
			waitingMsg := "Waiting for logs..."
			m.logViewport.SetContent(waitingMsg)
			m.logContent = waitingMsg
			m.logLoaded = true
		}

	case logPollTickMsg:
		if m.state == stateLogs {
			if isRunning(m.selectedJob.Status) {
				// Steps view: just refresh job/step status, no log fetch needed.
				cmds = append(cmds, fetchJobsCmd(m.client, m.selectedRun.ID))
				cmds = append(cmds, logPollCmd())
			} else {
				// Job finished while we were polling — fetch the full log.
				cmds = append(cmds, fetchLogsCmd(m.client, m.selectedJob.ID))
			}
		}

	case rerunMsg:
		m.statusMsg = msg.message
		// Snapshot current job IDs so we can detect when genuinely new ones appear.
		m.jobsPollStartIDs = make(map[int64]bool)
		for _, item := range m.jobsList.Items() {
			if ji, ok := item.(jobItem); ok {
				m.jobsPollStartIDs[ji.job.ID] = true
			}
		}
		if m.state == stateJobs {
			// Clear the list immediately — new jobs will appear via the running poll.
			cmds = append(cmds, m.jobsList.SetItems([]list.Item{}))
		}
		if !m.jobsPolling {
			// Triggered from stateRuns; start polling now.
			m.jobsPolling = true
			cmds = append(cmds, jobsPollCmd())
		}

	case jobsPollTickMsg:
		if m.jobsPolling {
			if m.state == stateJobs {
				cmds = append(cmds, fetchJobsCmd(m.client, m.selectedRun.ID))
			}
			cmds = append(cmds, jobsPollCmd()) // always keep the chain alive
		}

	case errMsg:
		m.loading = false
		m.statusMsg = fmt.Sprintf("error: %v", msg.err)

	case pipelineInfoMsg:
		m.pipelineInfo = msg.info

	case stepLogsMsg:
		if msg.maxFetchedID > m.stepLogsFetched {
			m.stepLogsFetched = msg.maxFetchedID
			if msg.content != "" {
				if m.logRaw != "" {
					m.logRaw += "\n"
				}
				m.logRaw += msg.content
				m.logLoaded = true
				m.applyLogFilter()
			} else if !m.logLoaded {
				waitingMsg := "Waiting for step logs..."
				m.logViewport.SetContent(waitingMsg)
				m.logContent = waitingMsg
				m.logLoaded = true
			}
		}

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
	extra := 0
	if m.logFilterMode {
		extra = 1
	}
	h := max(1, m.height-4-extra)
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
