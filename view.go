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
		return ""
	}
	switch m.state {
	case stateMenu:
		return m.viewMenu()
	case stateRuns:
		return m.viewRuns()
	case stateJobs:
		return m.viewJobs()
	case stateLogs:
		return m.viewLogs()
	case statePRs:
		return m.viewPRs()
	case stateWorkflows:
		return m.viewWorkflows()
	case stateDispatchForm:
		return m.viewDispatchForm()
	}
	return ""
}

// ─── Shared header / footer ───────────────────────────────────────────────────

func (m model) renderAppBar(viewName string) string {
	left := appNameStyle.Render("tgh")
	right := " " + m.client.owner + "/" + m.client.repo + " "

	usedWidth := lipgloss.Width(left) + lipgloss.Width(viewName) + lipgloss.Width(right)
	gap := max(0, m.width-usedWidth)

	bar := left + " " + viewName + strings.Repeat(" ", gap) + right
	return headerBarStyle.Width(m.width).Render(bar)
}

func renderFooter(hints []string) string {
	parts := make([]string, len(hints))
	for i, h := range hints {
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

// ─── Menu view ────────────────────────────────────────────────────────────────

var menuItems = []struct {
	name string
	desc string
}{
	{"Actions", "Workflow runs, logs and dispatch"},
	{"Pull Requests", "Open pull requests and their checks"},
}

func (m model) viewMenu() string {
	appBar := m.renderAppBar("Menu")

	var sb strings.Builder
	sb.WriteString("\n")
	for i, item := range menuItems {
		var line string
		if i == m.menuIndex {
			bg := lipgloss.Color("63")
			bgPlain := lipgloss.NewStyle().Background(bg)
			prefix := bgPlain.Render(" ▶ ")
			name := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("15")).Bold(true).Width(22).Render(item.name)
			sep := bgPlain.Render("  ")
			desc := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("245")).Render(item.desc)
			line = prefix + name + sep + desc
			// Pad to full terminal width so the highlight spans the whole row.
			if vis := lipgloss.Width(line); vis < m.width {
				line += bgPlain.Render(strings.Repeat(" ", m.width-vis))
			}
		} else {
			nameCol := lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Width(22).Render(item.name)
			line = "   " + nameCol + "  " + styleDim.Render(item.desc)
		}
		sb.WriteString(line + "\n")
	}

	// Pad remaining space
	used := 1 + len(menuItems) + 2 // appbar + blank + items + footer
	remaining := max(0, m.height-used)
	sb.WriteString(strings.Repeat("\n", remaining))

	footer := renderFooter([]string{
		"<↑/↓> navigate",
		"<enter> open",
		"<q> quit",
	})

	return lipgloss.JoinVertical(lipgloss.Left,
		appBar,
		sb.String(),
		footer,
	)
}

// ─── Runs view ────────────────────────────────────────────────────────────────

