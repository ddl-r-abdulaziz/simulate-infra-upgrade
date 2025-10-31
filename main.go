package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ddl-r-abdulaziz/upgrade-ami/pkg/amis"
	"github.com/ddl-r-abdulaziz/upgrade-ami/pkg/nodeclasses"
)

var (
	titleStyle        = lipgloss.NewStyle().MarginLeft(2)
	itemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
	paginationStyle   = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	helpStyle         = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)
	quitTextStyle     = lipgloss.NewStyle().Margin(1, 0, 2, 4)
)

type item struct {
	version  string
	date     string
	waitOnly bool // true for "just wait" option
}

func (i item) FilterValue() string {
	if i.waitOnly {
		return "wait"
	}
	return i.version
}

func (i item) Title() string {
	if i.waitOnly {
		return "⏳ Just wait (monitor nodeclaims)"
	}
	return i.version
}

func (i item) Description() string {
	if i.waitOnly {
		return "Monitor nodeclaim drift status without making changes"
	}
	return i.date
}

type model struct {
	list     list.Model
	choice   string
	quitting bool
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		return m, nil

	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "enter":
			i, ok := m.list.SelectedItem().(item)
			if ok {
				// Use FilterValue to get the correct choice (handles both version and "wait")
				m.choice = i.FilterValue()
			}
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	return "\n" + m.list.View()
}

func main() {
	fmt.Println("🔍 Collecting EC2NodeClass objects from cluster...")
	fmt.Println()

	// Get all nodeclasses
	nodeClasses, err := nodeclasses.GetEC2NodeClasses()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(nodeClasses.Items) == 0 {
		fmt.Fprintf(os.Stderr, "Error: No EC2NodeClass objects found in cluster\n")
		os.Exit(1)
	}

	// Display found nodeclasses
	fmt.Println("Found EC2NodeClass objects:")
	for _, nc := range nodeClasses.Items {
		if len(nc.Spec.AMISelectorTerms) > 0 {
			fmt.Printf("  - %s (AMI: %s)\n", nc.Metadata.Name, nc.Spec.AMISelectorTerms[0].Name)
		}
	}
	fmt.Println()

	// Parse the first AMI to get nodegroup and k8s version
	var k8sVersion string

	for _, nc := range nodeClasses.Items {
		if len(nc.Spec.AMISelectorTerms) > 0 {
			pattern, err := nodeclasses.ParseAMIName(nc.Spec.AMISelectorTerms[0].Name)
			if err != nil {
				continue
			}
			k8sVersion = pattern.K8sVersion
			break
		}
	}

	if k8sVersion == "" {
		fmt.Fprintf(os.Stderr, "Error: Could not determine k8s version from AMI names\n")
		os.Exit(1)
	}

	fmt.Printf("📋 Detected Kubernetes Version: %s\n", k8sVersion)
	fmt.Println()

	// Get the owner ID
	ownerID := nodeClasses.Items[0].Spec.AMISelectorTerms[0].Owner
	fmt.Printf("🔍 Owner ID: %s\n", ownerID)
	fmt.Println()

	// Build nodeclass map
	nodeclassMap := nodeclasses.BuildNodeClassMap(nodeClasses)

	// Get available AMIs
	fmt.Println("🔍 Querying AWS for available AMI versions...")
	availableAMIs, err := amis.GetAvailableAMIs(ownerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Extract versions
	versionItems, err := amis.ExtractVersions(availableAMIs, k8sVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Convert to items for bubbletea
	var items []list.Item
	// Add "just wait" option at the top
	items = append(items, item{
		waitOnly: true,
	})
	for _, vi := range versionItems {
		items = append(items, item{
			version: fmt.Sprintf("v%s", vi.Version),
			date:    fmt.Sprintf("Created: %s", vi.Date),
		})
	}

	fmt.Println("Select a version:")
	fmt.Println()

	// Initialize bubbletea
	const defaultWidth = 20
	l := list.New(items, itemDelegate{}, defaultWidth, 14)
	l.Title = "Available AMI Versions"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle

	m := model{list: l}
	program := tea.NewProgram(m, tea.WithAltScreen())

	finalModel, err := program.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if finalModel.(model).quitting {
		fmt.Println("Cancelled")
		os.Exit(0)
	}

	selectedItem := finalModel.(model).choice

	// Check if "just wait" was selected
	if selectedItem == "wait" {
		fmt.Println("\n⏳ Monitoring nodeclaim drift status...")
		fmt.Println("Press Ctrl+C to stop monitoring")
		fmt.Println()
		waitForNodeClaims()
		return
	}

	selectedVersion := selectedItem
	if selectedVersion == "" {
		fmt.Println("No version selected")
		os.Exit(0)
	}

	// Remove 'v' prefix to get the date part
	versionDate := strings.TrimPrefix(selectedVersion, "v")

	fmt.Printf("\n✅ Selected version: %s\n", selectedVersion)
	fmt.Println()

	// Dry run: collect all changes first
	type change struct {
		nodeclassName string
		oldAMI        string
		newAMI        string
	}
	var changes []change

	for _, nc := range nodeClasses.Items {
		if len(nc.Spec.AMISelectorTerms) > 0 {
			pattern, err := nodeclasses.ParseAMIName(nc.Spec.AMISelectorTerms[0].Name)
			if err != nil {
				fmt.Printf("⚠️  Skipping %s (could not parse AMI name)\n", nc.Metadata.Name)
				continue
			}

			oldAMI := nc.Spec.AMISelectorTerms[0].Name

			// Get the nodeclass info to determine if it should have a nodegroup
			info, ok := nodeclassMap[nc.Metadata.Name]
			if !ok {
				fmt.Printf("⚠️  Skipping %s (no nodeclass info found)\n", nc.Metadata.Name)
				continue
			}

			// Construct new AMI name based on whether this nodeclass uses a nodegroup
			var newAMI string
			if info.HasNodegroup {
				// Pattern with nodegroup: domino-eks-<nodegroup>-<k8s>-v<version>
				newAMI = fmt.Sprintf("domino-eks-%s-%s-v%s", info.Nodegroup, pattern.K8sVersion, versionDate)
			} else {
				// Pattern without nodegroup: domino-eks-<k8s>-v<version>
				newAMI = fmt.Sprintf("domino-eks-%s-v%s", pattern.K8sVersion, versionDate)
			}

			changes = append(changes, change{
				nodeclassName: nc.Metadata.Name,
				oldAMI:        oldAMI,
				newAMI:        newAMI,
			})
		}
	}

	// Display dry run summary
	fmt.Println("📋 Dry Run - Changes to be made:")
	fmt.Println(strings.Repeat("=", 80))
	for i, ch := range changes {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("NodeClass: %s\n", ch.nodeclassName)
		fmt.Printf("  Old AMI: %s\n", ch.oldAMI)
		fmt.Printf("  New AMI: %s\n", ch.newAMI)
	}
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println()

	// Ask for confirmation
	fmt.Print("Apply changes? (y/N): ")
	var response string
	fmt.Scanln(&response)

	if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
		fmt.Println("Cancelled")
		os.Exit(0)
	}

	fmt.Println()
	fmt.Println("🚀 Applying changes...")
	fmt.Println()

	// Apply the changes
	for _, ch := range changes {
		fmt.Printf("📝 Updating %s...\n", ch.nodeclassName)
		fmt.Printf("   Old: %s\n", ch.oldAMI)
		fmt.Printf("   New: %s\n", ch.newAMI)

		if err := nodeclasses.UpdateNodeClass(ch.nodeclassName, ch.newAMI); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to update %s: %v\n", ch.nodeclassName, err)
			continue
		}

		fmt.Printf("✅ Updated %s\n", ch.nodeclassName)
		fmt.Println()
	}

	fmt.Println("✅ All nodeclasses updated successfully!")
	fmt.Println()

	// Wait for nodeclaims to become undrifted
	fmt.Println("⏳ Waiting for nodeclaims to become undrifted...")
	fmt.Println("Press Ctrl+C to skip waiting")
	fmt.Println()
	waitForNodeClaims()
}

