package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// viewState is the current screen shown to the user.
type viewState int

const (
	stateRuns viewState = iota // list of workflow runs
	stateJobs                  // jobs for a selected run
	stateLogs                  // live log viewer for a selected job
)

// model is the root Bubble Tea model.
type model struct {
	state         viewState
	width, height int
	client        *GitHubClient

	// stateRuns
	runsList list.Model

	// stateJobs
	selectedRun WorkflowRun
	jobsList    list.Model

	// stateLogs
	selectedJob Job
	logViewport viewport.Model
	logContent  string         // rendered content with styling and selection
	logRaw      string         // raw log content (unrendered)
	logLoaded   bool
	autoScroll  bool
	selectedLogLine int // 0-based index of highlighted line

	// shared
	spinner   spinner.Model
	loading   bool
	statusMsg string
	err       error
	lastJobsForRun map[int64][]Job // track previous jobs to detect new ones on rerun
}

// ─── List item types ──────────────────────────────────────────────────────────

type runItem struct{ run WorkflowRun }

func (r runItem) FilterValue() string { return r.run.Name + " " + r.run.HeadBranch }

type jobItem struct{ job Job }

func (j jobItem) FilterValue() string { return j.job.Name }

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
		// For selected rows, render as plain text first, then apply background
		row := formatRunRowPlain(ri.run, d.width)
		// Pad to full width
		visWidth := lipgloss.Width(row)
		if visWidth < d.width {
			row = row + strings.Repeat(" ", d.width-visWidth)
		}
		// Apply background style
		style := lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("15")).
			Bold(true)
		row = style.Render(row)
		fmt.Fprint(w, row)
	} else {
		// For unselected rows, use styled version
		row := formatRunRow(ri.run, d.width, false)
		fmt.Fprint(w, normalItemStyle.Render(row))
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
		// For selected rows, render as plain text first, then apply background
		row := formatJobRowPlain(ji.job, d.width)
		// Pad to full width
		visWidth := lipgloss.Width(row)
		if visWidth < d.width {
			row = row + strings.Repeat(" ", d.width-visWidth)
		}
		// Apply background style
		style := lipgloss.NewStyle().
			Background(lipgloss.Color("63")).
			Foreground(lipgloss.Color("15")).
			Bold(true)
		row = style.Render(row)
		fmt.Fprint(w, row)
	} else {
		// For unselected rows, use styled version
		row := formatJobRow(ji.job, d.width, false)
		fmt.Fprint(w, normalItemStyle.Render(row))
	}
}

// ─── Row formatters ───────────────────────────────────────────────────────────

func formatRunRow(r WorkflowRun, width int, selected bool) string {
	const (
		cursorW = 2  // "▶ " or "  "
		iconW   = 2
		branchW = 22
		eventW  = 11
		ageW    = 8
		gaps    = 4
	)
	nameW := max(8, width-cursorW-iconW-branchW-eventW-ageW-gaps)

	cursor := "  "
	if selected {
		cursor = "▶ " // Don't style here - let delegate handle the full line styling
	}
	icon := statusIcon(r.Status, r.Conclusion)
	name := truncate(r.Name, nameW)
	branch := truncate(r.HeadBranch, branchW)
	event := truncate(r.Event, eventW)
	age := relativeTime(r.CreatedAt)

	// Build row without inner widths - just raw text
	row := cursor + " " + icon + " " + padRight(name, nameW) + " " + padRight(branch, branchW) + " " + padRight(event, eventW) + " " + padRight(age, ageW)
	
	return row
}

// formatRunRowPlain builds a run row as plain text with no styling
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

	cursor := "▶ "
	// Get plain icon without styling
	icon := getPlainStatusIcon(r.Status, r.Conclusion)
	name := truncate(r.Name, nameW)
	branch := truncate(r.HeadBranch, branchW)
	event := truncate(r.Event, eventW)
	age := relativeTime(r.CreatedAt)

	row := cursor + " " + icon + " " + padRight(name, nameW) + " " + padRight(branch, branchW) + " " + padRight(event, eventW) + " " + padRight(age, ageW)
	return row
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
		cursor = "▶ " // Don't style here - let delegate handle the full line styling
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

	// Build row without inner widths - just raw text
	row := cursor + " " + icon + " " + padRight(name, nameW) + " " + padRight(status, statusW) + " " + padRight(duration, durationW)
	
	return row
}

// formatJobRowPlain builds a job row as plain text with no styling
func formatJobRowPlain(j Job, width int) string {
	const (
		cursorW   = 2
		iconW     = 2
		statusW   = 14
		durationW = 10
		gaps      = 3
	)
	nameW := max(8, width-cursorW-iconW-statusW-durationW-gaps)

	cursor := "▶ "
	// Get plain icon without styling
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

	row := cursor + " " + icon + " " + padRight(name, nameW) + " " + padRight(status, statusW) + " " + padRight(duration, durationW)
	return row
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
// It measures s using lipgloss (ANSI-aware), so embedded colour codes are ignored.
func padToWidth(s string, n int) string {
	vis := lipgloss.Width(s)
	if vis < n {
		s += strings.Repeat(" ", n-vis)
	}
	return s
}

func truncate(s string, n int) string {
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

// ─── Entry point ──────────────────────────────────────────────────────────────

func main() {
	// Parse command-line arguments
	var repoPath string
	
	if len(os.Args) > 1 {
		arg := os.Args[1]
		// Handle help flags
		if arg == "-h" || arg == "--help" || arg == "help" {
			fmt.Println("Usage: tgh [REPO_PATH]")
			fmt.Println()
			fmt.Println("tgh is a terminal UI for browsing GitHub Actions job logs")
			fmt.Println()
			fmt.Println("Arguments:")
			fmt.Println("  REPO_PATH  Optional path to a git repository")
			fmt.Println()
			fmt.Println("Examples:")
			fmt.Println("  tgh                    # Run in current directory")
			fmt.Println("  tgh /path/to/repo      # Run in specified directory")
			fmt.Println("  tgh ../my-project      # Run in relative path")
			os.Exit(0)
		}
		repoPath = arg
	}

	// Create GitHub client with optional repo path
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

	vp := viewport.New(80, 20)

	m := model{
		state:           stateRuns,
		client:          client,
		runsList:        runsList,
		jobsList:        jobsList,
		logViewport:     vp,
		spinner:         s,
		loading:         true,
		autoScroll:      true,
		lastJobsForRun:  make(map[int64][]Job),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
