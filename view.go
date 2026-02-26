package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ─── Top-level View dispatcher ────────────────────────────────────────────────

func (m model) View() string {
	if m.width == 0 {
		return "" // not yet initialized
	}

	switch m.state {
	case stateRuns:
		return m.viewRuns()
	case stateJobs:
		return m.viewJobs()
	case stateLogs:
		return m.viewLogs()
	}
	return ""
}

// ─── Shared header / footer ───────────────────────────────────────────────────

// renderAppBar produces the top cyan bar (k9s-style) showing the app name and repo.
func (m model) renderAppBar(viewName string) string {
	left := appNameStyle.Render("tgh")
	right := " " + m.client.owner + "/" + m.client.repo + " "

	usedWidth := lipgloss.Width(left) + lipgloss.Width(viewName) + lipgloss.Width(right)
	gap := max(0, m.width-usedWidth)

	bar := left + " " + viewName + strings.Repeat(" ", gap) + right
	return headerBarStyle.Width(m.width).Render(bar)
}

// renderFooter renders the bottom key-hints bar.
func renderFooter(hints []string) string {
	parts := make([]string, len(hints))
	for i, h := range hints {
		// hints look like "<key> action" — colour the <key> part
		if strings.HasPrefix(h, "<") {
			end := strings.Index(h, ">")
			if end > 0 {
				key := keyStyle.Render(h[:end+1])
				rest := styleDim.Render(h[end+1:])
				parts[i] = key + rest
				continue
			}
		}
		parts[i] = styleDim.Render(h)
	}
	return footerStyle.Render(" " + strings.Join(parts, styleDim.Render("  ")))
}

// ─── Runs view ────────────────────────────────────────────────────────────────

func (m model) viewRuns() string {
	// Line 1: app bar
	var viewLabel string
	if m.loading && len(m.runsList.Items()) == 0 {
		viewLabel = m.spinner.View() + " Loading runs…"
	} else {
		viewLabel = fmt.Sprintf("Runs [%d]", len(m.runsList.Items()))
	}
	appBar := m.renderAppBar(viewLabel)

	// Line 2: breadcrumb / status message
	breadcrumb := ""
	if m.statusMsg != "" {
		breadcrumb = styleDim.Width(m.width).Render(" " + m.statusMsg)
	} else {
		breadcrumb = breadcrumbDimStyle.Width(m.width).Render(" GitHub Actions › Runs")
	}

	// Line 3: column headers
	colHeaders := m.runColHeaders()

	// Middle: list
	listView := m.runsList.View()

	// Footer
	footer := renderFooter([]string{
		"<enter> open",
		"<r> rerun-failed",
		"<R> rerun-all",
		"<tab> refresh",
		"<q> quit",
	})

	return lipgloss.JoinVertical(lipgloss.Left,
		appBar,
		breadcrumb,
		colHeaders,
		listView,
		footer,
	)
}

func (m model) runColHeaders() string {
	const (
		cursorW = 2
		iconW   = 2
		branchW = 22
		eventW  = 11
		ageW    = 8
		gaps    = 4
	)
	nameW := max(8, m.width-cursorW-iconW-branchW-eventW-ageW-gaps)

	cursor := lipgloss.NewStyle().Width(cursorW).Render("")
	icon := lipgloss.NewStyle().Width(iconW + 1).Render("")
	name := lipgloss.NewStyle().Width(nameW).Render("NAME")
	branch := lipgloss.NewStyle().Width(branchW).Render("BRANCH")
	event := lipgloss.NewStyle().Width(eventW).Render("EVENT")
	age := lipgloss.NewStyle().Width(ageW).Render("AGE")

	return colHeaderStyle.Render(cursor + icon + name + " " + branch + " " + event + " " + age)
}

// ─── Jobs view ────────────────────────────────────────────────────────────────

