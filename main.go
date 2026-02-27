package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// viewState is the current screen shown to the user.
type viewState int

const (
	stateMenu          viewState = iota // main menu
	stateRuns                           // list of workflow runs
	stateJobs                           // jobs for a selected run
	stateLogs                           // live log viewer for a selected job
	statePRs                            // list of open pull requests
	stateWorkflows                      // workflow dispatch picker
	stateDispatchForm                   // form to fill inputs before dispatching
)

// model is the root Bubble Tea model.
type model struct {
	state         viewState
	width, height int
	client        *GitHubClient

	// stateMenu
	menuIndex int

	// stateRuns
	runsList    list.Model
	runsPolling bool

	// stateJobs
	selectedRun      WorkflowRun
	jobsList         list.Model
	jobsPolling      bool
	jobsPollStartIDs map[int64]bool

	// stateLogs
	selectedJob   Job
	logViewport   viewport.Model
	logContent    string // rendered content with styling
	logRaw        string // raw log content (unrendered)
	logLoaded     bool
	autoScroll    bool
	lastLogLength int // track log size to detect incremental updates

	// live streaming (running jobs)
	liveStreaming      bool
	liveChangeID       int
	liveLogs           string
	liveFailedAttempts int

	// blob range polling (fallback for running jobs)
	logBlobURL    string
	logBlobOffset int64

	// GHES per-step log fetching
	pipelineInfo    *pipelineServiceInfo
	stepLogsFetched int

	// log filter
	logFilter     string
	logFilterMode bool

	// statePRs
	prsList    list.Model
	selectedPR *PullRequest // non-nil when viewing runs for a specific PR

	// stateWorkflows
	workflowsList list.Model
	defaultBranch string

	// stateDispatchForm
	selectedWorkflow Workflow
	formFields       []formField
	formActiveField  int
	formButton       int      // 0=field focused, 1=Cancel focused, 2=Build focused
	refBranches      []string // all branch names (from API)
	refTags          []string // all tag names (from API)
	refSection       int      // 0=input, 1=branches, 2=tags
	refBranchIdx     int      // selected index in filtered branch list
	refTagIdx        int      // selected index in filtered tag list

	// shared
	spinner        spinner.Model
	loading        bool
	statusMsg      string
	err            error
	lastJobsForRun map[int64][]Job
}

// ─── List item types ──────────────────────────────────────────────────────────

type runItem struct{ run WorkflowRun }

func (r runItem) FilterValue() string { return r.run.Name + " " + r.run.HeadBranch }

type jobItem struct{ job Job }

func (j jobItem) FilterValue() string { return j.job.Name }

type prItem struct{ pr PullRequest }

func (p prItem) FilterValue() string { return fmt.Sprintf("#%d %s", p.pr.Number, p.pr.Title) }

type workflowItem struct{ wf Workflow }

func (w workflowItem) FilterValue() string { return w.wf.Name }

// formField holds one field in the workflow dispatch form.
type formField struct {
	label       string
	description string
	fieldType   string   // "ref", "string", "boolean", "choice", "environment"
	options     []string // for "choice" type
	required    bool
	optionIdx   int // current selected index for choice/boolean cycling
	input       textinput.Model
}

// ─── Custom delegates (k9s-style single-line table rows) ─────────────────────

type runDelegate struct{ width int }

func (d runDelegate) Height() int                             { return 1 }
func (d runDelegate) Spacing() int                           { return 0 }
func (d runDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d runDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ri, ok := item.(runItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	if selected {
		row := formatRunRowPlain(ri.run, d.width)
		visWidth := lipgloss.Width(row)
		if visWidth < d.width {
			row = row + strings.Repeat(" ", d.width-visWidth)
		}
		style := lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("15")).
			Bold(true)
		fmt.Fprint(w, style.Render(row))
	} else {
		fmt.Fprint(w, normalItemStyle.Render(formatRunRow(ri.run, d.width, false)))
	}
}

type jobDelegate struct{ width int }

func (d jobDelegate) Height() int                             { return 1 }
func (d jobDelegate) Spacing() int                           { return 0 }
func (d jobDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d jobDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ji, ok := item.(jobItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	if selected {
		row := formatJobRowPlain(ji.job, d.width)
		visWidth := lipgloss.Width(row)
		if visWidth < d.width {
			row = row + strings.Repeat(" ", d.width-visWidth)
		}
		style := lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("15")).
			Bold(true)
		fmt.Fprint(w, style.Render(row))
	} else {
		fmt.Fprint(w, normalItemStyle.Render(formatJobRow(ji.job, d.width, false)))
	}
}