func (m model) viewRuns() string {
	var viewLabel string
	if m.loading && len(m.runsList.Items()) == 0 {
		viewLabel = m.spinner.View() + " Loading runs…"
	} else {
		viewLabel = fmt.Sprintf("Runs [%d]", len(m.runsList.Items()))
	}
	appBar := m.renderAppBar(viewLabel)

	var breadcrumb string
	if m.statusMsg != "" {
		breadcrumb = styleDim.Width(m.width).Render(" " + m.statusMsg)
	} else if m.selectedPR != nil {
		prLabel := truncate(fmt.Sprintf("#%d %s", m.selectedPR.Number, m.selectedPR.Title), m.width-30)
		breadcrumb = breadcrumbDimStyle.Width(m.width).Render(
			" Pull Requests › " + prLabel + " › Runs",
		)
	} else {
		breadcrumb = breadcrumbDimStyle.Width(m.width).Render(" Actions › Runs")
	}

	colHeaders := m.runColHeaders()
	listView := m.runsList.View()

	footerHints := []string{
		"<enter> open",
		"<r> rerun-failed",
		"<R> rerun-all",
		"<d> dispatch",
		"<o> browser",
		"<tab> refresh",
		"<esc/b> back",
		"<q> quit",
	}
	footer := renderFooter(footerHints)

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
	var viewLabel string
	if m.loading && len(m.jobsList.Items()) == 0 {
		viewLabel = m.spinner.View() + " Loading jobs…"
	} else {
		viewLabel = fmt.Sprintf("Jobs [%d]", len(m.jobsList.Items()))
	}
	appBar := m.renderAppBar(viewLabel)

	var breadcrumb string
	if m.statusMsg != "" {
		breadcrumb = styleDim.Width(m.width).Render(" " + m.statusMsg)
	} else {
		runLabel := truncate(m.selectedRun.Name, m.width-30)
		var prefix string
		if m.selectedPR != nil {
			prefix = fmt.Sprintf(" Pull Requests › #%d › Runs › ", m.selectedPR.Number)
		} else {
			prefix = " Actions › Runs › "
		}
		breadcrumb = breadcrumbDimStyle.Width(m.width).Render(prefix + runLabel)
	}

	colHeaders := m.jobColHeaders()
	listView := m.jobsList.View()

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

// ─── PRs view ─────────────────────────────────────────────────────────────────

func (m model) viewPRs() string {
	var viewLabel string
	if m.loading && len(m.prsList.Items()) == 0 {
		viewLabel = m.spinner.View() + " Loading pull requests…"
	} else {
		viewLabel = fmt.Sprintf("Pull Requests [%d]", len(m.prsList.Items()))
	}
	appBar := m.renderAppBar(viewLabel)

	var breadcrumb string
	if m.statusMsg != "" {
		breadcrumb = styleDim.Width(m.width).Render(" " + m.statusMsg)
	} else {
		breadcrumb = breadcrumbDimStyle.Width(m.width).Render(" Pull Requests")
	}

	colHeaders := m.prColHeaders()
	listView := m.prsList.View()

	footer := renderFooter([]string{
		"<enter> open runs",
		"<o> browser",
		"<r/tab> refresh",
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

func (m model) prColHeaders() string {
	const (
		cursorW = 3
		numW    = 6
		branchW = 18
		authorW = 14
		ageW    = 8
		gaps    = 4
	)
	titleW := max(8, m.width-cursorW-numW-branchW-authorW-ageW-gaps)

	num := lipgloss.NewStyle().Width(numW).Render("#")
	title := lipgloss.NewStyle().Width(titleW).Render("TITLE")
	branch := lipgloss.NewStyle().Width(branchW).Render("BRANCH")
	author := lipgloss.NewStyle().Width(authorW).Render("AUTHOR")
	age := lipgloss.NewStyle().Width(ageW).Render("AGE")

	// Align to match formatPRRow: "    " (4 spaces) + num + " " + title + ...
	return colHeaderStyle.Render("     " + num + " " + title + " " + branch + " " + author + " " + age)
}

// ─── Dispatch form view ───────────────────────────────────────────────────────

func (m model) viewDispatchForm() string {
	name := m.selectedWorkflow.Name
	appBar := m.renderAppBar("Dispatch › " + truncate(name, m.width-20))

	var breadcrumb string
	if m.loading {
		breadcrumb = styleDim.Width(m.width).Render(" " + m.statusMsg)
	} else {
		breadcrumb = breadcrumbDimStyle.Width(m.width).Render(
			" Actions › Runs › Dispatch › " + truncate(name, m.width-35),
		)
	}

	var sb strings.Builder
	sb.WriteString("\n")

	for i, f := range m.formFields {
		active := i == m.formActiveField

		// Label line
		labelText := f.label
		if f.required {
			labelText += " [required]"
		}
		var labelLine string
		if active {
			labelLine = "  " + styleHeader.Render(labelText)
		} else {
			labelLine = "  " + styleDim.Render(labelText)
		}
		if f.description != "" {
			labelLine += "  " + styleDim.Render(f.description)
		}
		sb.WriteString(labelLine + "\n")

		// Input widget
		sb.WriteString("  " + f.input.View() + "\n")

		// Ref field: section-tab browser (Input / Branches / Tags)
		if i == 0 && active && (len(m.refBranches) > 0 || len(m.refTags) > 0) {
			filter := strings.ToLower(f.input.Value())
			fb := filterRefs(m.refBranches, filter)
			ft := filterRefs(m.refTags, filter)

			// Section tab bar
			hilite := lipgloss.NewStyle().Foreground(colorWhite).Bold(true)
			var inputTab, branchTab, tagTab string
			branchLabel := fmt.Sprintf("Branches (%d)", len(fb))
			tagLabel := fmt.Sprintf("Tags (%d)", len(ft))
			switch m.refSection {
			case 0:
				inputTab = hilite.Render("[Input]")
				branchTab = styleDim.Render(branchLabel)
				tagTab = styleDim.Render(tagLabel)
			case 1:
				inputTab = styleDim.Render("Input")
				branchTab = hilite.Render("[" + branchLabel + "]")
				tagTab = styleDim.Render(tagLabel)
			case 2:
				inputTab = styleDim.Render("Input")
				branchTab = styleDim.Render(branchLabel)
				tagTab = hilite.Render("[" + tagLabel + "]")
			}
			sb.WriteString("  " + inputTab + "  " + branchTab + "  " + tagTab + "\n")

			// List for the active section (1=branches, 2=tags)
			const maxVisible = 6
			var listRefs []string
			var listIdx int
			switch m.refSection {
			case 1:
				listRefs, listIdx = fb, m.refBranchIdx
			case 2:
				listRefs, listIdx = ft, m.refTagIdx
			}
			if len(listRefs) == 0 && m.refSection != 0 {
				sb.WriteString("  " + styleDim.Render("(no matches)") + "\n")
			} else if len(listRefs) > 0 {
				start := listIdx - maxVisible/2
				if start < 0 {
					start = 0
				}
				if start+maxVisible > len(listRefs) {
					start = max(0, len(listRefs)-maxVisible)
				}
				end := start + maxVisible
				if end > len(listRefs) {
					end = len(listRefs)
				}
				for j := start; j < end; j++ {
					opt := listRefs[j]
					if j == listIdx {
						sb.WriteString("  " + lipgloss.NewStyle().
							Background(colorSelected).
							Foreground(colorWhite).
							Bold(true).
							Render("▶ "+opt) + "\n")
					} else {
						sb.WriteString("  " + styleDim.Render("  "+opt) + "\n")
					}
				}
				if len(listRefs) > maxVisible {
					sb.WriteString("  " + styleDim.Render(fmt.Sprintf("%d / %d", listIdx+1, len(listRefs))) + "\n")
				}
			}
		}

		// Hints for choice/boolean types
		switch f.fieldType {
		case "choice":
			if len(f.options) > 0 {
				var parts []string
				for j, opt := range f.options {
					if j == f.optionIdx {
						parts = append(parts, styleAccent.Render("▶"+opt))
					} else {
						parts = append(parts, styleDim.Render(opt))
					}
				}
				sb.WriteString("  " + styleDim.Render("↑/↓  ") + strings.Join(parts, styleDim.Render(" · ")) + "\n")
			}
		case "boolean":
			sb.WriteString("  " + styleDim.Render("space / ↑↓  toggle") + "\n")
		}

		sb.WriteString("\n")
	}

	// Cancel / Build buttons
	btnCancel := styleDim.Render("  Cancel  ")
	btnBuild := styleDim.Render("  Build  ")
	btnFocus := lipgloss.NewStyle().Background(colorSelected).Foreground(colorWhite).Bold(true)
	switch m.formButton {
	case 1:
		btnCancel = btnFocus.Render("  Cancel  ")
	case 2:
		btnBuild = btnFocus.Render("  Build  ")
	}
	sb.WriteString("  " + btnCancel + "   " + btnBuild + "\n")

	var footerHints []string
	if m.formButton != 0 {
		footerHints = []string{"<←/→> switch", "<enter> confirm", "<tab> fields", "<esc> back"}
	} else {
		footerHints = []string{"<tab> next", "<←/→> section", "<↑/↓> navigate", "<enter> select", "<esc> back"}
	}
	footer := renderFooter(footerHints)

	// Pad remaining space to push footer to the bottom.
	content := sb.String()
	contentLines := strings.Count(content, "\n")
	remaining := max(0, m.height-3-contentLines)
	content += strings.Repeat("\n", remaining)

	return lipgloss.JoinVertical(lipgloss.Left,
		appBar,
		breadcrumb,
		content,
		footer,
	)
}

// ─── Workflows view ───────────────────────────────────────────────────────────

func (m model) viewWorkflows() string {
	var viewLabel string
	if m.loading {
		if len(m.workflowsList.Items()) == 0 {
			viewLabel = m.spinner.View() + " Loading workflows…"
		} else {
			viewLabel = m.spinner.View() + " Fetching inputs…"
		}
	} else {
		viewLabel = fmt.Sprintf("Dispatch [%d]", len(m.workflowsList.Items()))
	}
	appBar := m.renderAppBar(viewLabel)

	var breadcrumb string
	if m.statusMsg != "" {
		breadcrumb = styleDim.Width(m.width).Render(" " + m.statusMsg)
	} else {
		ref := m.defaultBranch
		if ref == "" {
			ref = "…"
		}
		breadcrumb = breadcrumbDimStyle.Width(m.width).Render(
			" Actions › Runs › Dispatch  (triggers on " + styleAccent.Render(ref) + ")",
		)
	}

	colHeaders := m.workflowColHeaders()
	listView := m.workflowsList.View()

	ref := m.defaultBranch
	if ref == "" {
		ref = "main"
	}
	footer := renderFooter([]string{
		"<enter> dispatch on " + ref,
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

func (m model) workflowColHeaders() string {
	const (
		cursorW = 3
		fileW   = 30
		gaps    = 1
	)
	nameW := max(8, m.width-cursorW-fileW-gaps)

	file := lipgloss.NewStyle().Width(fileW).Render("FILE")
	name := lipgloss.NewStyle().Width(nameW).Render("NAME")

	return colHeaderStyle.Render("     " + file + " " + name)
}

// ─── Logs view ────────────────────────────────────────────────────────────────

func (m model) renderStepsContent() string {
	steps := m.selectedJob.Steps
	if len(steps) == 0 {
		return "\n " + m.spinner.View() + " Waiting for steps…"
	}

	nameW := max(4, m.width-3)

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
		default:
			line = " " + icon + " " + styleDim.Render(truncate(s.Name, nameW))
		}
		lines = append(lines, line)
	}

	text := strings.Join(lines, "\n")
	lineCount := strings.Count(text, "\n") + 1
	if pad := m.logViewport.Height - lineCount; pad > 0 {
		text += strings.Repeat("\n", pad)
	}
	return text
}

func (m model) viewLogs() string {
	jobLabel := truncate(m.selectedJob.Name, m.width-40)
	var progressSuffix string
	if isRunning(m.selectedJob.Status) {
		total := len(m.selectedJob.Steps)
		done := countCompletedSteps(m.selectedJob.Steps)
		if total > 0 {
			progressSuffix = fmt.Sprintf("  %d/%d steps", done, total)
		}
	} else {
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

	// Build breadcrumb reflecting full path
	var runBreadcrumb string
	if m.selectedPR != nil {
		runBreadcrumb = fmt.Sprintf(" PR #%d › Run: %s", m.selectedPR.Number,
			truncate(m.selectedRun.Name, m.width-20))
	} else {
		runBreadcrumb = " Run: " + truncate(m.selectedRun.Name, m.width-8)
	}
	runLine := breadcrumbDimStyle.Render(runBreadcrumb)

	var content string
	if isRunning(m.selectedJob.Status) {
		content = m.renderStepsContent()
	} else if !m.logLoaded {
		content = "\n " + m.spinner.View() + " Loading logs…"
	} else {
		content = m.logViewport.View()
	}

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
