The ec2nodeclasses on my cluster contain spec.amiSelectorTerms. There is only on term and it matches with a wildcard against the available private ami images to my cluster.

Using the kubectl cli and the aws cli, I'd like a script that collects the ec2nodeClass objects on my current cluster, find the matching suite of ami images that i can use instead.

The matching suite will be in the format: `domino-eks-<nodegroup>-<k8s-version>-vYYYYMMDD`

So when I run the script, it looks at the current nodeclasses, finds the ami images that match nodegroup and k8s version, and then offers me the `vYYYYMMDD` versions i can apply. Then when i select one, it edits all the nodegroups to have a specific image name, maintaining nodegroup. This should be re-entrant, so the next time the script runs, it wont be a wildcard.

## Building and Running

To build the Go program:

```bash
go build -o upgrade-ami main.go
```

To run it:

```bash
./upgrade-ami
```

The program will:
1. Connect to your cluster and retrieve all EC2NodeClass objects
2. Query AWS for available AMI versions matching your nodegroups and k8s version
3. Display an interactive terminal UI to select the desired version
4. Show a dry-run summary of all changes that will be made
5. Ask for confirmation before applying changes
6. Update all nodeclasses to use the selected AMI version
