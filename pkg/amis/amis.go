package amis

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// AMIInfo represents information about an AMI
type AMIInfo struct {
	Name         string
	ImageID      string
	CreationDate string
}

// GetAvailableAMIs retrieves all AMIs owned by the specified owner ID
func GetAvailableAMIs(ownerID string) ([]AMIInfo, error) {
	cmd := exec.Command("aws", "ec2", "describe-images",
		"--owners", ownerID,
		"--query", "Images[*].[Name,ImageId,CreationDate]",
		"--output", "text",
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get AMIs: %w", err)
	}

	var amis []AMIInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			amis = append(amis, AMIInfo{
				Name:         parts[0],
				ImageID:      parts[1],
				CreationDate: parts[2],
			})
		}
	}

	return amis, nil
}

// VersionItem represents a version with its creation date
type VersionItem struct {
	Version string
	Date    string
}

// ExtractVersions filters AMIs and extracts unique versions for the given k8s version
func ExtractVersions(amis []AMIInfo, k8sVersion string) ([]VersionItem, error) {
	versionSet := make(map[string]string) // version -> date
	patternWithNodegroup := regexp.MustCompile(`^domino-eks-.*-` + regexp.QuoteMeta(k8sVersion) + `-v([0-9]{8})$`)
	patternWithoutNodegroup := regexp.MustCompile(`^domino-eks-` + regexp.QuoteMeta(k8sVersion) + `-v([0-9]{8})$`)

	for _, ami := range amis {
		// Try pattern with nodegroup first
		matches := patternWithNodegroup.FindStringSubmatch(ami.Name)
		if len(matches) != 2 {
			// Try pattern without nodegroup
			matches = patternWithoutNodegroup.FindStringSubmatch(ami.Name)
		}

		if len(matches) == 2 {
			version := matches[1]
			// Keep the most recent date for each version
			if existingDate, exists := versionSet[version]; !exists || ami.CreationDate > existingDate {
				versionSet[version] = ami.CreationDate
			}
		}
	}

	if len(versionSet) == 0 {
		return nil, fmt.Errorf("no matching AMI versions found")
	}

	// Convert to slice
	var versionItems []VersionItem
	for version, dateStr := range versionSet {
		versionItems = append(versionItems, VersionItem{
			Version: version,
			Date:    ParseDate(dateStr),
		})
	}

	// Sort by version descending
	sort.Slice(versionItems, func(i, j int) bool {
		return versionItems[i].Version > versionItems[j].Version
	})

	return versionItems, nil
}

// ParseDate formats a date string
func ParseDate(dateStr string) string {
	t, err := time.Parse("2006-01-02T15:04:05.000Z", dateStr)
	if err != nil {
		return dateStr
	}
	return t.Format("2006-01-02 15:04")
}