type prDelegate struct{ width int }

func (d prDelegate) Height() int                             { return 1 }
func (d prDelegate) Spacing() int                           { return 0 }
func (d prDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d prDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pi, ok := item.(prItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	if selected {
		row := formatPRRowPlain(pi.pr, d.width)
		visWidth := lipgloss.Width(row)
		if visWidth < d.width {
			row = row + strings.Repeat(" ", d.width-visWidth)
		}
		style := lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("15")).
			Bold(true)
		fmt.Fprint(w, style.Render(row))
	} else {
		fmt.Fprint(w, normalItemStyle.Render(formatPRRow(pi.pr, d.width)))
	}
}

type workflowDelegate struct{ width int }

func (d workflowDelegate) Height() int                             { return 1 }
func (d workflowDelegate) Spacing() int                           { return 0 }
func (d workflowDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d workflowDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	wi, ok := item.(workflowItem)
	if !ok {
		return
	}
	selected := index == m.Index()
	if selected {
		row := formatWorkflowRowPlain(wi.wf, d.width)
		visWidth := lipgloss.Width(row)
		if visWidth < d.width {
			row = row + strings.Repeat(" ", d.width-visWidth)
		}
		style := lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("15")).
			Bold(true)
		fmt.Fprint(w, style.Render(row))
	} else {
		fmt.Fprint(w, normalItemStyle.Render(formatWorkflowRow(wi.wf, d.width)))
	}
}

// ─── Row formatters ───────────────────────────────────────────────────────────

func formatRunRow(r WorkflowRun, width int, selected bool) string {
	const (
		cursorW = 2
		iconW   = 2
		branchW = 22
		eventW  = 11
		ageW    = 8
		gaps    = 4
	)
	nameW := max(8, width-cursorW-iconW-branchW-eventW-ageW-gaps)

	cursor := "  "
	if selected {
		cursor = "▶ "
	}
	icon := statusIcon(r.Status, r.Conclusion)
	name := truncate(r.Name, nameW)
	branch := truncate(r.HeadBranch, branchW)
	event := truncate(r.Event, eventW)
	age := relativeTime(r.CreatedAt)

	return cursor + " " + icon + " " + padRight(name, nameW) + " " + padRight(branch, branchW) + " " + padRight(event, eventW) + " " + padRight(age, ageW)
}

func formatRunRowPlain(r WorkflowRun, width int) string {
	const (
		cursorW = 2
		iconW   = 2
		branchW = 22
		eventW  = 11
		ageW    = 8
		gaps    = 4
	)
	nameW := max(8, width-cursorW-iconW-branchW-eventW-ageW-gaps)

	icon := getPlainStatusIcon(r.Status, r.Conclusion)
	name := truncate(r.Name, nameW)
	branch := truncate(r.HeadBranch, branchW)
	event := truncate(r.Event, eventW)
	age := relativeTime(r.CreatedAt)

	return "▶  " + icon + " " + padRight(name, nameW) + " " + padRight(branch, branchW) + " " + padRight(event, eventW) + " " + padRight(age, ageW)
}

func formatJobRow(j Job, width int, selected bool) string {
	const (
		cursorW   = 2
		iconW     = 2
		statusW   = 14
		durationW = 10
		gaps      = 3
	)
	nameW := max(8, width-cursorW-iconW-statusW-durationW-gaps)

	cursor := "  "
	if selected {
		cursor = "▶ "
	}
	icon := statusIcon(j.Status, j.Conclusion)
	name := truncate(j.Name, nameW)
	status := truncate(statusLabel(j.Status, j.Conclusion), statusW)

	dur := ""
	if !j.StartedAt.IsZero() {
		end := j.CompletedAt
		if end.IsZero() {
			end = time.Now()
		}
		dur = end.Sub(j.StartedAt).Round(time.Second).String()
	}
	duration := truncate(dur, durationW)

	return cursor + " " + icon + " " + padRight(name, nameW) + " " + padRight(status, statusW) + " " + padRight(duration, durationW)
}

