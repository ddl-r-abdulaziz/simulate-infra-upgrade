package nodeclasses

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// EC2NodeClass represents a Karpenter EC2NodeClass resource
type EC2NodeClass struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		AMISelectorTerms []struct {
			Name  string `json:"name"`
			Owner string `json:"owner"`
		} `json:"amiSelectorTerms"`
	} `json:"spec"`
}

// NodeClassList represents a list of EC2NodeClass resources
type NodeClassList struct {
	Items []EC2NodeClass `json:"items"`
}

// AMIPattern represents the parsed components of an AMI name
type AMIPattern struct {
	HasNodegroup bool
	Nodegroup    string
	K8sVersion   string
	Version      string
}

// ParseAMIName parses an AMI name to extract nodegroup, k8s version, and version
func ParseAMIName(amiName string) (*AMIPattern, error) {
	// Format can be either:
	// - domino-eks-<nodegroup>-<k8s-version>-vYYYYMMDD (e.g., domino-eks-gpu-1.33-v20251001)
	// - domino-eks-<k8s-version>-vYYYYMMDD (e.g., domino-eks-1.33-v20251001)
	// Or with wildcards:
	// - domino-eks-<nodegroup>-<k8s-version>-* (e.g., domino-eks-gpu-1.33-*)
	// - domino-eks-<k8s-version>-* (e.g., domino-eks-1.33-*)

	// Try pattern with nodegroup first (with version)
	reWithNodegroupVersion := regexp.MustCompile(`^domino-eks-([^-]+)-(1\.[0-9]+)-v([0-9]{8})$`)
	matches := reWithNodegroupVersion.FindStringSubmatch(amiName)

	if len(matches) == 4 {
		return &AMIPattern{
			HasNodegroup: true,
			Nodegroup:    matches[1],
			K8sVersion:   matches[2],
			Version:      matches[3],
		}, nil
	}

	// Try pattern without nodegroup (with version)
	reWithoutNodegroupVersion := regexp.MustCompile(`^domino-eks-(1\.[0-9]+)-v([0-9]{8})$`)
	matches = reWithoutNodegroupVersion.FindStringSubmatch(amiName)

	if len(matches) == 3 {
		return &AMIPattern{
			HasNodegroup: false,
			Nodegroup:    "",
			K8sVersion:   matches[1],
			Version:      matches[2],
		}, nil
	}

	// Try pattern with nodegroup but wildcard (no version yet)
	reWithNodegroupWildcard := regexp.MustCompile(`^domino-eks-([^-]+)-(1\.[0-9]+)-.*$`)
	matches = reWithNodegroupWildcard.FindStringSubmatch(amiName)

	if len(matches) == 3 {
		return &AMIPattern{
			HasNodegroup: true,
			Nodegroup:    matches[1],
			K8sVersion:   matches[2],
			Version:      "", // Will be selected by user
		}, nil
	}

	// Try pattern without nodegroup but wildcard (no version yet)
	reWithoutNodegroupWildcard := regexp.MustCompile(`^domino-eks-(1\.[0-9]+)-.*$`)
	matches = reWithoutNodegroupWildcard.FindStringSubmatch(amiName)

	if len(matches) == 2 {
		return &AMIPattern{
			HasNodegroup: false,
			Nodegroup:    "",
			K8sVersion:   matches[1],
			Version:      "", // Will be selected by user
		}, nil
	}

	return nil, fmt.Errorf("invalid AMI name format: %s", amiName)
}

// GetEC2NodeClasses retrieves all EC2NodeClass objects from the cluster
func GetEC2NodeClasses() (NodeClassList, error) {
	cmd := exec.Command("kubectl", "get", "ec2nodeclass", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return NodeClassList{}, fmt.Errorf("failed to get nodeclasses: %w", err)
	}

	var nodeClasses NodeClassList
	if err := json.Unmarshal(output, &nodeClasses); err != nil {
		return NodeClassList{}, fmt.Errorf("failed to parse nodeclasses: %w", err)
	}

	return nodeClasses, nil
}

// UpdateNodeClass updates the AMI name in an EC2NodeClass
func UpdateNodeClass(name, newAMI string) error {
	// Get the current nodeclass
	cmd := exec.Command("kubectl", "get", "ec2nodeclass", name, "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get nodeclass %s: %w", name, err)
	}

	// Update the AMI name in the JSON
	var nodeclass map[string]interface{}
	if err := json.Unmarshal(output, &nodeclass); err != nil {
		return fmt.Errorf("failed to parse nodeclass JSON: %w", err)
	}

	// Navigate to spec.amiSelectorTerms[0].name and update it
	spec := nodeclass["spec"].(map[string]interface{})
	amiSelectorTerms := spec["amiSelectorTerms"].([]interface{})
	amiSelectorTerms[0].(map[string]interface{})["name"] = newAMI

	// Apply the changes
	updatedJSON, err := json.Marshal(nodeclass)
	if err != nil {
		return fmt.Errorf("failed to marshal updated JSON: %w", err)
	}

	applyCmd := exec.Command("kubectl", "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(string(updatedJSON))
	applyCmd.Stdout = os.Stdout
	applyCmd.Stderr = os.Stderr

	if err := applyCmd.Run(); err != nil {
		return fmt.Errorf("failed to apply changes: %w", err)
	}

	return nil
}

// NodeClassInfo contains metadata about a nodeclass
type NodeClassInfo struct {
	HasNodegroup bool
	Nodegroup    string
}

// BuildNodeClassMap builds a map of nodeclass names to their info
func BuildNodeClassMap(nodeClasses NodeClassList) map[string]*NodeClassInfo {
	nodeclassMap := make(map[string]*NodeClassInfo)

	for _, nc := range nodeClasses.Items {
		if len(nc.Spec.AMISelectorTerms) > 0 {
			pattern, err := ParseAMIName(nc.Spec.AMISelectorTerms[0].Name)
			if err == nil {
				ng := pattern.Nodegroup
				// If AMI name doesn't have explicit nodegroup, derive from nodeclass name
				if !pattern.HasNodegroup {
					// Extract nodegroup from nodeclass name (e.g., "domino-eks-compute" -> "compute")
					nameParts := strings.Split(nc.Metadata.Name, "-")
					if len(nameParts) >= 3 && nameParts[0] == "domino" && nameParts[1] == "eks" {
						ng = strings.Join(nameParts[2:], "-")
					}
				}
				nodeclassMap[nc.Metadata.Name] = &NodeClassInfo{
					HasNodegroup: pattern.HasNodegroup,
					Nodegroup:    ng,
				}
			}
		}
	}

	return nodeclassMap
}
