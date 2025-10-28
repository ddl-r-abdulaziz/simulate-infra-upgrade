package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"upgrade-ami/pkg/amis"
	"upgrade-ami/pkg/nodeclasses"
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
	version string
	date    string
}

func (i item) FilterValue() string { return i.version }
func (i item) Title() string       { return i.version }
func (i item) Description() string { return i.date }

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
				m.choice = i.version
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

	selectedVersion := finalModel.(model).choice
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
	fmt.Println("You can verify the changes with:")
	fmt.Println("  kubectl get nodeclaims.karpenter.sh -owide")
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