func formatJobRowPlain(j Job, width int) string {
	const (
		cursorW   = 2
		iconW     = 2
		statusW   = 14
		durationW = 10
		gaps      = 3
	)
	nameW := max(8, width-cursorW-iconW-statusW-durationW-gaps)

	icon := getPlainStatusIcon(j.Status, j.Conclusion)
	name := truncate(j.Name, nameW)
	status := truncate(statusLabel(j.Status, j.Conclusion), statusW)

	dur := ""
	if !j.StartedAt.IsZero() {
		end := j.CompletedAt
		if end.IsZero() {
			end = time.Now()
		}
		dur = end.Sub(j.StartedAt).Round(time.Second).String()
	}
	duration := truncate(dur, durationW)

	return "▶  " + icon + " " + padRight(name, nameW) + " " + padRight(status, statusW) + " " + padRight(duration, durationW)
}

func formatPRRow(pr PullRequest, width int) string {
	const (
		cursorW = 3
		numW    = 6
		branchW = 18
		authorW = 14
		ageW    = 8
		gaps    = 4
	)
	titleW := max(8, width-cursorW-numW-branchW-authorW-ageW-gaps)

	num := truncate(fmt.Sprintf("#%d", pr.Number), numW)
	title := truncate(pr.Title, titleW)
	branch := truncate(pr.Head.Ref, branchW)
	author := truncate(pr.User.Login, authorW)
	age := relativeTime(pr.UpdatedAt)

	return "    " + padRight(num, numW) + " " + padRight(title, titleW) + " " + padRight(branch, branchW) + " " + padRight(author, authorW) + " " + padRight(age, ageW)
}

func formatPRRowPlain(pr PullRequest, width int) string {
	const (
		cursorW = 3
		numW    = 6
		branchW = 18
		authorW = 14
		ageW    = 8
		gaps    = 4
	)
	titleW := max(8, width-cursorW-numW-branchW-authorW-ageW-gaps)

	num := truncate(fmt.Sprintf("#%d", pr.Number), numW)
	title := truncate(pr.Title, titleW)
	branch := truncate(pr.Head.Ref, branchW)
	author := truncate(pr.User.Login, authorW)
	age := relativeTime(pr.UpdatedAt)

	return "▶   " + padRight(num, numW) + " " + padRight(title, titleW) + " " + padRight(branch, branchW) + " " + padRight(author, authorW) + " " + padRight(age, ageW)
}

func formatWorkflowRow(wf Workflow, width int) string {
	const (
		cursorW = 3
		fileW   = 30
		gaps    = 1
	)
	nameW := max(8, width-cursorW-fileW-gaps)

	filename := wf.Path
	if idx := strings.LastIndex(filename, "/"); idx >= 0 {
		filename = filename[idx+1:]
	}

	return "    " + padRight(truncate(filename, fileW), fileW) + " " + truncate(wf.Name, nameW)
}

func formatWorkflowRowPlain(wf Workflow, width int) string {
	const (
		cursorW = 3
		fileW   = 30
		gaps    = 1
	)
	nameW := max(8, width-cursorW-fileW-gaps)

	filename := wf.Path
	if idx := strings.LastIndex(filename, "/"); idx >= 0 {
		filename = filename[idx+1:]
	}

	return "▶   " + padRight(truncate(filename, fileW), fileW) + " " + truncate(wf.Name, nameW)
}

// ─── Utilities ────────────────────────────────────────────────────────────────

// padRight pads the string with spaces on the right to reach length n.
func padRight(s string, n int) string {
	vis := lipgloss.Width(s)
	if vis < n {
		s += strings.Repeat(" ", n-vis)
	}
	return s
}

