# AMI Upgrade Tool

A CLI tool to upgrade EC2NodeClass AMI versions in your Kubernetes cluster with an interactive terminal UI.

**DISCLAIMER** This tool was vibecoded. I only made a modest attempt at checking all code paths.

The Agent thinks too much of this tool. It's purpose is to replace AMI's that we provately publish for our product. **That is all**. 

## Overview

This tool helps you upgrade AMI versions for Karpenter EC2NodeClass objects in your Kubernetes cluster. It:
- Discovers all EC2NodeClass objects in your cluster
- Queries AWS for available AMI versions matching your nodegroups and Kubernetes version
- Provides an interactive terminal UI to select the desired version
- Shows a dry-run summary before applying changes
- Asks for confirmation before making any updates

## Requirements

- Access to a Kubernetes cluster via `kubectl`
- AWS CLI configured with appropriate credentials
- Go 1.21+ (for building from source)

## Installation

### From Source

```bash
git clone <repository-url>
cd simulate-infra-upgrade
go build -o upgrade-ami .
```

## Usage

```bash
./upgrade-ami
```

The tool will:

1. **Discover EC2NodeClasses** - Retrieves all EC2NodeClass objects from your cluster using `kubectl`
2. **Query AWS** - Fetches available AMI versions that match your nodegroups and Kubernetes version
3. **Interactive Selection** - Displays a terminal UI where you can select the desired AMI version using arrow keys
4. **Dry Run Preview** - Shows a summary of all changes that will be made:
   ```
   📋 Dry Run - Changes to be made:
   ================================================================================
   NodeClass: domino-eks-compute
     Old AMI: domino-eks-1.33-*
     New AMI: domino-eks-1.33-v20251001
   
   NodeClass: domino-eks-gpu
     Old AMI: domino-eks-gpu-1.33-*
     New AMI: domino-eks-gpu-1.33-v20251001
   ================================================================================
   ```
5. **Confirmation** - Prompts for confirmation before applying changes (`y/N`)
6. **Apply Updates** - Updates all nodeclasses to use the selected AMI version

## Features

- ✅ Interactive TUI powered by [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- ✅ Dry-run mode to preview changes before applying
- ✅ Handles both wildcard (`*`) and specific AMI versions
- ✅ Supports AMI naming patterns with and without nodegroups
- ✅ Re-entrant: safe to run multiple times
- ✅ Colorful, user-friendly output

## AMI Name Patterns

The tool supports two AMI naming patterns:

1. **With nodegroup**: `domino-eks-<nodegroup>-<k8s-version>-v<YYYYMMDD>`
   - Example: `domino-eks-gpu-1.33-v20251001`

2. **Without nodegroup**: `domino-eks-<k8s-version>-v<YYYYMMDD>`
   - Example: `domino-eks-1.33-v20251001`

The tool automatically detects which pattern your nodeclasses use and maintains consistency when upgrading.

## Verification

After running the tool, verify the changes:

```bash
kubectl get ec2nodeclass -o jsonpath='{.items[*].spec.amiSelectorTerms[0].name}'
```

Or check your node claims:

```bash
kubectl get nodeclaims.karpenter.sh -owide
```

## Architecture

The codebase is organized into reusable packages:

- `pkg/nodeclasses/` - EC2NodeClass management, AMI name parsing, and updates
- `pkg/amis/` - AWS AMI querying and version filtering
- `main.go` - UI orchestration and user interaction

## Project Layout

```
.
├── main.go                 # Main entry point and UI
├── pkg/
│   ├── amis/
│   │   └── amis.go        # AMI querying and version extraction
│   └── nodeclasses/
│       └── nodeclasses.go # NodeClass management and parsing
├── README.md
└── go.mod
```

## License

MIT
