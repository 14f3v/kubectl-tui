package view

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/tenant"
)

func init() {
	Register("tenants", []string{"tnt", "tenant", "tenants"}, func(d Deps) Page {
		tier := d.TierLabel
		if tier == "" {
			tier = "tier"
		}
		return &tenantsPage{
			theme:    d.Theme,
			provider: tenant.NewCapsule(d.Session.Dyn, d.Session.CS, d.Session.Engine.Sink(), tier),
			ctx:      d.Session.Context(),
		}
	})
}

type tenantTick struct{}

// tenantsPage renders the Capsule tenant dashboard. Tenants and quota usage are
// refreshed on entry and every 30s (quota status.used emits no Tenant events).
type tenantsPage struct {
	theme     style.Theme
	provider  *tenant.Capsule
	ctx       context.Context
	views     []tenant.View
	available bool
	reason    string
	loaded    bool
	cursor    int
}

func (p *tenantsPage) Init() tea.Cmd     { return nil }
func (p *tenantsPage) Title() string     { return "tenants" }
func (p *tenantsPage) Kind() string      { return "" }
func (p *tenantsPage) Namespace() string { return "" }
func (p *tenantsPage) Filter() string    { return "" }
func (p *tenantsPage) SetFilter(string)  {}
func (p *tenantsPage) OnLeave()          {}

func (p *tenantsPage) Summary() Summary {
	total, ok, warn, errc := 0, 0, 0, 0
	total = len(p.views)
	for _, v := range p.views {
		switch v.StatusClass {
		case columns.StatusOK:
			ok++
		case columns.StatusWarn:
			warn++
		case columns.StatusError:
			errc++
		}
	}
	return Summary{Total: total, OK: ok, Warn: warn, Err: errc}
}

func (p *tenantsPage) OnEnter() tea.Cmd { return p.refreshCmd() }

func (p *tenantsPage) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		p.provider.Refresh(p.ctx) // emits a tenant.Snapshot via the sink
		return tenantTick{}
	}
}

func (p *tenantsPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *tenantsPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case tenant.Snapshot:
		p.loaded = true
		p.available = t.Available
		p.reason = t.Reason
		p.views = t.Views
		if p.cursor >= len(p.views) {
			p.cursor = len(p.views) - 1
		}
		return p, nil
	case tenantTick:
		// Schedule the next periodic refresh.
		return p, tea.Tick(30*time.Second, func(time.Time) tea.Msg { return tenantRefresh{} })
	case tenantRefresh:
		return p, p.refreshCmd()
	case tea.KeyPressMsg:
		switch t.String() {
		case "j", "down":
			if p.cursor < len(p.views)-1 {
				p.cursor++
			}
		case "k", "up":
			if p.cursor > 0 {
				p.cursor--
			}
		case "r":
			return p, p.refreshCmd()
		}
	}
	return p, nil
}

type tenantRefresh struct{}

func (p *tenantsPage) View(width, height int) string {
	t := p.theme
	if !p.loaded {
		return "\n" + t.Faint.Render("  loading tenants…")
	}
	if !p.available {
		msgText := "Capsule is not installed on this cluster"
		if p.reason == "forbidden" {
			msgText = "you are not permitted to list tenants (try capsule-proxy)"
		}
		return "\n" + lipgloss.NewStyle().Foreground(t.Pal.Warn).Render("  "+msgText)
	}
	if len(p.views) == 0 {
		return "\n" + t.Faint.Render("  no tenants")
	}

	var b strings.Builder
	header := t.ColHeader.Render(
		pad("  TENANT", 20) + pad("TIER", 9) + pad("NS", 4) + pad("PODS", 6) +
			pad("CPU used/quota", 22) + pad("MEM used/quota", 22) + pad("OWNER", 16) + "STATUS")
	b.WriteString(header + "\n")

	for i, v := range p.views {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		row := marker + nameStyle.Render(pad(v.Name, 18)) +
			p.tierBadge(v.Tier) + " " +
			t.Dim.Render(pad(itoaSmall(v.NS), 4)) +
			t.Dim.Render(pad(itoaSmall(v.Pods), 6)) +
			p.quotaCell(v.CPUUsed, v.CPUQuota, "cores", cpuCores) + " " +
			p.quotaCell(v.MemUsed, v.MemQuota, "Gi", memGi) + " " +
			t.Dim.Render(pad(trunc(v.Owner, 15), 16)) +
			p.statusCell(v)
		b.WriteString(row + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  r refresh · quota usage updates every 30s"))
	return b.String()
}

func (p *tenantsPage) tierBadge(tier string) string {
	if tier == "" {
		return p.theme.Faint.Render(pad("—", 8))
	}
	var fg color.Color = p.theme.Pal.TextDim
	switch strings.ToLower(tier) {
	case "gold":
		fg = p.theme.Pal.Warn
	case "silver":
		fg = p.theme.Pal.Text
	case "bronze":
		fg = p.theme.Pal.Pink
	}
	return lipgloss.NewStyle().Foreground(fg).Render(pad(tier, 8))
}

// quotaCell renders a mini bar plus "used/quota unit". conv converts the raw
// int64 (millicores or bytes) to display units.
func (p *tenantsPage) quotaCell(used, quota int64, unit string, conv func(int64) float64) string {
	t := p.theme
	if quota <= 0 {
		return t.Faint.Render(pad(fmt.Sprintf("%.1f/—%s", conv(used), unit), 21))
	}
	frac := float64(used) / float64(quota)
	bar := miniBar(frac, 6, t)
	txt := fmt.Sprintf("%.1f/%.0f%s", conv(used), conv(quota), unit)
	return bar + " " + t.Dim.Render(pad(txt, 14))
}

func (p *tenantsPage) statusCell(v tenant.View) string {
	var fg color.Color
	switch v.StatusClass {
	case columns.StatusOK:
		fg = p.theme.Pal.OK
	case columns.StatusWarn:
		fg = p.theme.Pal.Warn
	case columns.StatusError:
		fg = p.theme.Pal.Err
	default:
		fg = p.theme.Pal.TextFaint
	}
	return lipgloss.NewStyle().Foreground(fg).Render("● " + v.Status)
}

func cpuCores(millis int64) float64 { return float64(millis) / 1000 }
func memGi(bytes int64) float64     { return float64(bytes) / (1024 * 1024 * 1024) }

// miniBar renders a short proportional bar colored by fill: accent <70%, warn
// <90%, err at/above 90%.
func miniBar(frac float64, width int, t style.Theme) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	n := int(frac*float64(width) + 0.5)
	col := t.Pal.Accent
	switch {
	case frac >= 0.9:
		col = t.Pal.Err
	case frac >= 0.7:
		col = t.Pal.Warn
	}
	filled := lipgloss.NewStyle().Foreground(col).Render(strings.Repeat("█", n))
	empty := t.Faint.Render(strings.Repeat("░", width-n))
	return filled + empty
}

func pad(s string, w int) string {
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if len(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return s[:w-1] + "…"
}

func itoaSmall(n int) string { return fmt.Sprintf("%d", n) }