// padToWidth right-pads s with spaces so its visible width equals n.
func padToWidth(s string, n int) string {
	vis := lipgloss.Width(s)
	if vis < n {
		s += strings.Repeat(" ", n-vis)
	}
	return s
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// filterRefs returns the subset of refs whose name contains the lower-cased filter string.
// Returns the original slice unchanged when filter is empty.
func filterRefs(refs []string, lower string) []string {
	if lower == "" {
		return refs
	}
	var out []string
	for _, r := range refs {
		if strings.Contains(strings.ToLower(r), lower) {
			out = append(out, r)
		}
	}
	return out
}

// buildDispatchFormFields constructs the form fields for a workflow dispatch form.
// Field 0 is always the ref/branch/tag field; subsequent fields correspond to
// the workflow's workflow_dispatch inputs in the order they appear in the YAML.
func buildDispatchFormFields(inputs []WorkflowInput, defaultRef string) []formField {
	newInput := func(value string) textinput.Model {
		ti := textinput.New()
		ti.Width = 60
		ti.Prompt = "> "
		ti.SetValue(value)
		return ti
	}

	refInput := newInput(defaultRef)
	fields := []formField{
		{
			label:     "ref / branch / tag",
			fieldType: "ref",
			input:     refInput,
		},
	}

	for _, inp := range inputs {
		ft := inp.Type
		if ft == "" {
			ft = "string"
		}
		f := formField{
			label:       inp.Name,
			description: inp.Description,
			fieldType:   ft,
			options:     inp.Options,
			required:    inp.Required,
		}
		switch ft {
		case "boolean":
			val := "false"
			if inp.Default == "true" {
				val = "true"
			}
			f.input = newInput(val)
		case "choice":
			for i, opt := range inp.Options {
				if opt == inp.Default {
					f.optionIdx = i
					break
				}
			}
			val := inp.Default
			if val == "" && len(inp.Options) > 0 {
				val = inp.Options[0]
			}
			f.input = newInput(val)
		default:
			f.input = newInput(inp.Default)
		}
		fields = append(fields, f)
	}
	return fields
}

// ─── Entry point ──────────────────────────────────────────────────────────────

func main() {
	var repoPath string
	var debugFile string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help", "help":
			fmt.Println("Usage: tgh [REPO_PATH] [--debug <filename>]")
			fmt.Println()
			fmt.Println("tgh is a terminal UI for browsing GitHub Actions job logs")
			fmt.Println()
			fmt.Println("Arguments:")
			fmt.Println("  REPO_PATH          Optional path to a git repository")
			fmt.Println("  --debug <filename> Write debug log to the given file")
			fmt.Println()
			fmt.Println("Examples:")
			fmt.Println("  tgh                         # Run in current directory")
			fmt.Println("  tgh /path/to/repo           # Run in specified directory")
			fmt.Println("  tgh --debug /tmp/tgh.log    # Run with debug logging")
			os.Exit(0)
		case "--debug":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --debug requires a filename argument")
				os.Exit(1)
			}
			i++
			debugFile = args[i]
		default:
			repoPath = arg
		}
	}

	initDebugLog(debugFile)

	client, err := NewGitHubClient(repoPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorAmber)

	rdel := runDelegate{width: 80}
	runsList := list.New([]list.Item{}, rdel, 80, 20)
	runsList.SetShowTitle(false)
	runsList.SetShowStatusBar(false)
	runsList.SetShowPagination(false)
	runsList.SetFilteringEnabled(true)
	runsList.DisableQuitKeybindings()

	jdel := jobDelegate{width: 80}
	jobsList := list.New([]list.Item{}, jdel, 80, 20)
	jobsList.SetShowTitle(false)
	jobsList.SetShowStatusBar(false)
	jobsList.SetShowPagination(false)
	jobsList.SetFilteringEnabled(false)
	jobsList.DisableQuitKeybindings()

	pdel := prDelegate{width: 80}
	prsList := list.New([]list.Item{}, pdel, 80, 20)
	prsList.SetShowTitle(false)
	prsList.SetShowStatusBar(false)
	prsList.SetShowPagination(false)
	prsList.SetFilteringEnabled(false)
	prsList.DisableQuitKeybindings()

	wdel := workflowDelegate{width: 80}
	workflowsList := list.New([]list.Item{}, wdel, 80, 20)
	workflowsList.SetShowTitle(false)
	workflowsList.SetShowStatusBar(false)
	workflowsList.SetShowPagination(false)
	workflowsList.SetFilteringEnabled(false)
	workflowsList.DisableQuitKeybindings()

	vp := viewport.New(80, 20)

	m := model{
		state:          stateMenu,
		client:         client,
		runsList:       runsList,
		jobsList:       jobsList,
		prsList:        prsList,
		workflowsList:  workflowsList,
		logViewport:    vp,
		spinner:        s,
		autoScroll:     true,
		lastJobsForRun: make(map[int64][]Job),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
