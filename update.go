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
type prsLoadedMsg []PullRequest
type workflowsLoadedMsg []Workflow
type workflowInputsMsg []WorkflowInput
type refOptionsMsg struct {
	branches []string
	tags     []string
}
type dispatchTriggeredMsg string
type defaultBranchMsg string
type rerunMsg struct {
	message string
	runID   int64
	jobID   int64
}
type logPollTickMsg struct{}
type jobsPollTickMsg struct{}
type runsPollTickMsg struct{}
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

func fetchRunsForPRCmd(c *GitHubClient, headSHA string) tea.Cmd {
	return func() tea.Msg {
		runs, err := c.ListRunsForPR(headSHA)
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

func fetchPRsCmd(c *GitHubClient) tea.Cmd {
	return func() tea.Msg {
		prs, err := c.ListPullRequests()
		if err != nil {
			return errMsg{err}
		}
		return prsLoadedMsg(prs)
	}
}

func fetchWorkflowsCmd(c *GitHubClient) tea.Cmd {
	return func() tea.Msg {
		wfs, err := c.ListWorkflows()
		if err != nil {
			return errMsg{err}
		}
		return workflowsLoadedMsg(wfs)
	}
}

func fetchDefaultBranchCmd(c *GitHubClient) tea.Cmd {
	return func() tea.Msg {
		branch, err := c.GetDefaultBranch()
		if err != nil {
			return defaultBranchMsg("main")
		}
		return defaultBranchMsg(branch)
	}
}

func fetchWorkflowInputsCmd(c *GitHubClient, workflow Workflow) tea.Cmd {
	return func() tea.Msg {
		inputs, err := c.GetWorkflowInputs(workflow.Path)
		if err != nil {
			return errMsg{err}
		}
		return workflowInputsMsg(inputs)
	}
}

func fetchRefOptionsCmd(c *GitHubClient) tea.Cmd {
	return func() tea.Msg {
		branches, tags, err := c.ListRefs()
		if err != nil {
			return errMsg{err}
		}
		return refOptionsMsg{branches: branches, tags: tags}
	}
}

func triggerDispatchCmd(c *GitHubClient, workflowID int64, ref string, inputs map[string]string) tea.Cmd {
	return func() tea.Msg {
		if err := c.TriggerWorkflowDispatch(workflowID, ref, inputs); err != nil {
			return errMsg{err}
		}
		return dispatchTriggeredMsg("✓ Workflow dispatched on " + ref)
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

func runsPollCmd() tea.Cmd {
	return tea.Tick(10*time.Second, func(_ time.Time) tea.Msg {
		return runsPollTickMsg{}
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
	return m.spinner.Tick
}

// ─── Update ───────────────────────────────────────────────────────────────────

// numMenuItems is the number of items in the main menu.
const numMenuItems = 2

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		listH := max(1, msg.Height-4)
		m.runsList.SetSize(msg.Width, listH)
		m.jobsList.SetSize(msg.Width, listH)
		m.prsList.SetSize(msg.Width, listH)
		m.workflowsList.SetSize(msg.Width, listH)
		m.runsList.SetDelegate(runDelegate{width: msg.Width})
		m.jobsList.SetDelegate(jobDelegate{width: msg.Width})
		m.prsList.SetDelegate(prDelegate{width: msg.Width})
		m.workflowsList.SetDelegate(workflowDelegate{width: msg.Width})
		m.updateSizes()

	case tea.KeyMsg:
		// Main menu navigation — handle before everything else.
		if m.state == stateMenu {
			switch msg.String() {
			case "up", "k":
				if m.menuIndex > 0 {
					m.menuIndex--
				}
				return m, nil
			case "down", "j":
				if m.menuIndex < numMenuItems-1 {
					m.menuIndex++
				}
				return m, nil
			case "enter":
				switch m.menuIndex {
				case 0: // Actions
					m.state = stateRuns
					m.loading = true
					m.statusMsg = ""
					m.selectedPR = nil
					m.runsPolling = true
					return m, tea.Batch(fetchRunsCmd(m.client), runsPollCmd())
				case 1: // Pull Requests
					m.state = statePRs
					m.loading = true
					m.statusMsg = ""
					return m, fetchPRsCmd(m.client)
				}
				return m, nil
			}
			// Fall through for ctrl+c, q, etc.
		}

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

		// Dispatch form keyboard handling — fully handled here, always returns early.
		if m.state == stateDispatchForm {
			key := msg.String()

			// Keys that always apply regardless of focus.
			switch key {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.state = stateWorkflows
				m.formFields = nil
				m.formButton = 0
				return m, nil
			case "tab":
				if m.formButton != 0 {
					// Buttons → first field
					m.formButton = 0
					if len(m.formFields) > 0 {
						m.formActiveField = 0
						return m, m.formFields[0].input.Focus()
					}
					return m, nil
				}
				if len(m.formFields) > 0 {
					m.formFields[m.formActiveField].input.Blur()
					if m.formActiveField == len(m.formFields)-1 {
						// Last field → Build button
						m.formButton = 2
						return m, nil
					}
					m.formActiveField++
					return m, m.formFields[m.formActiveField].input.Focus()
				}
				return m, nil
			case "shift+tab":
				if m.formButton != 0 {
					// Buttons → last field
					m.formButton = 0
					if len(m.formFields) > 0 {
						m.formActiveField = len(m.formFields) - 1
						return m, m.formFields[m.formActiveField].input.Focus()
					}
					return m, nil
				}
				if len(m.formFields) > 0 {
					m.formFields[m.formActiveField].input.Blur()
					if m.formActiveField == 0 {
						// First field → Cancel button
						m.formButton = 1
						return m, nil
					}
					m.formActiveField--
					return m, m.formFields[m.formActiveField].input.Focus()
				}
				return m, nil
			case "enter":
				// Cancel button
				if m.formButton == 1 {
					m.state = stateWorkflows
					m.formFields = nil
					m.formButton = 0
					return m, nil
				}
				// Build button — dispatch
				if m.formButton == 2 {
					ref := ""
					if len(m.formFields) > 0 {
						ref = m.formFields[0].input.Value()
						if m.formFields[0].fieldType == "ref" {
							filter := strings.ToLower(ref)
							switch m.refSection {
							case 1:
								if fb := filterRefs(m.refBranches, filter); len(fb) > 0 {
									idx := m.refBranchIdx
									if idx >= len(fb) {
										idx = len(fb) - 1
									}
									ref = fb[idx]
								}
							case 2:
								if ft := filterRefs(m.refTags, filter); len(ft) > 0 {
									idx := m.refTagIdx
									if idx >= len(ft) {
										idx = len(ft) - 1
									}
									ref = ft[idx]
								}
							}
						}
					}
					if ref == "" {
						ref = m.defaultBranch
						if ref == "" {
							ref = "main"
						}
					}
					inputs := make(map[string]string)
					for _, f := range m.formFields[1:] {
						if val := f.input.Value(); val != "" {
							inputs[f.label] = val
						}
					}
					m.loading = true
					m.statusMsg = "Dispatching workflow…"
					return m, triggerDispatchCmd(m.client, m.selectedWorkflow.ID, ref, inputs)
				}
				// On ref field in list section: select the highlighted item into the input.
				if len(m.formFields) > 0 && m.formActiveField == 0 && m.formFields[0].fieldType == "ref" {
					filter := strings.ToLower(m.formFields[0].input.Value())
					switch m.refSection {
					case 1:
						if fb := filterRefs(m.refBranches, filter); len(fb) > 0 {
							idx := m.refBranchIdx
							if idx >= len(fb) {
								idx = len(fb) - 1
							}
							m.formFields[0].input.SetValue(fb[idx])
							m.formFields[0].input.Placeholder = ""
							m.refSection = 0
						}
					case 2:
						if ft := filterRefs(m.refTags, filter); len(ft) > 0 {
							idx := m.refTagIdx
							if idx >= len(ft) {
								idx = len(ft) - 1
							}
							m.formFields[0].input.SetValue(ft[idx])
							m.formFields[0].input.Placeholder = ""
							m.refSection = 0
						}
					}
				}
				return m, nil
			}

			// When buttons are focused: ←/→ toggle between Cancel and Build.
			if m.formButton != 0 {
				if key == "left" || key == "right" {
					if m.formButton == 1 {
						m.formButton = 2
					} else {
						m.formButton = 1
					}
				}
				return m, nil
			}

			// Handle field-type-specific keys.
			if len(m.formFields) > 0 {
				f := &m.formFields[m.formActiveField]

				// Ref field: ←/→ cycle section; ↑/↓/k/j navigate the active list.
				if f.fieldType == "ref" {
					if key == "left" {
						m.refSection = (m.refSection + 2) % 3
						return m, nil
					}
					if key == "right" {
						m.refSection = (m.refSection + 1) % 3
						return m, nil
					}
					filter := strings.ToLower(f.input.Value())
					if m.refSection == 1 {
						fb := filterRefs(m.refBranches, filter)
						if key == "up" || key == "k" {
							if m.refBranchIdx > 0 {
								m.refBranchIdx--
							}
							return m, nil
						}
						if key == "down" || key == "j" {
							if m.refBranchIdx < len(fb)-1 {
								m.refBranchIdx++
							}
							return m, nil
						}
					}
					if m.refSection == 2 {
						ft := filterRefs(m.refTags, filter)
						if key == "up" || key == "k" {
							if m.refTagIdx > 0 {
								m.refTagIdx--
							}
							return m, nil
						}
						if key == "down" || key == "j" {
							if m.refTagIdx < len(ft)-1 {
								m.refTagIdx++
							}
							return m, nil
						}
					}
					// All other keys update the filter; clamp list indices afterwards.
					var cmd tea.Cmd
					m.formFields[m.formActiveField].input, cmd = m.formFields[m.formActiveField].input.Update(msg)
					newFilter := strings.ToLower(m.formFields[0].input.Value())
					if fb := filterRefs(m.refBranches, newFilter); m.refBranchIdx >= len(fb) {
						m.refBranchIdx = max(0, len(fb)-1)
					}
					if ft := filterRefs(m.refTags, newFilter); m.refTagIdx >= len(ft) {
						m.refTagIdx = max(0, len(ft)-1)
					}
					return m, cmd
				}

				if f.fieldType == "choice" && (key == "up" || key == "k") && len(f.options) > 0 {
					f.optionIdx = (f.optionIdx - 1 + len(f.options)) % len(f.options)
					f.input.SetValue(f.options[f.optionIdx])
					return m, nil
				}
				if f.fieldType == "choice" && (key == "down" || key == "j") && len(f.options) > 0 {
					f.optionIdx = (f.optionIdx + 1) % len(f.options)
					f.input.SetValue(f.options[f.optionIdx])
					return m, nil
				}
				if f.fieldType == "boolean" && (key == "up" || key == "k" || key == "down" || key == "j" || key == " ") {
					if f.input.Value() == "true" {
						f.input.SetValue("false")
					} else {
						f.input.SetValue("true")
					}
					return m, nil
				}
				// Delegate all remaining input to the active textinput.
				var cmd tea.Cmd
				m.formFields[m.formActiveField].input, cmd = m.formFields[m.formActiveField].input.Update(msg)
				return m, cmd
			}
			return m, nil
		}

		switch msg.String() {

		case "ctrl+c":
			return m, tea.Quit

		case "q":
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
						cmds = append(cmds, fetchJobsCmd(m.client, m.selectedRun.ID))
						cmds = append(cmds, logPollCmd())
					} else {
						cmds = append(cmds, fetchLogsCmd(m.client, item.job.ID))
					}
					return m, tea.Batch(cmds...)
				}
			case statePRs:
				if item, ok := m.prsList.SelectedItem().(prItem); ok {
					pr := item.pr
					m.selectedPR = &pr
					m.state = stateRuns
					m.loading = true
					m.statusMsg = ""
					m.runsPolling = true
					return m, tea.Batch(
						fetchRunsForPRCmd(m.client, pr.Head.SHA),
						runsPollCmd(),
					)
				}
			case stateWorkflows:
				if item, ok := m.workflowsList.SelectedItem().(workflowItem); ok {
					m.selectedWorkflow = item.wf
					m.loading = true
					m.statusMsg = ""
					return m, fetchWorkflowInputsCmd(m.client, item.wf)
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
			case stateRuns:
				// If list filter is active, let the list clear it.
				if m.runsList.FilterState() == list.Filtering || m.runsList.FilterState() == list.FilterApplied {
					var cmd tea.Cmd
					m.runsList, cmd = m.runsList.Update(msg)
					return m, cmd
				}
				m.runsPolling = false
				m.statusMsg = ""
				if m.selectedPR != nil {
					m.selectedPR = nil
					m.state = statePRs
				} else {
					m.state = stateMenu
				}
				return m, nil
			case statePRs:
				m.state = stateMenu
				m.statusMsg = ""
				return m, nil
			case stateWorkflows:
				m.state = stateRuns
				m.statusMsg = ""
				return m, nil
			}

		case "d":
			if m.state == stateRuns {
				m.state = stateWorkflows
				m.statusMsg = ""
				cmds = append(cmds, fetchWorkflowsCmd(m.client))
				if m.defaultBranch == "" {
					cmds = append(cmds, fetchDefaultBranchCmd(m.client))
				}
				return m, tea.Batch(cmds...)
			}

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
			case statePRs:
				m.loading = true
				m.statusMsg = ""
				return m, fetchPRsCmd(m.client)
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
			switch m.state {
			case stateRuns:
				m.loading = true
				m.statusMsg = ""
				if m.selectedPR != nil {
					cmds = append(cmds, fetchRunsForPRCmd(m.client, m.selectedPR.Head.SHA))
				} else {
					cmds = append(cmds, fetchRunsCmd(m.client))
				}
				return m, tea.Batch(cmds...)
			case statePRs:
				m.loading = true
				m.statusMsg = ""
				return m, fetchPRsCmd(m.client)
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
			case stateRuns:
				if item, ok := m.runsList.SelectedItem().(runItem); ok {
					if item.run.HTMLURL != "" {
						if err := OpenInBrowser(item.run.HTMLURL); err != nil {
							m.statusMsg = fmt.Sprintf("error opening browser: %v", err)
						} else {
							m.statusMsg = "✓ Opened run in browser"
						}
					}
				}
				return m, nil
			case statePRs:
				if item, ok := m.prsList.SelectedItem().(prItem); ok {
					if item.pr.HTMLURL != "" {
						if err := OpenInBrowser(item.pr.HTMLURL); err != nil {
							m.statusMsg = fmt.Sprintf("error opening browser: %v", err)
						} else {
							m.statusMsg = "✓ Opened PR in browser"
						}
					}
				}
				return m, nil
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

	case prsLoadedMsg:
		m.loading = false
		items := make([]list.Item, len(msg))
		for i, pr := range msg {
			items[i] = prItem{pr}
		}
		cmds = append(cmds, m.prsList.SetItems(items))

	case workflowsLoadedMsg:
		m.loading = false
		items := make([]list.Item, len(msg))
		for i, wf := range msg {
			items[i] = workflowItem{wf}
		}
		cmds = append(cmds, m.workflowsList.SetItems(items))

	case workflowInputsMsg:
		ref := m.defaultBranch
		if ref == "" {
			ref = "main"
		}
		m.formFields = buildDispatchFormFields([]WorkflowInput(msg), ref)
		m.formActiveField = 0
		m.formButton = 0
		m.refBranches = nil
		m.refTags = nil
		m.refSection = 0
		m.refBranchIdx = 0
		m.refTagIdx = 0
		m.state = stateDispatchForm
		m.loading = false
		if len(m.formFields) > 0 {
			blinkCmd := m.formFields[0].input.Focus()
			cmds = append(cmds, blinkCmd)
		}
		cmds = append(cmds, fetchRefOptionsCmd(m.client))

	case refOptionsMsg:
		m.refBranches = msg.branches
		m.refTags = msg.tags
		// Pre-select: find the default branch in the list and highlight it.
		// Clear the textinput so it acts as an empty filter (showing all refs).
		// Keep the branch name as a placeholder so the user still sees the default.
		if len(m.formFields) > 0 {
			val := m.formFields[0].input.Value()
			for i, b := range m.refBranches {
				if b == val {
					m.refSection = 1
					m.refBranchIdx = i
					m.formFields[0].input.Placeholder = val
					m.formFields[0].input.SetValue("")
					break
				}
			}
		}

	case dispatchTriggeredMsg:
		m.loading = false
		m.statusMsg = string(msg)
		m.state = stateRuns
		m.formFields = nil
		// Refresh runs after a short moment (dispatch takes time to appear)
		cmds = append(cmds, fetchRunsCmd(m.client))

	case defaultBranchMsg:
		m.defaultBranch = string(msg)

	case jobsLoadedMsg:
		m.loading = false

		if m.jobsPollStartIDs != nil {
			hasNew := false
			for _, j := range msg {
				if !m.jobsPollStartIDs[j.ID] {
					hasNew = true
					break
				}
			}
			if !hasNew {
				break
			}
			m.jobsPollStartIDs = nil
		}

		runID := m.selectedRun.ID
		oldJobs := m.lastJobsForRun[runID]

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

		m.lastJobsForRun[runID] = msg

		if m.state == stateLogs {
			for _, j := range msg {
				if j.ID == m.selectedJob.ID {
					wasRunning := isRunning(m.selectedJob.Status)
					m.selectedJob = j
					isNowDone := wasRunning && !isRunning(m.selectedJob.Status)
					if isNowDone {
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
				cmds = append(cmds, fetchJobsCmd(m.client, m.selectedRun.ID))
				cmds = append(cmds, logPollCmd())
			} else {
				cmds = append(cmds, fetchLogsCmd(m.client, m.selectedJob.ID))
			}
		}

	case rerunMsg:
		m.statusMsg = msg.message
		m.jobsPollStartIDs = make(map[int64]bool)
		for _, item := range m.jobsList.Items() {
			if ji, ok := item.(jobItem); ok {
				m.jobsPollStartIDs[ji.job.ID] = true
			}
		}
		if m.state == stateJobs {
			cmds = append(cmds, m.jobsList.SetItems([]list.Item{}))
		}
		if !m.jobsPolling {
			m.jobsPolling = true
			cmds = append(cmds, jobsPollCmd())
		}

	case jobsPollTickMsg:
		if m.jobsPolling {
			if m.state == stateJobs {
				cmds = append(cmds, fetchJobsCmd(m.client, m.selectedRun.ID))
			}
			cmds = append(cmds, jobsPollCmd())
		}

	case runsPollTickMsg:
		if m.runsPolling {
			if m.selectedPR != nil {
				cmds = append(cmds, fetchRunsForPRCmd(m.client, m.selectedPR.Head.SHA))
			} else {
				cmds = append(cmds, fetchRunsCmd(m.client))
			}
			cmds = append(cmds, runsPollCmd())
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

	// Delegate remaining messages to the active list/viewport.
	switch m.state {
	case stateRuns:
		var cmd tea.Cmd
		m.runsList, cmd = m.runsList.Update(msg)
		cmds = append(cmds, cmd)
	case stateJobs:
		var cmd tea.Cmd
		m.jobsList, cmd = m.jobsList.Update(msg)
		cmds = append(cmds, cmd)
	case statePRs:
		var cmd tea.Cmd
		m.prsList, cmd = m.prsList.Update(msg)
		cmds = append(cmds, cmd)
	case stateWorkflows:
		var cmd tea.Cmd
		m.workflowsList, cmd = m.workflowsList.Update(msg)
		cmds = append(cmds, cmd)
	case stateDispatchForm:
		// Forward non-key messages (e.g. cursor blink) to the active textinput.
		if len(m.formFields) > 0 {
			var cmd tea.Cmd
			m.formFields[m.formActiveField].input, cmd = m.formFields[m.formActiveField].input.Update(msg)
			cmds = append(cmds, cmd)
		}
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