func (m model) viewJobs() string {
	// Line 1: app bar
	var viewLabel string
	if m.loading && len(m.jobsList.Items()) == 0 {
		viewLabel = m.spinner.View() + " Loading jobs…"
	} else {
		viewLabel = fmt.Sprintf("Jobs [%d]", len(m.jobsList.Items()))
	}
	appBar := m.renderAppBar(viewLabel)

	// Line 2: breadcrumb
	breadcrumb := ""
	if m.statusMsg != "" {
		breadcrumb = styleDim.Width(m.width).Render(" " + m.statusMsg)
	} else {
		runLabel := truncate(m.selectedRun.Name, m.width-30)
		breadcrumb = breadcrumbDimStyle.Width(m.width).Render(
			" GitHub Actions › Runs › " + runLabel,
		)
	}

	// Line 3: column headers
	colHeaders := m.jobColHeaders()

	// Middle: list
	listView := m.jobsList.View()

	// Footer
	footer := renderFooter([]string{
		"<enter> logs",
		"<o> open",
		"<r> rerun-failed",
		"<R> rerun-all",
		"<esc/b> back",
		"<q> quit",
	})

	return lipgloss.JoinVertical(lipgloss.Left,
		appBar,
		breadcrumb,
		colHeaders,
		listView,
		footer,
	)
}

func (m model) jobColHeaders() string {
	const (
		cursorW   = 2
		iconW     = 2
		statusW   = 14
		durationW = 10
		gaps      = 3
	)
	nameW := max(8, m.width-cursorW-iconW-statusW-durationW-gaps)

	cursor := lipgloss.NewStyle().Width(cursorW).Render("")
	icon := lipgloss.NewStyle().Width(iconW + 1).Render("")
	name := lipgloss.NewStyle().Width(nameW).Render("NAME")
	status := lipgloss.NewStyle().Width(statusW).Render("STATUS")
	duration := lipgloss.NewStyle().Width(durationW).Render("DURATION")

	return colHeaderStyle.Render(cursor + icon + name + " " + status + " " + duration)
}

// ─── Logs view ────────────────────────────────────────────────────────────────

// renderStepsContent renders the step list for a running job.
func (m model) renderStepsContent() string {
	steps := m.selectedJob.Steps
	if len(steps) == 0 {
		return "\n " + m.spinner.View() + " Waiting for steps…"
	}

	nameW := max(4, m.width-3) // 1(margin) + 1(icon) + 1(space)

	var lines []string
	for _, s := range steps {
		var icon string
		if s.Status == "in_progress" {
			icon = m.spinner.View()
		} else {
			icon = statusIcon(s.Status, s.Conclusion)
		}

		var line string
		switch {
		case s.Status == "in_progress":
			elapsed := ""
			if !s.StartedAt.IsZero() {
				elapsed = " " + styleDim.Render("("+time.Since(s.StartedAt).Round(time.Second).String()+")")
			}
			label := styleHeader.Render(truncate(s.Name, nameW)) + elapsed
			line = " " + icon + " " + label
		case s.Status == "completed" && s.Conclusion == "failure":
			line = " " + icon + " " + styleError.Render(truncate(s.Name, nameW))
		case s.Status == "completed":
			line = " " + icon + " " + truncate(s.Name, nameW)
		default: // queued / pending / skipped
			line = " " + icon + " " + styleDim.Render(truncate(s.Name, nameW))
		}
		lines = append(lines, line)
	}

	// Pad to fill the viewport height so the footer stays at the bottom.
	text := strings.Join(lines, "\n")
	lineCount := strings.Count(text, "\n") + 1
	if pad := m.logViewport.Height - lineCount; pad > 0 {
		text += strings.Repeat("\n", pad)
	}
	return text
}