// formatAge formats a duration similar to kubectl age format
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd%dh", days, hours)
}

// waitForNodeClaims waits for nodeclaims to become undrifted and displays status
func waitForNodeClaims() {
	err := nodeclasses.WaitForNodeClaimsUndrifted(5*time.Second, func(statuses []nodeclasses.NodeClaimStatus) bool {
		// Clear screen and display status
		fmt.Print("\033[H\033[2J") // ANSI escape codes to clear screen
		fmt.Println("📊 NodeClaim Drift Status")
		fmt.Println(strings.Repeat("=", 80))

		if len(statuses) == 0 {
			fmt.Println("No nodeclaims found")
			fmt.Println()
			fmt.Println("Press Ctrl+C to exit")
			return true
		}

		driftedCount := 0
		for _, status := range statuses {
			statusIcon := "✅"
			statusText := "Undrifted"
			if status.Drifted {
				statusIcon = "⚠️"
				statusText = "Drifted"
				if status.Reason != "" {
					statusText += fmt.Sprintf(" (%s)", status.Reason)
				}
				driftedCount++
			}
			ageStr := formatAge(status.Age)
			fmt.Printf("%s %s (NodeClass: %s, Age: %s)\n", statusIcon, status.Name, status.NodeClass, ageStr)
			fmt.Printf("   Status: %s\n", statusText)
			fmt.Println()
		}

		fmt.Println(strings.Repeat("=", 80))
		if driftedCount > 0 {
			fmt.Printf("⏳ Waiting... (%d/%d nodeclaims still drifted)\n", driftedCount, len(statuses))
		} else {
			fmt.Println("✅ All nodeclaims are undrifted!")
		}
		fmt.Println("Press Ctrl+C to exit")

		return true // Continue waiting
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n⚠️  Error monitoring nodeclaims: %v\n", err)
		return
	}

	fmt.Println("\n✅ All nodeclaims are now undrifted!")
}

type itemDelegate struct{}

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                            { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}

	str := fmt.Sprintf("%s - %s", it.version, it.Description())

	if index == m.Index() {
		str = "> " + str
		fmt.Fprint(w, selectedItemStyle.Render(str))
	} else {
		fmt.Fprint(w, itemStyle.Render(str))
	}
}
