// Package tui implements pgctl's live, screen-recording-friendly terminal UI
// for `pgctl upgrade watch`: a Bubble Tea program that replays one captured
// query at a time against both the source and target instances and fills in
// a comparison table row-by-row as results arrive, instead of dumping a
// static summary at the end like `upgrade compare` does.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackc/pgx/v5/pgxpool"

	"pgctl/internal/upgrade"
)

// Styling. Kept simple and high-contrast so it reads well in a screen
// recording at a distance.
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Padding(0, 1)

	borderStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(0, 1)

	statusOKStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42")). // green
			Bold(true)

	statusRegressedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")). // red
				Bold(true)

	statusMissingStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")). // yellow
				Bold(true)

	progressStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("219"))
)

// queryResultMsg is delivered by replayOneQueryCmd once a single query has
// been replayed against both instances and compared.
type queryResultMsg struct {
	comparison upgrade.Comparison
	err        error
}

// Model is the Bubble Tea model backing `pgctl upgrade watch`. It walks
// through queries sequentially (one in flight at a time) so the table fills
// in visibly for a demo recording, rather than firing every replay
// concurrently and printing a final table all at once.
type Model struct {
	ctx context.Context

	queries []upgrade.QueryStat
	index   int

	sourcePool *pgxpool.Pool
	targetPool *pgxpool.Pool
	threshold  float64

	table   table.Model
	results []upgrade.Comparison
	done    bool
	lastErr error

	width  int
	height int
}

// NewModel constructs a watch Model ready to run via tea.NewProgram.
func NewModel(queries []upgrade.QueryStat, sourcePool, targetPool *pgxpool.Pool, threshold float64) Model {
	columns := []table.Column{
		{Title: "Query", Width: 48},
		{Title: "Before ms", Width: 10},
		{Title: "After ms", Width: 10},
		{Title: "Δ%", Width: 8},
		{Title: "Plan Δ", Width: 8},
		{Title: "Status", Width: 12},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(nil),
		table.WithFocused(false),
		table.WithHeight(20),
	)

	tableStyles := table.DefaultStyles()
	tableStyles.Header = tableStyles.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("63")).
		BorderBottom(true).
		Bold(true)
	tableStyles.Selected = tableStyles.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57"))
	t.SetStyles(tableStyles)

	return Model{
		ctx:        context.Background(),
		queries:    queries,
		sourcePool: sourcePool,
		targetPool: targetPool,
		threshold:  threshold,
		table:      t,
		results:    make([]upgrade.Comparison, 0, len(queries)),
	}
}

// Init kicks off replay of the first query, if there is one.
func (m Model) Init() tea.Cmd {
	if len(m.queries) == 0 {
		return nil
	}
	return replayOneQueryCmd(m.ctx, m.sourcePool, m.targetPool, m.queries[0], m.threshold)
}

// replayOneQueryCmd replays a single query against sourcePool (labeled
// "before") and targetPool (labeled "after"), diffs the two single-result
// reports with upgrade.Compare, and returns the resulting Comparison as a
// queryResultMsg. Any error from either replay call is captured in the
// message rather than causing a panic, so one bad query doesn't crash the
// whole live demo.
func replayOneQueryCmd(ctx context.Context, sourcePool, targetPool *pgxpool.Pool, q upgrade.QueryStat, threshold float64) tea.Cmd {
	return func() tea.Msg {
		single := []upgrade.QueryStat{q}

		beforeReport, err := upgrade.Replay(ctx, sourcePool, single, "before")
		if err != nil {
			return queryResultMsg{err: fmt.Errorf("replaying %q against source: %w", truncateForError(q.Query), err)}
		}

		afterReport, err := upgrade.Replay(ctx, targetPool, single, "after")
		if err != nil {
			return queryResultMsg{err: fmt.Errorf("replaying %q against target: %w", truncateForError(q.Query), err)}
		}

		comparisonReport := upgrade.Compare(beforeReport, afterReport, threshold)
		if len(comparisonReport.Comparisons) == 0 {
			return queryResultMsg{err: fmt.Errorf("no comparison produced for %q", truncateForError(q.Query))}
		}

		return queryResultMsg{comparison: comparisonReport.Comparisons[0]}
	}
}