func (m model) viewLogs() string {
	// Line 1: app bar with job name and step progress
	jobLabel := truncate(m.selectedJob.Name, m.width-40)
	var progressSuffix string
	if isRunning(m.selectedJob.Status) {
		total := len(m.selectedJob.Steps)
		done := countCompletedSteps(m.selectedJob.Steps)
		if total > 0 {
			progressSuffix = fmt.Sprintf("  %d/%d steps", done, total)
		}
	} else {
		// Compact dot summary for completed jobs
		var dots strings.Builder
		for _, s := range m.selectedJob.Steps {
			switch {
			case s.Status == "completed" && s.Conclusion == "success":
				dots.WriteString(statusSuccess.Render("●"))
			case s.Status == "completed" && (s.Conclusion == "failure" || s.Conclusion == "cancelled"):
				dots.WriteString(statusFailure.Render("●"))
			case s.Status == "completed":
				dots.WriteString(statusNeutral.Render("●"))
			default:
				dots.WriteString(styleDim.Render("○"))
			}
		}
		if dots.Len() > 0 {
			progressSuffix = "  " + dots.String()
		}
	}
	appBar := m.renderAppBar("Logs › " + jobLabel + progressSuffix)

	// Line 2: job status + current step name (running) or scroll/filter hints (done)
	icon := statusIcon(m.selectedJob.Status, m.selectedJob.Conclusion)
	label := statusLabel(m.selectedJob.Status, m.selectedJob.Conclusion)
	extras := ""
	if isRunning(m.selectedJob.Status) {
		for _, s := range m.selectedJob.Steps {
			if s.Status == "in_progress" {
				dur := ""
				if !s.StartedAt.IsZero() {
					dur = " (" + time.Since(s.StartedAt).Round(time.Second).String() + ")"
				}
				extras = "  " + styleDim.Render("▶ "+s.Name+dur)
				break
			}
		}
	} else {
		if m.autoScroll {
			extras += "  " + styleAccent.Render("[auto-scroll]")
		}
		if m.logFilter != "" {
			extras += "  " + styleAccent.Render("[filter: "+m.logFilter+"]")
		}
	}
	if m.statusMsg != "" {
		extras += "  " + styleAccent.Render(m.statusMsg)
	}
	statusLine := " " + icon + " " + styleHeader.Render(label) + extras

	// Line 3: run breadcrumb
	runLine := breadcrumbDimStyle.Render(" Run: " + truncate(m.selectedRun.Name, m.width-8))

	// Content area: steps list while running, log viewport when done
	var content string
	if isRunning(m.selectedJob.Status) {
		content = m.renderStepsContent()
	} else if !m.logLoaded {
		content = "\n " + m.spinner.View() + " Loading logs…"
	} else {
		content = m.logViewport.View()
	}

	// Filter bar (shown above footer when active)
	var filterBar string
	if m.logFilterMode {
		cursor := styleAccent.Render("█")
		countStr := ""
		if m.logFilter != "" {
			lower := strings.ToLower(m.logFilter)
			count := 0
			for _, line := range strings.Split(m.logRaw, "\n") {
				if strings.Contains(strings.ToLower(line), lower) {
					count++
				}
			}
			countStr = styleDim.Render(fmt.Sprintf("  (%d lines)", count))
		}
		filterBar = filterBarStyle.Width(m.width).Render("  / " + m.logFilter + cursor + countStr)
	}

	// Footer hints vary by context
	var footerHints []string
	switch {
	case m.logFilterMode:
		footerHints = []string{"<esc> clear filter", "<enter> close bar", "<↑/↓> scroll"}
	case isRunning(m.selectedJob.Status):
		footerHints = []string{"<o> open", "<r> refresh", "<esc/b> back", "<q> quit"}
	default:
		footerHints = []string{
			"<↑/↓> scroll", "<g> top", "<G> bottom", "<a> auto-scroll",
			"</> filter", "<c> copy", "<o> open", "<r> refresh", "<esc/b> back", "<q> quit",
		}
	}
	footer := renderFooter(footerHints)

	parts := []string{appBar, statusLine, runLine, content}
	if m.logFilterMode {
		parts = append(parts, filterBar)
	}
	parts = append(parts, footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// ─── Log rendering ────────────────────────────────────────────────────────────

// renderLogs transforms raw log text into ANSI-styled output.
func renderLogs(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = renderLogLine(line)
	}
	return strings.Join(result, "\n")
}

func renderLogLine(line string) string {
	var rendered string
	switch {
	case strings.HasPrefix(line, "##[group]"):
		name := strings.TrimPrefix(line, "##[group]")
		rendered = styleAccent.Render("▶ " + name)
	case strings.HasPrefix(line, "##[endgroup]"):
		rendered = styleDim.Render(strings.Repeat("─", 60))
	case strings.HasPrefix(line, "##[error]"):
		msg := strings.TrimPrefix(line, "##[error]")
		rendered = styleError.Render("✗ " + msg)
	case strings.HasPrefix(line, "##[warning]"):
		msg := strings.TrimPrefix(line, "##[warning]")
		rendered = styleWarn.Render("⚠ " + msg)
	case strings.HasPrefix(line, "##[command]"):
		msg := strings.TrimPrefix(line, "##[command]")
		rendered = styleCmd.Render("$ " + msg)
	default:
		rendered = line
	}
	return rendered
}
