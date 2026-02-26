package main

import "github.com/charmbracelet/lipgloss"

// ANSI 256-color palette
const (
	colorCyan     = lipgloss.Color("51")
	colorAmber    = lipgloss.Color("214")
	colorGreen    = lipgloss.Color("76")
	colorRed      = lipgloss.Color("196")
	colorBlue     = lipgloss.Color("39")
	colorGray     = lipgloss.Color("242")
	colorDimText  = lipgloss.Color("245")
	colorWhite    = lipgloss.Color("15")
	colorYellow   = lipgloss.Color("226")
	colorHeaderBg = lipgloss.Color("24")  // dark cyan bg for top bar
	colorSelected = lipgloss.Color("63")  // cornflower blue — visible on dark bg
)

var (
	// Header bar (k9s-style top bar)
	headerBarStyle = lipgloss.NewStyle().
			Background(colorHeaderBg).
			Foreground(colorWhite)

	appNameStyle = lipgloss.NewStyle().
			Background(colorCyan).
			Foreground(lipgloss.Color("0")).
			Bold(true).
			Padding(0, 1)

	// Breadcrumb / view title line
	breadcrumbStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	breadcrumbDimStyle = lipgloss.NewStyle().
				Foreground(colorDimText)

	// Column header row
	colHeaderStyle = lipgloss.NewStyle().
			Foreground(colorDimText).
			Bold(true)

	// List item styles
	normalItemStyle = lipgloss.NewStyle().
			Foreground(colorWhite)

	selectedItemStyle = lipgloss.NewStyle().
				Background(colorSelected).
				Foreground(colorWhite).
				Bold(true).
				Width(0) // Will be updated dynamically

	// Footer key hints
	footerStyle = lipgloss.NewStyle().
			Foreground(colorGray)

	keyStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	// Log rendering
	styleAccent = lipgloss.NewStyle().Foreground(colorCyan)
	styleError  = lipgloss.NewStyle().Foreground(colorRed)
	styleWarn   = lipgloss.NewStyle().Foreground(colorYellow)
	styleCmd    = lipgloss.NewStyle().Foreground(colorGray)
	styleDim    = lipgloss.NewStyle().Foreground(colorGray)
	styleHeader = lipgloss.NewStyle().Foreground(colorWhite).Bold(true)

	// Filter bar (log search)
	filterBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(colorCyan)

	// Status badge styles
	statusInProgress = lipgloss.NewStyle().Foreground(colorAmber)
	statusSuccess    = lipgloss.NewStyle().Foreground(colorGreen)
	statusFailure    = lipgloss.NewStyle().Foreground(colorRed)
	statusQueued     = lipgloss.NewStyle().Foreground(colorBlue)
	statusNeutral    = lipgloss.NewStyle().Foreground(colorGray)
)

func statusIcon(status, conclusion string) string {
	switch {
	case status == "in_progress":
		return statusInProgress.Render("●")
	case conclusion == "success":
		return statusSuccess.Render("✓")
	case conclusion == "failure":
		return statusFailure.Render("✗")
	case status == "queued":
		return statusQueued.Render("○")
	case conclusion == "cancelled":
		return statusNeutral.Render("⊘")
	case conclusion == "skipped":
		return statusNeutral.Render("–")
	default:
		return styleDim.Render("○")
	}
}

// getPlainStatusIcon returns just the icon character without any styling
func getPlainStatusIcon(status, conclusion string) string {
	switch {
	case status == "in_progress":
		return "●"
	case conclusion == "success":
		return "✓"
	case conclusion == "failure":
		return "✗"
	case status == "queued":
		return "○"
	case conclusion == "cancelled":
		return "⊘"
	case conclusion == "skipped":
		return "–"
	default:
		return "○"
	}
}

func statusLabel(status, conclusion string) string {
	if status == "in_progress" {
		return "in progress"
	}
	if conclusion != "" {
		return conclusion
	}
	return status
}