func truncateForError(s string) string {
	const n = 60
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Update handles incoming messages: new comparison results advance the
// replay loop and grow the table; window resizes keep the table usable;
// keypresses allow quitting mid-run.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Leave room for the header, borders and footer.
		tableHeight := m.height - 8
		if tableHeight < 5 {
			tableHeight = 5
		}
		m.table.SetHeight(tableHeight)

		// Widen the query column to use the extra horizontal space, if any.
		queryWidth := m.width - 60
		if queryWidth < 20 {
			queryWidth = 20
		}
		m.table.SetColumns([]table.Column{
			{Title: "Query", Width: queryWidth},
			{Title: "Before ms", Width: 10},
			{Title: "After ms", Width: 10},
			{Title: "Δ%", Width: 8},
			{Title: "Plan Δ", Width: 8},
			{Title: "Status", Width: 12},
		})
		return m, nil

	case queryResultMsg:
		if msg.err != nil {
			// Record a synthetic "missing" comparison so the row still shows
			// up in the table instead of silently vanishing, and remember
			// the error for the CLI layer to report afterward.
			m.lastErr = msg.err
			q := m.queries[m.index]
			msg.comparison = upgrade.Comparison{
				Query:        q.Query,
				MissingAfter: true,
			}
		}

		m.results = append(m.results, msg.comparison)
		m.table.SetRows(append(m.table.Rows(), renderRow(msg.comparison)))

		m.index++
		if m.index >= len(m.queries) {
			m.done = true
			return m, nil
		}
		return m, replayOneQueryCmd(m.ctx, m.sourcePool, m.targetPool, m.queries[m.index], m.threshold)
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func renderRow(c upgrade.Comparison) table.Row {
	status := statusOKStyle.Render("ok")
	planDelta := "no"
	switch {
	case c.MissingAfter:
		status = statusMissingStyle.Render("MISSING")
	case c.Regressed:
		status = statusRegressedStyle.Render("REGRESSED")
	}
	if c.PlanChanged {
		planDelta = "yes"
	}

	deltaPct := "-"
	afterMs := "-"
	if !c.MissingAfter {
		deltaPct = fmt.Sprintf("%.0f%%", c.DeltaPct*100)
		afterMs = fmt.Sprintf("%.2f", c.AfterMs)
	}

	return table.Row{
		truncateForError(c.Query),
		fmt.Sprintf("%.2f", c.BeforeMs),
		afterMs,
		deltaPct,
		planDelta,
		status,
	}
}

// Results returns the comparisons accumulated so far (all of them, once
// Done() is true). The CLI layer reads this after the program exits to
// decide whether to fail the run.
func (m Model) Results() []upgrade.Comparison {
	return m.results
}

// Done reports whether every query in the batch has been replayed.
func (m Model) Done() bool {
	return m.done
}

// AnyRegression reports whether any completed comparison regressed or was
// missing from the "after" run, mirroring upgrade.ComparisonReport.AnyRegression.
func (m Model) AnyRegression() bool {
	for _, c := range m.results {
		if c.Regressed || c.MissingAfter {
			return true
		}
	}
	return false
}

// View renders the header, table and footer.
func (m Model) View() string {
	var b strings.Builder

	var header string
	if m.done {
		regressed := 0
		for _, c := range m.results {
			if c.Regressed || c.MissingAfter {
				regressed++
			}
		}
		header = fmt.Sprintf("Done — %d/%d regressed", regressed, len(m.results))
	} else {
		header = fmt.Sprintf("Replaying query %d/%d...", m.index+1, len(m.queries))
	}
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(progressStyle.Render(progressBar(m.index, len(m.queries), 40)))
	b.WriteString("\n\n")

	b.WriteString(borderStyle.Render(m.table.View()))
	b.WriteString("\n")

	footer := "q to quit"
	if m.lastErr != nil {
		footer = fmt.Sprintf("last error: %v  |  %s", m.lastErr, footer)
	}
	b.WriteString(footerStyle.Render(footer))

	return b.String()
}

func progressBar(current, total, width int) string {
	if total == 0 {
		return ""
	}
	filled := current * width / total
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", width-filled) + "]"
}
